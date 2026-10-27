package messaging

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/user/whatsmeow-basileia/internal/service/media"
	"go.mau.fi/whatsmeow"
	"go.uber.org/zap"
)

type SendMediaPayload struct {
	DeviceID string `json:"device_id"`
	JobID    string `json:"job_id,omitempty"`
	Number   string `json:"number,omitempty"`
	Phone    string `json:"phone,omitempty"`
	MediaURL string `json:"media_url,omitempty"`
	Base64   string `json:"base64,omitempty"`
	Type     string `json:"type"` // image, video, document, audio
	Caption  string `json:"caption,omitempty"`
	Text     string `json:"text,omitempty"` // Basileapi alias for Caption
	MimeType string `json:"mimetype,omitempty"`
	FileName string `json:"fileName,omitempty"`

	SendOptions
}

// MediaRefPrefix marks a media_url that points to an object uploaded via /api/media/upload.
const MediaRefPrefix = "media:"

func (s *Service) handleSendMedia(body []byte) error {
	var payload SendMediaPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		s.log.Error("Failed to unmarshal media payload", zap.Error(err))
		return nil // don't requeue bad format
	}

	// Resolve aliases
	if payload.Number == "" && payload.Phone != "" {
		payload.Number = payload.Phone
	}
	if payload.Caption == "" && payload.Text != "" {
		payload.Caption = payload.Text
	}

	return s.runJob(payload.JobID, payload.DeviceID, func(ctx context.Context, sc *sendContext) sendOutcome {
		client := sc.client

		// Map type to whatsmeow.MediaType
		var mediaType whatsmeow.MediaType
		switch strings.ToLower(payload.Type) {
		case "image":
			mediaType = whatsmeow.MediaImage
		case "video":
			mediaType = whatsmeow.MediaVideo
		case "audio", "ptt", "myaudio":
			mediaType = whatsmeow.MediaAudio
		case "document":
			mediaType = whatsmeow.MediaDocument
		default:
			return sendOutcome{err: fmt.Errorf("unsupported media type %q", payload.Type), permanent: true}
		}

		// Auto-correct number formatting (solves 9th digit in BR) using WhatsApp's directory
		remoteJID, err := ResolveRecipient(ctx, client, payload.Number)
		if err != nil {
			return sendOutcome{err: err, permanent: isPermanent(err)}
		}

		data, err := s.loadMedia(ctx, payload)
		if err != nil {
			return sendOutcome{err: err, permanent: isPermanent(err)}
		}
		if payload.MimeType == "" {
			payload.MimeType = http.DetectContentType(data)
			if mediaType == whatsmeow.MediaAudio {
				payload.MimeType = "" // keep the builder default (ogg/opus PTT)
			}
		}

		// Use our MediaService Uploader logic
		uploader := media.NewUploader(client)
		ctxUpload, cancelUpload := context.WithTimeout(ctx, 2*time.Minute)
		defer cancelUpload()

		s.log.Info("Uploading media to WhatsApp servers...", zap.Int("bytes", len(data)))
		uploadResp, err := uploader.Upload(ctxUpload, data, mediaType)
		if err != nil {
			return sendOutcome{err: fmt.Errorf("upload media to WhatsApp: %w", err)}
		}

		// Build message and send
		builder := NewMessageBuilder(client, remoteJID.String())
		builder.WithMedia(uploadResp, mediaType, payload.MimeType, payload.FileName, payload.Caption)

		out := s.deliver(ctx, sc, builder, payload.SendOptions, len(payload.Caption))
		if out.err == nil && s.cfg.ArchiveMedia && s.history != nil {
			go func(id string) {
				actx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				mime := payload.MimeType
				if mime == "" {
					mime = http.DetectContentType(data)
				}
				// Give the history writer a moment to persist the row first.
				time.Sleep(time.Second)
				if _, err := s.history.ArchiveBytes(actx, sc.instanceID, id, data, mime, time.Now()); err != nil {
					s.log.Warn("Failed to archive outgoing media", zap.Error(err))
				}
			}(out.msgID)
		}
		return out
	})
}

// loadMedia reads the media from base64, an uploaded object (media:<key>) or a URL.
func (s *Service) loadMedia(ctx context.Context, payload SendMediaPayload) ([]byte, error) {
	switch {
	case payload.Base64 != "":
		// Remove data URI prefix if present (e.g. data:image/png;base64,)
		b64str := payload.Base64
		if idx := strings.Index(b64str, ";base64,"); idx != -1 {
			b64str = b64str[idx+8:]
		}
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64str))
		if err != nil {
			return nil, permanentf("invalid base64 media: %v", err)
		}
		return data, nil

	case strings.HasPrefix(payload.MediaURL, MediaRefPrefix):
		if !s.storage.Enabled() {
			return nil, permanentf("media reference used but object storage is not configured")
		}
		data, _, err := s.storage.Get(ctx, strings.TrimPrefix(payload.MediaURL, MediaRefPrefix))
		if err != nil {
			return nil, fmt.Errorf("read uploaded media: %w", err)
		}
		return data, nil

	case payload.MediaURL != "":
		s.log.Info("Downloading media...", zap.String("url", payload.MediaURL))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, payload.MediaURL, nil)
		if err != nil {
			return nil, permanentf("invalid media url: %v", err)
		}
		resp, err := s.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("download media: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return nil, permanentf("media URL returned HTTP %d", resp.StatusCode)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("media URL returned HTTP %d", resp.StatusCode)
		}
		limit := s.cfg.MediaMaxBytes
		if limit <= 0 {
			limit = 64 << 20
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		if err != nil {
			return nil, fmt.Errorf("read media body: %w", err)
		}
		if int64(len(data)) > limit {
			return nil, permanentf("media larger than %d bytes", limit)
		}
		return data, nil
	}
	return nil, permanentf("no media_url nor base64 provided in payload")
}

type permanentError struct{ msg string }

func (e *permanentError) Error() string { return e.msg }

func permanentf(format string, args ...interface{}) error {
	return &permanentError{msg: fmt.Sprintf(format, args...)}
}

func isPermanent(err error) bool {
	var p *permanentError
	return errors.As(err, &p)
}
