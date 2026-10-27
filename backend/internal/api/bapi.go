package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/user/whatsmeow-basileia/internal/infrastructure/rabbitmq"
	"github.com/user/whatsmeow-basileia/internal/service/messaging"
)

// UazSendMessageRequest represents the payload for UazAPI message endpoints
type UazSendMessageRequest struct {
	Number string `json:"number"`
	Text   string `json:"text,omitempty"`
	// media fields
	Type     string `json:"type,omitempty"`
	Caption  string `json:"caption,omitempty"`
	File     string `json:"file,omitempty"`
	Filename string `json:"filename,omitempty"`
	DocName  string `json:"docName,omitempty"`
	MimeType string `json:"mimetype,omitempty"`
	Async    bool   `json:"async,omitempty"`

	// Contact
	FullName    string `json:"fullName,omitempty"`
	PhoneNumber string `json:"phoneNumber,omitempty"`

	// Location
	Address   string  `json:"address,omitempty"`
	Name      string  `json:"name,omitempty"`
	Latitude  float64 `json:"latitude,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`

	// Presence
	Presence string `json:"presence,omitempty"`

	// Menu / List
	ListButton string   `json:"listButton,omitempty"`
	Choices    []string `json:"choices,omitempty"`

	// Interactive / Buttons / Poll
	Title      string   `json:"title,omitempty"`
	Footer     string   `json:"footer,omitempty"`
	FooterText string   `json:"footerText,omitempty"`
	Buttons    []string `json:"buttons,omitempty"`
	Question   string   `json:"question,omitempty"`
	Options    []string `json:"options,omitempty"`
	MaxAnswer  int      `json:"maxAnswer,omitempty"`

	// payment / pix
	Amount  float64 `json:"amount,omitempty"`
	PixKey  string  `json:"pixKey,omitempty"`
	PixType string  `json:"pixType,omitempty"`
	PixName string  `json:"pixName,omitempty"`

	// delay, replyid, mentions, track_source, track_id
	messaging.SendOptions
}

func trackOpts(o messaging.SendOptions) messaging.EnqueueOptions {
	return messaging.EnqueueOptions{TrackSource: o.TrackSource, TrackID: o.TrackID}
}

// SendUazGeneric handles all other UazAPI message types
// @Summary Send UazAPI Message (Various Types)
// @Description Sends different types of messages (contact, location, status, menu, carousel, payment, pix, presence) via UazAPI format.
// @Description To send presence, use `/message/presence` with `{ "number": "...", "presence": "composing" }`.
// @Description For menus/lists, use `/send/menu` with `{ "number": "...", "listButton": "Ver", "choices": ["[Geral]", "Opção 1|op1"] }`.
// @Tags uazapi-messages
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body UazSendMessageRequest true "Message Payload"
// @Success 200 {object} map[string]interface{} "Message queued successfully"
// @Failure 401 {string} string "Unauthorized: missing token"
// @Failure 400 {string} string "Invalid request body"
// @Failure 500 {string} string "Failed to enqueue message"
// @Router /send/contact [post]
// @Router /send/location [post]
// @Router /message/presence [post]
// @Router /send/status [post]
// @Router /send/menu [post]
// @Router /send/carousel [post]
// @Router /send/location-button [post]
// @Router /send/request-payment [post]
// @Router /send/pix-button [post]
func (h *Handler) SendUazGeneric(w http.ResponseWriter, r *http.Request) {
	token, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}

	var req UazSendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Use specific type if empty based on route later or let the worker determine
	msgType := req.Type

	switch {
	case strings.HasSuffix(r.URL.Path, "/send/contact"):
		msgType = "contact"
	case strings.HasSuffix(r.URL.Path, "/send/location"):
		msgType = "location"
	case strings.HasSuffix(r.URL.Path, "/message/presence"):
		msgType = "presence"
	case strings.HasSuffix(r.URL.Path, "/send/status"):
		msgType = "status"
	case strings.HasSuffix(r.URL.Path, "/send/menu"):
		if msgType == "" {
			msgType = "list"
		} // it could be "button" or "list" from payload
	case strings.HasSuffix(r.URL.Path, "/send/poll"):
		msgType = "poll"
	case strings.HasSuffix(r.URL.Path, "/send/button"):
		msgType = "button"
	case strings.HasSuffix(r.URL.Path, "/send/carousel"):
		msgType = "carousel"
	case strings.HasSuffix(r.URL.Path, "/send/location-button"):
		msgType = "location-button"
	case strings.HasSuffix(r.URL.Path, "/send/request-payment"):
		msgType = "request-payment"
	case strings.HasSuffix(r.URL.Path, "/send/pix-button"):
		msgType = "pix-button"
	}

	// Create a generic payload mapping all fields
	payload := map[string]interface{}{
		"device_id":   token,
		"number":      req.Number,
		"text":        req.Text,
		"type":        msgType,
		"caption":     req.Caption,
		"file":        req.File,
		"filename":    req.Filename,
		"fullName":    req.FullName,
		"phoneNumber": req.PhoneNumber,
		"address":     req.Address,
		"name":        req.Name,
		"latitude":    req.Latitude,
		"longitude":   req.Longitude,
		"presence":    req.Presence,
		"listButton":  req.ListButton,
		"choices":     req.Choices,
		"title":       req.Title,
		"footer":      req.Footer,
		"footerText":  req.FooterText,
		"buttons":     req.Buttons,
		"question":    req.Question,
		"options":     req.Options,
		"maxAnswer":   req.MaxAnswer,
		"amount":      req.Amount,
		"pixKey":      req.PixKey,
		"pixType":     req.PixType,
		"pixName":     req.PixName,
		"delay":       req.Delay,
		"replyid":     req.ReplyID,
		"mentions":    req.Mentions,
	}

	jobID, err := h.messaging.Enqueue(r.Context(), token, rabbitmq.QueueSendMessage, payload, trackOpts(req.SendOptions))
	if err != nil {
		http.Error(w, "Failed to enqueue message", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":     "queued",
		"job_id": jobID,
		"response": map[string]string{
			"status":  "success",
			"message": "Message sent successfully",
		},
	})
}

