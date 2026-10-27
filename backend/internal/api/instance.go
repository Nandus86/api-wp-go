package api

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skip2/go-qrcode"
	"github.com/user/whatsmeow-basileia/internal/infrastructure/whatsapp"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types"
)

func qrDataURL(code string) string {
	png, err := qrcode.Encode(code, qrcode.Medium, 512)
	if err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
}

// instanceState builds the status payload shared by /instance/status and /instance/connect.
func (h *Handler) instanceState(id string) map[string]interface{} {
	inst, client, ok := h.manager.InstanceStatus(id)
	if !ok {
		return nil
	}
	connected := client != nil && client.IsConnected()
	loggedIn := client != nil && client.IsLoggedIn()
	state := "disconnected"
	switch {
	case connected && loggedIn:
		state = "connected"
	case connected:
		state = "connecting"
	}
	out := map[string]interface{}{
		"instance": inst,
		"status": map[string]interface{}{
			"connected": connected,
			"loggedIn":  loggedIn,
			"jid":       inst.JID,
		},
		"state": state,
	}
	if p, ok := h.manager.Pairing(id); ok {
		if p.QRCode != "" {
			out["qrcode"] = qrDataURL(p.QRCode)
			out["qrcode_raw"] = p.QRCode
		}
		if p.PairCode != "" {
			out["paircode"] = p.PairCode
		}
		out["pairing_expires_at"] = p.ExpiresAt
	}
	return out
}

// UazInstanceStatus returns the connection state (and pending QR/pairing code).
// @Summary Instance status (UAZAPI)
// @Tags uazapi-instance
// @Produce json
// @Param token header string true "Instance Token"
// @Success 200 {object} map[string]interface{}
// @Router /instance/status [get]
func (h *Handler) UazInstanceStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, h.instanceState(id))
}

type connectInput struct {
	Phone string `json:"phone"`
}

// UazInstanceConnect connects a paired instance, or starts pairing: a QR code, or a
// pairing code when "phone" is given.
// @Summary Connect / pair instance (UAZAPI)
// @Tags uazapi-instance
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body connectInput false "phone for pairing-code login"
// @Success 200 {object} map[string]interface{}
// @Router /instance/connect [post]
func (h *Handler) UazInstanceConnect(w http.ResponseWriter, r *http.Request) {
	var in connectInput
	if r.ContentLength != 0 {
		_ = decodeJSON(r, &in)
	}
	id, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}
	h.connectInstance(w, r, id, in.Phone)
}

func (h *Handler) connectInstance(w http.ResponseWriter, r *http.Request, id, phone string) {
	client := h.manager.GetClient(id)
	if client == nil {
		writeError(w, http.StatusNotFound, "instance not found")
		return
	}
	if client.Store.ID != nil {
		// Already paired: just make sure it is connected.
		if err := h.manager.Connect(r.Context(), id); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		client.WaitForConnection(5 * time.Second)
		writeJSON(w, http.StatusOK, h.instanceState(id))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	if phone = strings.TrimSpace(phone); phone != "" {
		if _, err := h.manager.PairCode(ctx, id, phone); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	} else if _, err := h.manager.GetQROnce(ctx, id); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h.instanceState(id))
}

// UazInstanceDisconnect logs the instance out (a new QR code is needed to reconnect).
// @Summary Disconnect / log out (UAZAPI)
// @Tags uazapi-instance
// @Produce json
// @Param token header string true "Instance Token"
// @Success 200 {object} map[string]interface{}
// @Router /instance/disconnect [post]
func (h *Handler) UazInstanceDisconnect(w http.ResponseWriter, r *http.Request) {
	id, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}
	if err := h.manager.Logout(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h.instanceState(id))
}

// UazInstanceReset restarts the connection keeping the session.
// @Summary Reset connection (UAZAPI)
// @Tags uazapi-instance
// @Produce json
// @Param token header string true "Instance Token"
// @Success 200 {object} map[string]interface{}
// @Router /instance/reset [post]
func (h *Handler) UazInstanceReset(w http.ResponseWriter, r *http.Request) {
	id, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}
	client := h.manager.GetClient(id)
	if client == nil || client.Store.ID == nil {
		writeError(w, http.StatusConflict, "instance is not paired")
		return
	}
	client.Disconnect()
	if err := client.Connect(); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	client.WaitForConnection(10 * time.Second)
	writeJSON(w, http.StatusOK, h.instanceState(id))
}

