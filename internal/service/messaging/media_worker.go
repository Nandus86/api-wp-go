package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/user/whatsmeow-basileia/internal/infrastructure/rabbitmq"
	"github.com/user/whatsmeow-basileia/internal/infrastructure/whatsapp"
	"github.com/user/whatsmeow-basileia/internal/service/media"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.uber.org/zap"
)

type SendMediaPayload struct {
	DeviceID string `json:"device_id"`
	Phone    string `json:"phone,omitempty"`
	Number   string `json:"number,omitempty"`
	MediaURL string `json:"media_url"`
	Type     string `json:"type"` // image, video, document, audio
	Caption  string `json:"caption,omitempty"`
	Text     string `json:"text,omitempty"` // Uazapi alias for Caption
	MimeType string `json:"mimetype,omitempty"`
	FileName string `json:"fileName,omitempty"`
}

type MediaWorker struct {
	rmqClient *rabbitmq.Client
	manager   *whatsapp.MultiClientManager
	logger    *zap.Logger
}

func NewMediaWorker(rmqClient *rabbitmq.Client, manager *whatsapp.MultiClientManager, logger *zap.Logger) *MediaWorker {
	return &MediaWorker{
		rmqClient: rmqClient,
		manager:   manager,
		logger:    logger,
	}
}

func (w *MediaWorker) Start() {
	err := w.rmqClient.Consume("send_media_queue", w.handleSendMedia)
	if err != nil {
		w.logger.Error("Failed to start consuming send_media_queue", zap.Error(err))
	} else {
		w.logger.Info("Started RabbitMQ worker for send_media_queue")
	}
}

func (w *MediaWorker) handleSendMedia(body []byte) error {
	var payload SendMediaPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		w.logger.Error("Failed to unmarshal media payload", zap.Error(err))
		return nil // don't requeue bad format
	}

	// Resolve aliases
	if payload.Phone == "" && payload.Number != "" {
		payload.Phone = payload.Number
	}
	if payload.Caption == "" && payload.Text != "" {
		payload.Caption = payload.Text
	}

	client := w.manager.GetClient(payload.DeviceID)
	if client == nil {
		w.logger.Warn("Device not found for media sending", zap.String("deviceID", payload.DeviceID))
		return fmt.Errorf("device not found")
	}

	if !client.IsConnected() {
		w.logger.Warn("Device not connected", zap.String("deviceID", payload.DeviceID))
		return fmt.Errorf("device not connected")
	}

	remoteJID, err := types.ParseJID(payload.Phone + "@s.whatsapp.net")
	if err != nil {
		w.logger.Error("Invalid phone number format", zap.Error(err))
		return nil
	}

	// Map type to whatsmeow.MediaType
	var mediaType whatsmeow.MediaType
	switch strings.ToLower(payload.Type) {
	case "image":
		mediaType = whatsmeow.MediaImage
	case "video":
		mediaType = whatsmeow.MediaVideo
	case "audio":
		mediaType = whatsmeow.MediaAudio
	case "document":
		mediaType = whatsmeow.MediaDocument
	default:
		w.logger.Error("Unsupported media type", zap.String("type", payload.Type))
		return nil
	}

	// Download media
	w.logger.Info("Downloading media...", zap.String("url", payload.MediaURL))
	resp, err := http.Get(payload.MediaURL)
	if err != nil {
		w.logger.Error("Failed to download media", zap.Error(err))
		// We might want to requeue if download fails due to temporary network issues,
		// but returning error requeues. Let's requeue for true network errors.
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		w.logger.Error("Media URL returned non-200 status", zap.Int("status", resp.StatusCode))
		return nil // Not a retryable error usually
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		w.logger.Error("Failed to read media body", zap.Error(err))
		return err
	}

	// Use our MediaService Uploader logic
	uploader := media.NewUploader(client)
	ctxUpload, cancelUpload := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelUpload()

	w.logger.Info("Uploading media to WhatsApp servers...")
	uploadResp, err := uploader.Upload(ctxUpload, data, mediaType)
	if err != nil {
		w.logger.Error("Failed to upload media to WhatsApp", zap.Error(err))
		return err
	}

	// Build message and send
	builder := NewMessageBuilder(client, remoteJID.String())
	builder.WithMedia(uploadResp, mediaType, payload.MimeType, payload.FileName, payload.Caption)

	ctxSend, cancelSend := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelSend()

	msgID, err := builder.Send(ctxSend)
	if err != nil {
		w.logger.Error("Failed to send media message after retries", zap.Error(err))
		return err
	}

	w.logger.Info("Media message sent successfully via queue", zap.String("msgID", msgID), zap.String("phone", payload.Phone))
	return nil
}