// SendUazText sends a text message via UazAPI format
// @Summary Send UazAPI Text Message
// @Description Sends a simple text message in the UazAPI format.
// @Tags uazapi-messages
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body UazSendMessageRequest true "Text Message Payload"
// @Success 200 {object} map[string]interface{} "Message queued successfully"
// @Failure 401 {string} string "Unauthorized: missing token"
// @Failure 400 {string} string "Invalid request body"
// @Failure 500 {string} string "Failed to enqueue message"
// @Router /send/text [post]
func (h *Handler) SendUazText(w http.ResponseWriter, r *http.Request) {
	token, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}

	var req UazSendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Map to internal format
	internalReq := SendMessageRequest{
		DeviceID:    token,
		Phone:       req.Number,
		Text:        req.Text,
		SendOptions: req.SendOptions,
	}

	jobID, err := h.messaging.Enqueue(r.Context(), token, rabbitmq.QueueSendMessage, internalReq, trackOpts(req.SendOptions))
	if err != nil {
		http.Error(w, "Failed to enqueue message", http.StatusInternalServerError)
		return
	}

	// UazAPI success format
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":     "queued",
		"job_id": jobID,
		"response": map[string]string{
			"status":  "success",
			"message": "Message sent successfully",
		},
	})
}

// SendUazMedia sends a media message via UazAPI format
// @Summary Send UazAPI Media Message
// @Description Sends a media message (image, video, audio, document) in the UazAPI format.
// @Tags uazapi-messages
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body UazSendMessageRequest true "Media Message Payload"
// @Success 200 {object} map[string]interface{} "Media queued successfully"
// @Failure 401 {string} string "Unauthorized: missing token"
// @Failure 400 {string} string "Invalid request body"
// @Failure 500 {string} string "Failed to enqueue message"
// @Router /send/media [post]
func (h *Handler) SendUazMedia(w http.ResponseWriter, r *http.Request) {
	token, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}

	var req UazSendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Detect type
	mediaType := "image"
	switch req.Type {
	case "document", "video", "audio":
		mediaType = req.Type
	case "ptt", "myaudio":
		mediaType = "audio"
	}

	// UAZAPI sends the caption in "text" and the file name in "docName".
	caption := req.Caption
	if caption == "" {
		caption = req.Text
	}
	fileName := req.Filename
	if fileName == "" {
		fileName = req.DocName
	}

	internalReq := SendMediaRequest{
		DeviceID:    token,
		Phone:       req.Number,
		Type:        mediaType,
		Caption:     caption,
		FileName:    fileName,
		MimeType:    req.MimeType,
		SendOptions: req.SendOptions,
	}
	// "file" may be a URL, an uploaded media reference or base64 data.
	if strings.HasPrefix(req.File, "data:") || (!strings.Contains(req.File, "://") && !strings.HasPrefix(req.File, messaging.MediaRefPrefix) && len(req.File) > 200) {
		internalReq.Base64 = req.File
	} else {
		internalReq.MediaURL = req.File
	}

	jobID, err := h.messaging.Enqueue(r.Context(), token, rabbitmq.QueueSendMedia, internalReq, trackOpts(req.SendOptions))
	if err != nil {
		http.Error(w, "Failed to enqueue message", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":     "queued",
		"job_id": jobID,
		"response": map[string]string{
			"status":  "success",
			"message": "Media sent successfully",
		},
	})
}

// DebugSendButton sends a hardcoded button message exactly as evolution-go does
// This bypasses the normal builder to isolate whether the issue is in the builder or the library
func (h *Handler) DebugSendButton(w http.ResponseWriter, r *http.Request) {
	token, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}

	// Hardcoded payload matching evolution-go's SendButton exactly
	payload := map[string]interface{}{
		"device_id": token,
		"number":    "554399284670", // Test number
		"type":      "button",
		"message":   "Test button message",
		"title":     "Test Title",
		"footer":    "Test Footer",
		"buttons":   []string{"Option 1|id_1", "Option 2|id_2"},
	}

	jobID, err := h.messaging.Enqueue(r.Context(), token, rabbitmq.QueueSendMessage, payload, messaging.EnqueueOptions{})
	if err != nil {
		http.Error(w, "Failed to enqueue message", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"job_id":  jobID,
		"message": "Debug button message queued - check logs for detailed protobuf dump",
	})
}

// DebugSendList sends a hardcoded list message exactly as evolution-go does
// This bypasses the normal builder to isolate whether the issue is in the builder or the library
func (h *Handler) DebugSendList(w http.ResponseWriter, r *http.Request) {
	token, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}

	// Hardcoded payload matching evolution-go's SendList exactly
	payload := map[string]interface{}{
		"device_id":  token,
		"number":     "554399284670", // Test number
		"type":       "list",
		"message":    "Test list message",
		"title":      "Test List Title",
		"footer":     "Test Footer",
		"listButton": "View Options",
		"choices":    []string{"[Section 1]", "Option 1|id_1|Description 1", "Option 2|id_2|Description 2"},
	}

	jobID, err := h.messaging.Enqueue(r.Context(), token, rabbitmq.QueueSendMessage, payload, messaging.EnqueueOptions{})
	if err != nil {
		http.Error(w, "Failed to enqueue message", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"job_id":  jobID,
		"message": "Debug list message queued - check logs for detailed protobuf dump",
	})
}