// UazInstanceDelete deletes the instance of the token.
// @Summary Delete instance (UAZAPI)
// @Tags uazapi-instance
// @Produce json
// @Param token header string true "Instance Token"
// @Success 200 {object} map[string]interface{}
// @Router /instance [delete]
func (h *Handler) UazInstanceDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}
	if err := h.manager.DeleteInstance(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "deleted": id})
}

type nameInput struct {
	Name string `json:"name"`
}

// UazUpdateInstanceName renames the instance.
// @Summary Rename instance (UAZAPI)
// @Tags uazapi-instance
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body nameInput true "New name"
// @Success 200 {object} map[string]interface{}
// @Router /instance/updateInstanceName [post]
func (h *Handler) UazUpdateInstanceName(w http.ResponseWriter, r *http.Request) {
	var in nameInput
	if err := decodeJSON(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	id, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}
	if err := h.manager.RenameInstance(id, strings.TrimSpace(in.Name)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h.instanceState(id))
}

// UazInstanceCreate creates an instance (admin).
// @Summary Create instance (UAZAPI)
// @Tags uazapi-admin
// @Accept json
// @Produce json
// @Param admintoken header string true "Admin key"
// @Param request body nameInput true "Instance name"
// @Success 200 {object} map[string]interface{}
// @Router /instance/create [post]
func (h *Handler) UazInstanceCreate(w http.ResponseWriter, r *http.Request) {
	var in nameInput
	if err := decodeJSON(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	id, token, _, err := h.manager.NewClientWithName("", "", strings.TrimSpace(in.Name), "", "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := h.instanceState(id)
	out["token"] = token
	out["name"] = in.Name
	writeJSON(w, http.StatusOK, out)
}

// UazInstanceAll lists every instance (admin).
// @Summary List instances (UAZAPI)
// @Tags uazapi-admin
// @Produce json
// @Param admintoken header string true "Admin key"
// @Success 200 {array} whatsapp.Instance
// @Router /instance/all [get]
func (h *Handler) UazInstanceAll(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.manager.ListInstances())
}

// UazGetPrivacy reads the account privacy settings.
// @Summary Get privacy settings (UAZAPI)
// @Tags uazapi-instance
// @Produce json
// @Param token header string true "Instance Token"
// @Success 200 {object} map[string]interface{}
// @Router /instance/privacy [get]
func (h *Handler) UazGetPrivacy(w http.ResponseWriter, r *http.Request) {
	_, client, ok := h.clientFor(w, r, "")
	if !ok {
		return
	}
	settings, err := client.TryFetchPrivacySettings(r.Context(), true)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// UazSetPrivacy changes privacy settings (only the fields sent are changed).
// @Summary Set privacy settings (UAZAPI)
// @Description Fields: groupadd, last, status, profile, readreceipts, online, calladd. Values: all, contacts, contact_blacklist, none, match_last_seen, known.
// @Tags uazapi-instance
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body map[string]string true "Settings"
// @Success 200 {object} map[string]interface{}
// @Router /instance/privacy [post]
func (h *Handler) UazSetPrivacy(w http.ResponseWriter, r *http.Request) {
	var in map[string]string
	if err := decodeJSON(r, &in); err != nil || len(in) == 0 {
		writeError(w, http.StatusBadRequest, "send at least one privacy setting")
		return
	}
	_, client, ok := h.clientFor(w, r, "")
	if !ok {
		return
	}
	var settings types.PrivacySettings
	for name, value := range in {
		var err error
		settings, err = client.SetPrivacySetting(r.Context(), types.PrivacySettingType(name), types.PrivacySetting(value))
		if err != nil {
			writeError(w, http.StatusBadGateway, name+": "+err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, settings)
}

type presenceInput struct {
	Presence string `json:"presence"`
}

// UazInstancePresence sets the global presence (and keeps it as the "always online" setting).
// @Summary Set account presence (UAZAPI)
// @Tags uazapi-instance
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body presenceInput true "available or unavailable"
// @Success 200 {object} map[string]interface{}
// @Router /instance/presence [post]
func (h *Handler) UazInstancePresence(w http.ResponseWriter, r *http.Request) {
	var in presenceInput
	if err := decodeJSON(r, &in); err != nil || (in.Presence != "available" && in.Presence != "unavailable") {
		writeError(w, http.StatusBadRequest, "presence must be available or unavailable")
		return
	}
	id, client, ok := h.clientFor(w, r, "")
	if !ok {
		return
	}
	if err := client.SendPresence(r.Context(), types.Presence(in.Presence)); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	inst, _ := h.manager.GetInstance(id)
	settings := inst.Settings
	settings.AlwaysOnline = in.Presence == "available"
	_ = h.manager.UpdateSettings(id, settings)
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "presence": in.Presence})
}

type proxyInput struct {
	Mode     string `json:"mode"`
	ProxyURL string `json:"proxy_url"`
}

// UazGetProxy returns the instance proxy.
// @Summary Get proxy (UAZAPI)
// @Tags uazapi-instance
// @Produce json
// @Param token header string true "Instance Token"
// @Success 200 {object} map[string]interface{}
// @Router /instance/proxy [get]
func (h *Handler) UazGetProxy(w http.ResponseWriter, r *http.Request) {
	id, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}
	inst, _ := h.manager.GetInstance(id)
	mode := "none"
	if inst.ProxyURI != "" {
		mode = "custom"
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"mode": mode, "proxy_url": inst.ProxyURI})
}

