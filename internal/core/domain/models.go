package domain

import (
	"time"
)

type MessageType string

const (
	TextMessage  MessageType = "text"
	ImageMessage MessageType = "image"
	VideoMessage MessageType = "video"
	AudioMessage MessageType = "audio"
)

type Message struct {
	ID        string
	JID       string
	Type      MessageType
	Content   string
	Params    map[string]interface{}
	CreatedAt time.Time
}

type DeviceStatus string

const (
	DeviceStatusConnected    DeviceStatus = "connected"
	DeviceStatusDisconnected DeviceStatus = "disconnected"
	DeviceStatusPairing      DeviceStatus = "pairing"
)

type Device struct {
	ID           string
	Name         string
	PhoneNumber  string
	Status       DeviceStatus
	LastActivity time.Time
}
