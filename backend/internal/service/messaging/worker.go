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

	// Contact
	FullName     string `json:"fullName,omitempty"`
	PhoneNumber  string `json:"phoneNumber,omitempty"`
	Organization string `json:"organization,omitempty"`
	Email        string `json:"email,omitempty"`
	URL          string `json:"url,omitempty"`

	// Location
	Address   string  `json:"address,omitempty"`
	Name      string  `json:"name,omitempty"`
	Latitude  float64 `json:"latitude,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`

	// Presence
	Presence string `json:"presence,omitempty"`

	// Menu / List
	ListButton string `json:"listButton,omitempty"`

	// payment / pix
	Amount  float64 `json:"amount,omitempty"`
	PixKey  string  `json:"pixKey,omitempty"`
	PixType string  `json:"pixType,omitempty"`
	PixName string  `json:"pixName,omitempty"`

	// Carousel pseudo support
	Carousel []CarouselCard `json:"carousel,omitempty"`
}

type CarouselCard struct {
	Text     string `json:"text,omitempty"`
	Image    string `json:"image,omitempty"`
	Video    string `json:"video,omitempty"`
	Document string `json:"document,omitempty"`
	Filename string `json:"filename,omitempty"`
	Buttons  []struct {
		ID   string `json:"id,omitempty"`
		Text string `json:"text,omitempty"`
		Type string `json:"type,omitempty"`
	} `json:"buttons,omitempty"`
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

	var remoteJID types.JID
	if payload.Type == "status" {
		remoteJID = types.NewJID("status", "broadcast")
	} else {
		// Auto-correct number formatting (solves 9th digit in BR) using WhatsApp's directory
		isVerified := false
		resp, err := client.IsOnWhatsApp(context.Background(), []string{payload.Number})
		if err == nil && len(resp) > 0 && resp[0].IsIn {
			remoteJID = resp[0].JID
			isVerified = true
		}

		if !isVerified {
			var parseErr error
			remoteJID, parseErr = types.ParseJID(payload.Number + "@s.whatsapp.net")
			if parseErr != nil {
				w.logger.Error("Invalid phone number format", zap.Error(parseErr))
				return nil
			}
		}
	}

	// Presence bypasses normal message sending if it's explicitly presence update
	if payload.Presence != "" {
		ctx := context.Background()
		if payload.Presence == "composing" {
			client.SendChatPresence(ctx, remoteJID, types.ChatPresenceComposing, types.ChatPresenceMediaText)
		} else if payload.Presence == "recording" {
			client.SendChatPresence(ctx, remoteJID, types.ChatPresenceComposing, types.ChatPresenceMediaAudio)
		} else if payload.Presence == "paused" {
			client.SendChatPresence(ctx, remoteJID, types.ChatPresencePaused, types.ChatPresenceMediaText)
		} else if payload.Presence == "available" {
			client.SendPresence(ctx, types.PresenceAvailable)
		} else {
			client.SendPresence(ctx, types.PresenceUnavailable)
		}
		return nil
	}

	builder := NewMessageBuilder(client, remoteJID.String())

	if payload.Type == "contact" {
		builder.WithContact(payload.FullName, payload.PhoneNumber)
	} else if payload.Type == "location" {
		builder.WithLocation(payload.Latitude, payload.Longitude, payload.Name, payload.Address)
	} else if payload.Type == "location-button" {
		builder.WithRequestLocationButton(payload.Message, payload.Footer, payload.Title)
	} else if payload.Type == "pix-button" {
		builder.WithCopyButton(payload.Message, payload.Footer, payload.Title, payload.CopyText, payload.PixKey)
	} else if payload.Type == "request-payment" {
		text := fmt.Sprintf("%v\nValue: %v", payload.Message, payload.Amount)
		builder.WithCopyButton(text, payload.Footer, payload.Title, "Copy PIX", payload.PixKey)
	} else if payload.Type == "list" {
		builder.WithList(payload.Message, payload.Footer, payload.Title, payload.ListButton, payload.Choices)
	} else if payload.Type == "carousel" {
		builder.WithText(payload.Message + "\n[Carousel Support Coming Soon]")
	} else if payload.CopyCode != "" {
		builder.WithCopyButton(payload.Message, payload.Footer, payload.Title, payload.CopyText, payload.CopyCode)
	} else if payload.Type == "button" && len(payload.Buttons) > 0 {
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
