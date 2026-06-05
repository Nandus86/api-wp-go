package api

import (
	"encoding/json"
	"net/http"
	"time"
)

type UazInitRequest struct {
	Name               string `json:"name"`
	SystemName         string `json:"systemName"`
	AdminField01       string `json:"adminField01"`
	AdminField02       string `json:"adminField02"`
	FingerprintProfile string `json:"fingerprintProfile"`
	Browser            string `json:"browser"`
}

type UazInitResponse struct {
	Response string      `json:"response"`
	Instance UazInstance `json:"instance"`
	Token    string      `json:"token"`
}

type UazInstance struct {
	ID         string `json:"id"`
	Token      string `json:"token"`
	Status     string `json:"status"`
	Name       string `json:"name"`
	SystemName string `json:"systemName"`
	Created    string `json:"created"`
}

// InitInstance creates a new UazAPI-compatible instance
// @Summary Create UazAPI Instance
// @Description Creates a new WhatsApp instance compatible with UazAPI guidelines. Returns a token to be used in the header for subsequent requests.
// @Tags uazapi-instance
// @Accept json
// @Produce json
// @Param request body UazInitRequest true "Instance Configuration"
// @Success 200 {object} UazInitResponse
// @Failure 400 {string} string "Invalid request body or missing name"
// @Failure 500 {string} string "Failed to create instance"
// @Router /instance/init [post]
func (h *Handler) InitInstance(w http.ResponseWriter, r *http.Request) {
	// Optional: Check admintoken header
	// adminToken := r.Header.Get("admintoken")

	var req UazInitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "Name is required", http.StatusBadRequest)
		return
	}

	// For compatibility mapping token to our instance ID
	createdInstanceID, _, _, err := h.manager.NewClientWithName("", "", req.Name, "", "")
	if err != nil {
		http.Error(w, "Failed to create instance", http.StatusInternalServerError)
		return
	}

	res := UazInitResponse{
		Response: "Instance created successfully",
		Token:    createdInstanceID, // We use our ID as their token
		Instance: UazInstance{
			ID:         createdInstanceID,
			Token:      createdInstanceID,
			Status:     "disconnected",
			Name:       req.Name,
			SystemName: req.SystemName,
			Created:    time.Now().Format(time.RFC3339),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// ConnectInstance connects an existing UazAPI instance to WhatsApp
// @Summary Connect UazAPI Instance
// @Description Connects the instance to WhatsApp and returns a base64 QR Code if not logged in.
// @Tags uazapi-instance
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Success 200 {object} map[string]interface{} "Connection status or QR code"
// @Failure 401 {string} string "Unauthorized: missing token"
// @Failure 404 {string} string "Device not found or not loaded"
// @Failure 500 {string} string "Failed to connect"
// @Failure 504 {string} string "Timeout waiting for QR"
// @Router /instance/connect [post]
func (h *Handler) ConnectInstance(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("token")
	if token == "" {
		http.Error(w, "Unauthorized: missing token", http.StatusUnauthorized)
		return
	}

	client := h.manager.GetClient(token)
	if client == nil {
		http.Error(w, "Device not found or not loaded", http.StatusNotFound)
		return
	}

	if client.IsConnected() {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connected": true,
			"loggedIn":  true,
		})
		return
	}

	// Reconnect and fetch QR using GetQR which connects automatically
	qrChan, err := h.manager.GetQR(r.Context(), token)
	if err != nil {
		http.Error(w, "Failed to connect", http.StatusInternalServerError)
		return
	}

	// Wait for the first QR code to match UazAPI synchronous standard
	select {
	case code := <-qrChan:
		// Uazapi returns base64 image here. 
		// For simplicity, we just return the raw string to be encoded in frontend 
		// or they can generate QR from it.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connected": false,
			"loggedIn":  false,
			"instance": map[string]string{
				"qrcode": code, 
			},
		})
		return
	case <-time.After(15 * time.Second):
		http.Error(w, "Timeout waiting for QR", http.StatusGatewayTimeout)
		return
	}
}

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
	token := r.Header.Get("token")
	if token == "" {
		http.Error(w, "Unauthorized: missing token", http.StatusUnauthorized)
		return
	}

	var req UazSendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Use specific type if empty based on route later or let the worker determine
	msgType := req.Type
	
	switch r.URL.Path {
	case "/send/contact":
		msgType = "contact"
	case "/send/location":
		msgType = "location"
	case "/message/presence":
		msgType = "presence" 
	case "/send/status":
		msgType = "status"
	case "/send/menu":
		if msgType == "" { msgType = "list" } // it could be "button" or "list" from payload
	case "/send/carousel":
		msgType = "carousel"
	case "/send/location-button":
		msgType = "location-button"
	case "/send/request-payment":
		msgType = "request-payment"
	case "/send/pix-button":
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
	token := r.Header.Get("token")
	if token == "" {
		http.Error(w, "Unauthorized: missing token", http.StatusUnauthorized)
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
	token := r.Header.Get("token")
	if token == "" {
		http.Error(w, "Unauthorized: missing token", http.StatusUnauthorized)
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
