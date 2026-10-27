package messaging

import (
	"context"
	"encoding/json"
	"fmt"

	"go.mau.fi/whatsmeow/types"
	"go.uber.org/zap"
)

type SendMessagePayload struct {
	DeviceID   string   `json:"device_id"`
	JobID      string   `json:"job_id,omitempty"`
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

	SendOptions
}

// SendOptions are the UAZAPI options shared by every send endpoint.
type SendOptions struct {
	// Delay in milliseconds shown as "typing..." before the message is sent.
	Delay int `json:"delay,omitempty"`
	// ReplyID quotes a previous message (id or "owner:id").
	ReplyID string `json:"replyid,omitempty"`
	// Mentions: comma separated numbers, or "all" for every group participant.
	Mentions    string `json:"mentions,omitempty"`
	TrackSource string `json:"track_source,omitempty"`
	TrackID     string `json:"track_id,omitempty"`
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

func (s *Service) handleSendMessage(body []byte) error {
	debugf("\n\n====== WORKER: Received message from RabbitMQ ======\n")
	debugf("Raw JSON: %s\n", string(body))

	var payload SendMessagePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		s.log.Error("Failed to unmarshal message payload", zap.Error(err))
		return nil
	}

	debugf("Parsed Payload: device=%s number=%s type=%s message=%q title=%q footer=%q listButton=%q buttons=%v choices=%v options=%v\n",
		payload.DeviceID, payload.Number, payload.Type, payload.Message, payload.Title, payload.Footer, payload.ListButton,
		payload.Buttons, payload.Choices, payload.Options)

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

	return s.runJob(payload.JobID, payload.DeviceID, func(ctx context.Context, sc *sendContext) sendOutcome {
		client := sc.client

		var remoteJID types.JID
		if payload.Type == "status" {
			remoteJID = types.NewJID("status", "broadcast")
		} else {
			jid, err := ResolveRecipient(ctx, client, payload.Number)
			if err != nil {
				return sendOutcome{err: err, permanent: isPermanent(err)}
			}
			remoteJID = jid
		}

		// Presence bypasses normal message sending if it's explicitly presence update
		if payload.Presence != "" {
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
			return sendOutcome{presenceOnly: true}
		}

		builder := NewMessageBuilder(client, remoteJID.String())

		debugf("\n====== WORKER: Building message ======\n")
		if payload.Type == "contact" {
			debugf("Builder method: WithContact\n")
			builder.WithContact(payload.FullName, payload.PhoneNumber)
		} else if payload.Type == "location" {
			debugf("Builder method: WithLocation\n")
			builder.WithLocation(payload.Latitude, payload.Longitude, payload.Name, payload.Address)
		} else if payload.Type == "location-button" {
			debugf("Builder method: WithRequestLocationButton\n")
			builder.WithRequestLocationButton(payload.Message, payload.Footer, payload.Title)
		} else if payload.Type == "pix-button" {
			debugf("Builder method: WithCopyButton (pix)\n")
			builder.WithCopyButton(payload.Message, payload.Footer, payload.Title, payload.CopyText, payload.PixKey)
		} else if payload.Type == "request-payment" {
			debugf("Builder method: WithCopyButton (payment)\n")
			text := fmt.Sprintf("%v\nValue: %v", payload.Message, payload.Amount)
			builder.WithCopyButton(text, payload.Footer, payload.Title, "Copy PIX", payload.PixKey)
		} else if payload.Type == "list" {
			debugf("Builder method: WithList (%d choices)\n", len(payload.Choices))
			builder.WithList(payload.Message, payload.Footer, payload.Title, payload.ListButton, payload.Choices)
		} else if payload.Type == "poll" {
			debugf("Builder method: WithPoll\n")
			builder.WithPoll(payload.Question, payload.Options, payload.MaxAnswer)
		} else if payload.Type == "carousel" {
			debugf("Builder method: WithText (carousel placeholder)\n")
			builder.WithText(payload.Message + "\n[Carousel Support Coming Soon]")
		} else if payload.CopyCode != "" {
			debugf("Builder method: WithCopyButton (copy)\n")
			builder.WithCopyButton(payload.Message, payload.Footer, payload.Title, payload.CopyText, payload.CopyCode)
		} else if payload.Type == "button" && len(payload.Buttons) > 0 {
			debugf("Builder method: WithButtons (%d buttons)\n", len(payload.Buttons))
			builder.WithButtons(payload.Message, payload.Footer, payload.Title, payload.Buttons)
		} else {
			debugf("Builder method: WithText (default)\n")
			builder.WithText(payload.Message)
		}

		return s.deliver(ctx, sc, builder, payload.SendOptions, len(payload.Message))
	})
}