// UazSetProxy sets (mode=custom) or removes (mode=none) the instance proxy.
// @Summary Set proxy (UAZAPI)
// @Tags uazapi-instance
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body proxyInput true "Proxy"
// @Success 200 {object} map[string]interface{}
// @Router /instance/proxy [post]
func (h *Handler) UazSetProxy(w http.ResponseWriter, r *http.Request) {
	var in proxyInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}
	proxy := strings.TrimSpace(in.ProxyURL)
	if strings.EqualFold(in.Mode, "none") {
		proxy = ""
	}
	if err := h.manager.UpdateInstanceProxy(id, proxy); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.UazGetProxy(w, r)
}

// UazProfileName changes the WhatsApp display name.
// @Summary Set profile name (UAZAPI)
// @Tags uazapi-profile
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body nameInput true "Name"
// @Success 200 {object} map[string]interface{}
// @Router /profile/name [post]
func (h *Handler) UazProfileName(w http.ResponseWriter, r *http.Request) {
	var in nameInput
	if err := decodeJSON(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	_, client, ok := h.clientFor(w, r, "")
	if !ok {
		return
	}
	if err := client.SendAppState(r.Context(), appstate.BuildSettingPushName(strings.TrimSpace(in.Name))); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "name": in.Name})
}

type imageInput struct {
	Image string `json:"image"`
}

// UazProfileImage changes (or with "remove" deletes) the profile picture. JPEG is required.
// @Summary Set profile picture (UAZAPI)
// @Tags uazapi-profile
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body imageInput true "Image URL, base64 or \"remove\""
// @Success 200 {object} map[string]interface{}
// @Router /profile/image [post]
func (h *Handler) UazProfileImage(w http.ResponseWriter, r *http.Request) {
	var in imageInput
	if err := decodeJSON(r, &in); err != nil || in.Image == "" {
		writeError(w, http.StatusBadRequest, "image is required")
		return
	}
	_, client, ok := h.clientFor(w, r, "")
	if !ok {
		return
	}
	var data []byte
	if !strings.EqualFold(in.Image, "remove") && !strings.EqualFold(in.Image, "delete") {
		var err error
		if data, err = h.loadImage(r.Context(), in.Image); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	pictureID, err := client.SetGroupPhoto(r.Context(), types.EmptyJID, data)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "picture_id": pictureID})
}

type rejectCallInput struct {
	Number string `json:"number"`
	ID     string `json:"id"`
}

