package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/user/whatsmeow-basileia/internal/infrastructure/rabbitmq"
	"github.com/user/whatsmeow-basileia/internal/infrastructure/whatsapp"
	"github.com/user/whatsmeow-basileia/pkg/logger"
	"go.mau.fi/whatsmeow/types"
	"go.uber.org/zap"
)

type SendMessagePayload struct {
	DeviceID   string   `json:"device_id"`
	Number     string   `json:"number,omitempty"`
	Phone      string   `json:"phone,omitempty"`
	Message    string   `json:"message,omitempty"`
	Text       string   `json:"text,omitempty"`
	Type       string   `json:"type,omitempty"`
	Title      string   `json:"title,omitempty"`
	Footer     string   `json:"footer,omitempty"`
	FooterText string   `json:"footerText,omitempty"`
	Buttons    []string `json:"buttons,omitempty"`
	Choices    []string `json:"choices,omitempty"`
	CopyCode   string   `json:"copy_code,omitempty"`
	CopyText   string   `json:"copy_text,omitempty"`
}

type Worker struct {
	rmqClient *rabbitmq.Client
	manager   *whatsapp.MultiClientManager
	logger    *zap.Logger
}

func NewWorker(rmqClient *rabbitmq.Client, manager *whatsapp.MultiClientManager, logger *zap.Logger) *Worker {
	return &Worker{
		rmqClient: rmqClient,
		manager:   manager,
		logger:    logger,
	}
}

func (w *Worker) Start() {
	err := w.rmqClient.Consume("send_message_queue", w.handleSendMessage)
	if err != nil {
		w.logger.Error("Failed to start consuming send_message_queue", zap.Error(err))
	} else {
		w.logger.Info("Started RabbitMQ worker for send_message_queue")
	}
}

func (w *Worker) handleSendMessage(body []byte) error {
	var payload SendMessagePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		w.logger.Error("Failed to unmarshal message payload", zap.Error(err))
		return nil
	}

	// Resolve aliases (Uazapi compatibility)
	if payload.Number == "" && payload.Phone != "" {
		payload.Number = payload.Phone
	}
	if payload.Message == "" && payload.Text != "" {
		payload.Message = payload.Text
	}
	if payload.Footer == "" && payload.FooterText != "" {
		payload.Footer = payload.FooterText
	}
	if len(payload.Buttons) == 0 && len(payload.Choices) > 0 {
		payload.Buttons = payload.Choices
	}

	client := w.manager.GetClient(payload.DeviceID)
	if client == nil {
		w.logger.Warn("Device not found for message sending", zap.String("deviceID", payload.DeviceID))
		return fmt.Errorf("device not found")
	}

	if !client.IsConnected() {
		w.logger.Warn("Device not connected", zap.String("deviceID", payload.DeviceID))
		return fmt.Errorf("device not connected")
	}

	remoteJID, err := types.ParseJID(payload.Number + "@s.whatsapp.net")
	if err != nil {
		w.logger.Error("Invalid phone number format", zap.Error(err))
		return nil
	}

	builder := NewMessageBuilder(client, remoteJID.String())

	if payload.CopyCode != "" {
		builder.WithCopyButton(payload.Message, payload.Footer, payload.Title, payload.CopyText, payload.CopyCode)
	} else if payload.Type == "button" {
		builder.WithButtons(payload.Message, payload.Footer, payload.Title, payload.Buttons)
	} else {
		builder.WithText(payload.Message)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	msgID, err := builder.Send(ctx)
	if err != nil {
		w.logger.Error("Failed to send message via Whatsmeow", zap.Error(err))
		logger.PushLog("error", "Failed to send message via Whatsmeow: "+err.Error())
		return err // Requeue
	}

	w.logger.Info("Message sent successfully via Queue", zap.String("msgID", msgID), zap.String("phone", payload.Phone))
	logger.PushLog("info", "Message sent successfully to "+payload.Phone)
	// Record outgoing statement stat
	w.manager.RecordMessageStat(payload.DeviceID, "out")

	return nil
}
