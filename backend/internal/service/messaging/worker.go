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

	// Poll
	Question  string   `json:"question,omitempty"`
	Options   []string `json:"options,omitempty"`
	MaxAnswer int      `json:"maxAnswer,omitempty"`

	// payment / pix
	Amount         float64 `json:"amount,omitempty"`
	PixKey         string  `json:"pixKey,omitempty"`
	PixType        string  `json:"pixType,omitempty"`
	PixName        string  `json:"pixName,omitempty"`
	ItemName       string  `json:"itemName,omitempty"`
	InvoiceNumber  string  `json:"invoiceNumber,omitempty"`
	PaymentLink    string  `json:"paymentLink,omitempty"`
	FileUrl        string  `json:"fileUrl,omitempty"`
	FileName       string  `json:"fileName,omitempty"`
	BoletoCode     string  `json:"boletoCode,omitempty"`
	AdditionalNote string  `json:"additionalNote,omitempty"`

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
	fmt.Printf("\n\n====== WORKER: Received message from RabbitMQ ======\n")
	fmt.Printf("Raw JSON: %s\n", string(body))
	
	var payload SendMessagePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		w.logger.Error("Failed to unmarshal message payload", zap.Error(err))
		fmt.Printf("ERROR: Failed to unmarshal: %v\n", err)
		return nil
	}

	fmt.Printf("Parsed Payload:\n")
	fmt.Printf("  DeviceID: %s\n", payload.DeviceID)
	fmt.Printf("  Number: %s\n", payload.Number)
	fmt.Printf("  Type: %s\n", payload.Type)
	fmt.Printf("  Message: %s\n", payload.Message)
	fmt.Printf("  Title: %s\n", payload.Title)
	fmt.Printf("  Footer: %s\n", payload.Footer)
	fmt.Printf("  ListButton: %s\n", payload.ListButton)
	fmt.Printf("  Buttons: %v\n", payload.Buttons)
	fmt.Printf("  Choices: %v\n", payload.Choices)
	fmt.Printf("  Options: %v\n", payload.Options)
	fmt.Printf("  CopyCode: %s\n", payload.CopyCode)
	fmt.Printf("  Question: %s\n", payload.Question)
	fmt.Printf("  MaxAnswer: %d\n", payload.MaxAnswer)
	fmt.Printf("==========================================\n\n")

	// Resolve aliases (Basileapi compatibility)
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
	if len(payload.Options) == 0 && len(payload.Choices) > 0 {
		payload.Options = payload.Choices
	}

	client := w.manager.GetClient(payload.DeviceID)
	if client == nil {
		w.logger.Warn("Device not found for message sending", zap.String("deviceID", payload.DeviceID))
		return nil
	}

	if !client.IsConnected() {
		w.logger.Warn("Device not connected", zap.String("deviceID", payload.DeviceID))
		return nil
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

	fmt.Printf("\n====== WORKER: Building message ======\n")
	if payload.Type == "contact" {
		fmt.Printf("Builder method: WithContact\n")
		builder.WithContact(payload.FullName, payload.PhoneNumber)
	} else if payload.Type == "location" {
		fmt.Printf("Builder method: WithLocation\n")
		builder.WithLocation(payload.Latitude, payload.Longitude, payload.Name, payload.Address)
	} else if payload.Type == "location-button" {
		fmt.Printf("Builder method: WithRequestLocationButton\n")
		builder.WithRequestLocationButton(payload.Message, payload.Footer, payload.Title)
	} else if payload.Type == "pix-button" {
		fmt.Printf("Builder method: WithCopyButton (pix)\n")
		builder.WithCopyButton(payload.Message, payload.Footer, payload.Title, payload.CopyText, payload.PixKey)
	} else if payload.Type == "request-payment" {
		fmt.Printf("Builder method: WithCopyButton (payment)\n")
		text := fmt.Sprintf("%v\nValue: %v", payload.Message, payload.Amount)
		builder.WithCopyButton(text, payload.Footer, payload.Title, "Copy PIX", payload.PixKey)
	} else if payload.Type == "list" {
		fmt.Printf("Builder method: WithList\n")
		fmt.Printf("  Choices count: %d\n", len(payload.Choices))
		builder.WithList(payload.Message, payload.Footer, payload.Title, payload.ListButton, payload.Choices)
	} else if payload.Type == "poll" {
		fmt.Printf("Builder method: WithPoll\n")
		builder.WithPoll(payload.Question, payload.Options, payload.MaxAnswer)
	} else if payload.Type == "carousel" {
		fmt.Printf("Builder method: WithText (carousel placeholder)\n")
		builder.WithText(payload.Message + "\n[Carousel Support Coming Soon]")
	} else if payload.CopyCode != "" {
		fmt.Printf("Builder method: WithCopyButton (copy)\n")
		builder.WithCopyButton(payload.Message, payload.Footer, payload.Title, payload.CopyText, payload.CopyCode)
	} else if payload.Type == "button" && len(payload.Buttons) > 0 {
		fmt.Printf("Builder method: WithButtons\n")
		fmt.Printf("  Buttons count: %d\n", len(payload.Buttons))
		builder.WithButtons(payload.Message, payload.Footer, payload.Title, payload.Buttons)
	} else {
		fmt.Printf("Builder method: WithText (default)\n")
		builder.WithText(payload.Message)
	}
	fmt.Printf("==========================================\n\n")

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
