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
	createdInstanceID, _, err := h.manager.NewClientWithName(req.Name)
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

type UazSendMessageRequest struct {
	Number string `json:"number"`
	Text   string `json:"text,omitempty"`
	// add other fields like caption, type, media etc
	Type     string `json:"type,omitempty"`
	Caption  string `json:"caption,omitempty"`
	File     string `json:"file,omitempty"`
	Filename string `json:"filename,omitempty"`
	Async    bool   `json:"async,omitempty"`
}

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