// UazRejectCall rejects an incoming call (id comes from the calls.offer webhook).
// @Summary Reject call (UAZAPI)
// @Tags uazapi-calls
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body rejectCallInput true "Caller and call id"
// @Success 200 {object} map[string]interface{}
// @Router /call/reject [post]
func (h *Handler) UazRejectCall(w http.ResponseWriter, r *http.Request) {
	var in rejectCallInput
	if err := decodeJSON(r, &in); err != nil || in.Number == "" || in.ID == "" {
		writeError(w, http.StatusBadRequest, "number and id are required")
		return
	}
	_, client, ok := h.clientFor(w, r, "")
	if !ok {
		return
	}
	from, err := parseJID(in.Number)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := client.RejectCall(r.Context(), from, in.ID); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success"})
}

// ---- REST device extensions (/api/device/{id}/...) ----

type pairCodeInput struct {
	Phone string `json:"phone"`
}

// PairDevice links an instance with a phone-number pairing code instead of a QR code.
// @Summary Pairing code
// @Tags device
// @Accept json
// @Produce json
// @Param id path string true "Device ID"
// @Param request body pairCodeInput true "Phone in international format"
// @Success 200 {object} map[string]interface{}
// @Router /device/{id}/pair-code [post]
func (h *Handler) PairDevice(w http.ResponseWriter, r *http.Request) {
	var in pairCodeInput
	if err := decodeJSON(r, &in); err != nil || strings.TrimSpace(in.Phone) == "" {
		writeError(w, http.StatusBadRequest, "phone is required")
		return
	}
	id, ok := h.instanceFor(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	code, err := h.manager.PairCode(ctx, id, in.Phone)
	if errors.Is(err, whatsapp.ErrAlreadyPaired) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"device_id": id, "pair_code": code})
}

// LogoutDevice unlinks the WhatsApp session (the instance is kept, unpaired).
// @Summary Log out device
// @Tags device
// @Produce json
// @Param id path string true "Device ID"
// @Success 200 {object} map[string]interface{}
// @Router /device/{id}/logout [post]
func (h *Handler) LogoutDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := h.instanceFor(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if err := h.manager.Logout(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "device_id": id})
}

// DisconnectDevice closes the connection but keeps the session (reconnect without QR).
// @Summary Disconnect device
// @Tags device
// @Produce json
// @Param id path string true "Device ID"
// @Success 200 {object} map[string]interface{}
// @Router /device/{id}/disconnect [post]
func (h *Handler) DisconnectDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := h.instanceFor(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if err := h.manager.Disconnect(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "device_id": id})
}

// GetDeviceSettings returns behaviour settings of an instance.
// @Summary Get device settings
// @Tags device
// @Produce json
// @Param id path string true "Device ID"
// @Success 200 {object} whatsapp.InstanceSettings
// @Router /device/{id}/settings [get]
func (h *Handler) GetDeviceSettings(w http.ResponseWriter, r *http.Request) {
	id, ok := h.instanceFor(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	inst, _ := h.manager.GetInstance(id)
	writeJSON(w, http.StatusOK, inst.Settings)
}

// UpdateDeviceSettings replaces behaviour settings (reject calls, always online, read
// messages, ignore groups/status in webhooks, send delay range).
// @Summary Update device settings
// @Tags device
// @Accept json
// @Produce json
// @Param id path string true "Device ID"
// @Param request body whatsapp.InstanceSettings true "Settings"
// @Success 200 {object} whatsapp.InstanceSettings
// @Router /device/{id}/settings [put]
func (h *Handler) UpdateDeviceSettings(w http.ResponseWriter, r *http.Request) {
	id, ok := h.instanceFor(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	inst, _ := h.manager.GetInstance(id)
	settings := inst.Settings
	// Decode onto the current settings so omitted fields keep their value.
	if err := decodeJSON(r, &settings); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if settings.MsgDelayMin < 0 || settings.MsgDelayMax < 0 || settings.MsgDelayMax > 120000 {
		writeError(w, http.StatusBadRequest, "msg_delay_min/max must be between 0 and 120000 ms")
		return
	}
	if err := h.manager.UpdateSettings(id, settings); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, settings)
}
