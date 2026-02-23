package ports

import (
	"context"
	"io"

	"github.com/user/whatsmeow-basileia/internal/core/domain"
	"go.mau.fi/whatsmeow"
)

// WhatsAppService defines the contract for interacting with WhatsApp
type WhatsAppService interface {
	Connect(ctx context.Context, deviceID string) error
	Disconnect(ctx context.Context, deviceID string) error
	GetStatus(deviceID string) (domain.DeviceStatus, error)
	GetQRChannel(deviceID string) (<-chan string, error)
	SendMessage(ctx context.Context, deviceID string, msg domain.Message) error
}

// EventHandlerRegistry defines how we register handlers for events
type EventHandlerRegistry interface {
	Register(eventType interface{}, handler func(evt interface{}))
	Dispatch(evt interface{})
}

// MediaUploader defines how to handle media uploads
type MediaUploader interface {
	Upload(ctx context.Context, data io.Reader, mimeType string) (whatsmeow.UploadResponse, error)
}
