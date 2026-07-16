package api

import (
	"encoding/json"
	"net/http"
	"strings"
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
	Async    bool   `json:"async,omitempty"`

	// Contact
	FullName     string `json:"fullName,omitempty"`
	PhoneNumber  string `json:"phoneNumber,omitempty"`

	// Location
	Address   string  `json:"address,omitempty"`
	Name      string  `json:"name,omitempty"`
	Latitude  float64 `json:"latitude,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`

	// Presence
	Presence string `json:"presence,omitempty"`

	// Menu / List
	ListButton string `json:"listButton,omitempty"`
    Choices    []string `json:"choices,omitempty"`

	// Interactive / Buttons / Poll
	Title     string   `json:"title,omitempty"`
	Footer    string   `json:"footer,omitempty"`
	Buttons   []string `json:"buttons,omitempty"`
	Question  string   `json:"question,omitempty"`
	Options   []string `json:"options,omitempty"`
	MaxAnswer int      `json:"maxAnswer,omitempty"`

	// payment / pix
	Amount  float64 `json:"amount,omitempty"`
	PixKey  string  `json:"pixKey,omitempty"`
	PixType string  `json:"pixType,omitempty"`
	PixName string  `json:"pixName,omitempty"`
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
	token := r.Header.Get("api_key")
	if token == "" {
		token = r.Header.Get("x-api-key")
	}
	if token == "" {
		token = r.Header.Get("token") // fallback for legacy
	}
	if token == "" {
		http.Error(w, "Unauthorized: missing api_key", http.StatusUnauthorized)
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
		if msgType == "" { msgType = "list" } // it could be "button" or "list" from payload
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
		"device_id":    token,
		"number":       req.Number,
		"text":         req.Text,
		"type":         msgType,
		"caption":      req.Caption,
		"file":         req.File,
		"filename":     req.Filename,
		"fullName":     req.FullName,
		"phoneNumber":  req.PhoneNumber,
		"address":      req.Address,
		"name":         req.Name,
		"latitude":     req.Latitude,
		"longitude":    req.Longitude,
		"presence":     req.Presence,
		"listButton":   req.ListButton,
		"choices":      req.Choices,
		"title":        req.Title,
		"footer":       req.Footer,
		"buttons":      req.Buttons,
		"question":     req.Question,
		"options":      req.Options,
		"maxAnswer":    req.MaxAnswer,
		"amount":       req.Amount,
		"pixKey":       req.PixKey,
		"pixType":      req.PixType,
		"pixName":      req.PixName,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, "Failed to encode message to queue", http.StatusInternalServerError)
		return
	}

	err = h.rmqClient.Publish(r.Context(), "send_message_queue", payloadBytes)
	if err != nil {
		http.Error(w, "Failed to enqueue message", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id": "queued",
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
	token := r.Header.Get("api_key")
	if token == "" {
		token = r.Header.Get("x-api-key")
	}
	if token == "" {
		token = r.Header.Get("token") // fallback for legacy
	}
	if token == "" {
		http.Error(w, "Unauthorized: missing api_key", http.StatusUnauthorized)
		return
	}

	var req UazSendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Map to internal format
	internalReq := SendMessageRequest{
		DeviceID: token,
		Phone:    req.Number,
		Text:     req.Text,
	}

	payloadBytes, err := json.Marshal(internalReq)
	if err != nil {
		http.Error(w, "Failed to encode message to queue", http.StatusInternalServerError)
		return
	}

	err = h.rmqClient.Publish(r.Context(), "send_message_queue", payloadBytes)
	if err != nil {
		http.Error(w, "Failed to enqueue message", http.StatusInternalServerError)
		return
	}

	// UazAPI success format
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id": "queued",
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
	token := r.Header.Get("api_key")
	if token == "" {
		token = r.Header.Get("x-api-key")
	}
	if token == "" {
		token = r.Header.Get("token") // fallback for legacy
	}
	if token == "" {
		http.Error(w, "Unauthorized: missing api_key", http.StatusUnauthorized)
		return
	}

	var req UazSendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Detect type
	mediaType := "image"
	if req.Type == "document" || req.Type == "video" || req.Type == "audio" {
		mediaType = req.Type
	}

	internalReq := SendMediaRequest{
		DeviceID: token,
		Phone:    req.Number,
		Type:     mediaType,
		MediaURL: req.File,
		Caption:  req.Caption,
		FileName: req.Filename,
	}

	payloadBytes, err := json.Marshal(internalReq)
	if err != nil {
		http.Error(w, "Failed to encode message to queue", http.StatusInternalServerError)
		return
	}

	err = h.rmqClient.Publish(r.Context(), "send_media_queue", payloadBytes)
	if err != nil {
		http.Error(w, "Failed to enqueue message", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id": "queued",
		"response": map[string]string{
			"status":  "success",
			"message": "Media sent successfully",
		},
	})
}

// DebugSendButton sends a hardcoded button message exactly as evolution-go does
// This bypasses the normal builder to isolate whether the issue is in the builder or the library
func (h *Handler) DebugSendButton(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("api_key")
	if token == "" {
		token = r.Header.Get("x-api-key")
	}
	if token == "" {
		http.Error(w, "Unauthorized: missing api_key", http.StatusUnauthorized)
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

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, "Failed to encode message to queue", http.StatusInternalServerError)
		return
	}

	err = h.rmqClient.Publish(r.Context(), "send_message_queue", payloadBytes)
	if err != nil {
		http.Error(w, "Failed to enqueue message", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Debug button message queued - check logs for detailed protobuf dump",
	})
}

// DebugSendList sends a hardcoded list message exactly as evolution-go does
// This bypasses the normal builder to isolate whether the issue is in the builder or the library
func (h *Handler) DebugSendList(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("api_key")
	if token == "" {
		token = r.Header.Get("x-api-key")
	}
	if token == "" {
		http.Error(w, "Unauthorized: missing api_key", http.StatusUnauthorized)
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

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, "Failed to encode message to queue", http.StatusInternalServerError)
		return
	}

	err = h.rmqClient.Publish(r.Context(), "send_message_queue", payloadBytes)
	if err != nil {
		http.Error(w, "Failed to enqueue message", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Debug list message queued - check logs for detailed protobuf dump",
	})
}
