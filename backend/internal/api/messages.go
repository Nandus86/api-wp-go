package api

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/user/whatsmeow-basileia/internal/auth"
	"github.com/user/whatsmeow-basileia/internal/infrastructure/whatsapp"
	"github.com/user/whatsmeow-basileia/internal/service/messages"
	"github.com/user/whatsmeow-basileia/internal/service/messaging"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// clientFor resolves the instance of a request and returns its connected client.
func (h *Handler) clientFor(w http.ResponseWriter, r *http.Request, requested string) (string, *whatsmeow.Client, bool) {
	id, ok := h.instanceFor(w, r, requested)
	if !ok {
		return "", nil, false
	}
	client := h.manager.GetClient(id)
	if client == nil {
		writeError(w, http.StatusNotFound, "instance not found")
		return "", nil, false
	}
	if !client.IsConnected() || !client.IsLoggedIn() {
		writeError(w, http.StatusConflict, "instance is not connected")
		return "", nil, false
	}
	return id, client, true
}

// GetJob returns the state of a queued send.
// @Summary Send job status
// @Description Every queued send returns a job_id; poll it to know whether it was sent, is retrying or failed.
// @Tags message
// @Produce json
// @Param id path string true "Job ID"
// @Success 200 {object} messaging.Job
// @Router /message/job/{id} [get]
func (h *Handler) GetJob(w http.ResponseWriter, r *http.Request) {
	job, err := h.messaging.Jobs().Get(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, messaging.ErrJobNotFound) {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !auth.FromContext(r.Context()).CanAccessInstance(job.InstanceID) {
		writeError(w, http.StatusForbidden, "API key not authorized for this instance")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// ListMessages returns the stored message history of an instance.
// @Summary Message history
// @Tags message
// @Produce json
// @Param id path string true "Device ID"
// @Param chat query string false "Chat JID or phone number"
// @Param before query string false "RFC3339 timestamp (pagination)"
// @Param limit query int false "Max rows (default 50, max 500)"
// @Param offset query int false "Offset"
// @Success 200 {object} map[string]interface{}
// @Router /device/{id}/messages [get]
func (h *Handler) ListMessages(w http.ResponseWriter, r *http.Request) {
	id, ok := h.instanceFor(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	q := r.URL.Query()
	f := messages.Filter{}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	f.Offset, _ = strconv.Atoi(q.Get("offset"))
	if chat := q.Get("chat"); chat != "" {
		jid, err := messaging.ParseJIDOrNumber(chat)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		f.ChatJID = jid.String()
	}
	if b := q.Get("before"); b != "" {
		t, err := time.Parse(time.RFC3339, b)
		if err != nil {
			writeError(w, http.StatusBadRequest, "before must be RFC3339")
			return
		}
		f.Before = &t
	}
	list, total, err := h.history.Find(r.Context(), id, f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"total": total, "items": list})
}

type findInput struct {
	ID          string `json:"id"`
	ChatID      string `json:"chatid"`
	TrackSource string `json:"track_source"`
	TrackID     string `json:"track_id"`
	Limit       int    `json:"limit"`
	Offset      int    `json:"offset"`
}

// UazFindMessages searches the message history (UAZAPI /message/find).
// @Summary Find messages (UAZAPI)
// @Tags uazapi-messages
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body findInput true "Filters"
// @Success 200 {object} map[string]interface{}
// @Router /message/find [post]
func (h *Handler) UazFindMessages(w http.ResponseWriter, r *http.Request) {
	id, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}
	var in findInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	f := messages.Filter{ID: in.ID, Limit: in.Limit, Offset: in.Offset}
	if in.ChatID != "" {
		jid, err := messaging.ParseJIDOrNumber(in.ChatID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		f.ChatJID = jid.String()
	}
	if in.TrackID != "" || in.TrackSource != "" {
		// Tracking ids live on the send job; resolve them to message ids.
		jobs, _, err := h.messaging.Jobs().List(r.Context(), messaging.JobFilter{InstanceID: id, Limit: 1000})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out := []messages.Message{}
		for _, j := range jobs {
			if (in.TrackID == "" || j.TrackID == in.TrackID) && (in.TrackSource == "" || j.TrackSource == in.TrackSource) && j.WAMessageID != "" {
				if m, err := h.history.Get(r.Context(), id, j.WAMessageID); err == nil {
					out = append(out, *m)
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"messages": out, "total": len(out)})
		return
	}
	list, total, err := h.history.Find(r.Context(), id, f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"messages": list, "total": total})
}

// target locates a message for an action: from the history, or from the number given.
func (h *Handler) target(ctx context.Context, instanceID string, client *whatsmeow.Client, id, number string) (chat, sender types.JID, fromMe bool, stored *messages.Message, err error) {
	id = messages.NormalizeID(id)
	if m, gerr := h.history.Get(ctx, instanceID, id); gerr == nil {
		chat, _ = types.ParseJID(m.ChatJID)
		if m.SenderJID != "" {
			sender, _ = types.ParseJID(m.SenderJID)
		}
		if m.FromMe && client.Store.ID != nil {
			sender = client.Store.ID.ToNonAD()
		}
		return chat, sender, m.FromMe, m, nil
	}
	if number == "" {
		return types.EmptyJID, types.EmptyJID, false, nil, errors.New("message not found in history: send the chat number too")
	}
	chat, err = messaging.ResolveRecipient(ctx, client, number)
	if err != nil {
		return types.EmptyJID, types.EmptyJID, false, nil, err
	}
	// Unknown message: assume it was sent by the other side of a direct chat.
	return chat, chat, false, nil, nil
}

type reactInput struct {
	Number string `json:"number"`
	Text   string `json:"text"`
	ID     string `json:"id"`
}

// UazReact reacts to a message with an emoji (empty text removes the reaction).
// @Summary React to a message (UAZAPI)
// @Tags uazapi-messages
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body reactInput true "Reaction"
// @Success 200 {object} map[string]interface{}
// @Router /message/react [post]
func (h *Handler) UazReact(w http.ResponseWriter, r *http.Request) {
	var in reactInput
	if err := decodeJSON(r, &in); err != nil || in.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	id, client, ok := h.clientFor(w, r, "")
	if !ok {
		return
	}
	chat, sender, _, _, err := h.target(r.Context(), id, client, in.ID, in.Number)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	resp, err := client.SendMessage(r.Context(), chat, client.BuildReaction(chat, sender, messages.NormalizeID(in.ID), in.Text))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "id": resp.ID, "timestamp": resp.Timestamp.Unix()})
}

type idInput struct {
	ID     string `json:"id"`
	Number string `json:"number"`
	Text   string `json:"text"`
}

// UazDeleteMessage revokes a message for everyone.
// @Summary Delete a message for everyone (UAZAPI)
// @Tags uazapi-messages
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body idInput true "Message id (number optional)"
// @Success 200 {object} map[string]interface{}
// @Router /message/delete [post]
func (h *Handler) UazDeleteMessage(w http.ResponseWriter, r *http.Request) {
	var in idInput
	if err := decodeJSON(r, &in); err != nil || in.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	id, client, ok := h.clientFor(w, r, "")
	if !ok {
		return
	}
	chat, sender, fromMe, _, err := h.target(r.Context(), id, client, in.ID, in.Number)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	msgID := messages.NormalizeID(in.ID)
	if fromMe {
		sender = types.EmptyJID
	}
	resp, err := client.SendMessage(r.Context(), chat, client.BuildRevoke(chat, sender, msgID))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = h.history.MarkDeleted(r.Context(), id, msgID)
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "id": resp.ID})
}

// UazEditMessage edits the text of an own message.
// @Summary Edit a sent message (UAZAPI)
// @Tags uazapi-messages
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body idInput true "Message id and new text"
// @Success 200 {object} map[string]interface{}
// @Router /message/edit [post]
func (h *Handler) UazEditMessage(w http.ResponseWriter, r *http.Request) {
	var in idInput
	if err := decodeJSON(r, &in); err != nil || in.ID == "" || in.Text == "" {
		writeError(w, http.StatusBadRequest, "id and text are required")
		return
	}
	id, client, ok := h.clientFor(w, r, "")
	if !ok {
		return
	}
	chat, _, fromMe, stored, err := h.target(r.Context(), id, client, in.ID, in.Number)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if stored != nil && !fromMe {
		writeError(w, http.StatusBadRequest, "only messages sent by this instance can be edited")
		return
	}
	msgID := messages.NormalizeID(in.ID)
	resp, err := client.SendMessage(r.Context(), chat, client.BuildEdit(chat, msgID, &waE2E.Message{Conversation: proto.String(in.Text)}))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = h.history.MarkEdited(r.Context(), id, msgID, in.Text)
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "id": resp.ID})
}

type markReadInput struct {
	ID []string `json:"id"`
}

// UazMarkRead marks messages as read (sends blue ticks).
// @Summary Mark messages as read (UAZAPI)
// @Tags uazapi-messages
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body markReadInput true "Message ids"
// @Success 200 {object} map[string]interface{}
// @Router /message/markread [post]
func (h *Handler) UazMarkRead(w http.ResponseWriter, r *http.Request) {
	var in markReadInput
	if err := decodeJSON(r, &in); err != nil || len(in.ID) == 0 {
		writeError(w, http.StatusBadRequest, "id (array) is required")
		return
	}
	id, client, ok := h.clientFor(w, r, "")
	if !ok {
		return
	}
	type key struct{ chat, sender types.JID }
	groups := map[key][]types.MessageID{}
	var missing []string
	for _, raw := range in.ID {
		m, err := h.history.Get(r.Context(), id, raw)
		if err != nil || m.FromMe {
			missing = append(missing, raw)
			continue
		}
		chat, _ := types.ParseJID(m.ChatJID)
		sender, _ := types.ParseJID(m.SenderJID)
		k := key{chat, sender}
		groups[k] = append(groups[k], m.ID)
	}
	marked := 0
	for k, ids := range groups {
		if err := client.MarkRead(r.Context(), ids, time.Now(), k.chat, k.sender); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		marked += len(ids)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "marked": marked, "not_found": missing})
}

type pinInput struct {
	ID       string `json:"id"`
	Number   string `json:"number"`
	Pin      *bool  `json:"pin"`
	Duration int    `json:"duration"` // seconds: 86400, 604800 or 2592000
}

// UazPinMessage pins or unpins a message in its chat.
// @Summary Pin a message (UAZAPI)
// @Tags uazapi-messages
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body pinInput true "Pin"
// @Success 200 {object} map[string]interface{}
// @Router /message/pin [post]
func (h *Handler) UazPinMessage(w http.ResponseWriter, r *http.Request) {
	var in pinInput
	if err := decodeJSON(r, &in); err != nil || in.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	id, client, ok := h.clientFor(w, r, "")
	if !ok {
		return
	}
	chat, sender, _, _, err := h.target(r.Context(), id, client, in.ID, in.Number)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	pin := in.Pin == nil || *in.Pin
	typ := waE2E.PinInChatMessage_PIN_FOR_ALL
	if !pin {
		typ = waE2E.PinInChatMessage_UNPIN_FOR_ALL
	}
	if in.Duration <= 0 {
		in.Duration = 7 * 24 * 3600
	}
	msg := &waE2E.Message{
		PinInChatMessage: &waE2E.PinInChatMessage{
			Key:               client.BuildMessageKey(chat, sender, messages.NormalizeID(in.ID)),
			Type:              typ.Enum(),
			SenderTimestampMS: proto.Int64(time.Now().UnixMilli()),
		},
		MessageContextInfo: &waE2E.MessageContextInfo{MessageAddOnDurationInSecs: proto.Uint32(uint32(in.Duration))},
	}
	resp, err := client.SendMessage(r.Context(), chat, msg)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "id": resp.ID, "pinned": pin})
}

type downloadInput struct {
	ID           string `json:"id"`
	ReturnBase64 bool   `json:"return_base64"`
	ReturnLink   *bool  `json:"return_link"`
}

// UazDownload returns the media of a stored message as base64 and/or a temporary link.
// @Summary Download message media (UAZAPI)
// @Description Media archived in MinIO is served from there; otherwise it is downloaded from WhatsApp (and archived when storage is available).
// @Tags uazapi-messages
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body downloadInput true "Message id"
// @Success 200 {object} map[string]interface{}
// @Router /message/download [post]
func (h *Handler) UazDownload(w http.ResponseWriter, r *http.Request) {
	var in downloadInput
	if err := decodeJSON(r, &in); err != nil || in.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	id, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}
	m, err := h.history.Get(r.Context(), id, in.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "message not found in history")
		return
	}
	wantLink := in.ReturnLink == nil || *in.ReturnLink
	if !h.storage.Enabled() {
		wantLink = false
		in.ReturnBase64 = true
	}

	var data []byte
	mime := m.MediaMime
	key := m.MediaKey
	if key != "" && h.storage.Enabled() {
		data, mime, err = h.storage.Get(r.Context(), key)
		if err != nil {
			key = "" // object missing: fall back to WhatsApp
		}
	}
	if key == "" {
		raw := m.Raw()
		if raw == nil {
			writeError(w, http.StatusNotFound, "message has no downloadable media")
			return
		}
		client := h.manager.GetClient(id)
		if client == nil || !client.IsConnected() {
			writeError(w, http.StatusConflict, "instance is not connected")
			return
		}
		data, err = client.DownloadAny(r.Context(), raw)
		if err != nil {
			writeError(w, http.StatusBadGateway, "download from WhatsApp failed: "+err.Error())
			return
		}
		mime = m.MediaMime
		if h.storage.Enabled() {
			key, _ = h.history.ArchiveBytes(r.Context(), id, m.ID, data, mime, m.Timestamp)
		}
	}

	out := map[string]interface{}{"mimetype": mime, "size": len(data), "id": m.ID}
	if in.ReturnBase64 {
		out["base64Data"] = base64.StdEncoding.EncodeToString(data)
	}
	if wantLink && key != "" {
		if link, err := h.storage.PresignedURL(r.Context(), key, 24*time.Hour); err == nil {
			out["fileURL"] = link
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// UazListAsync lists queued (not yet sent) messages of the instance.
// @Summary Pending queued messages (UAZAPI)
// @Tags uazapi-messages
// @Produce json
// @Param token header string true "Instance Token"
// @Success 200 {object} map[string]interface{}
// @Router /message/async [get]
func (h *Handler) UazListAsync(w http.ResponseWriter, r *http.Request) {
	id, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}
	jobs, total, err := h.messaging.Jobs().List(r.Context(), messaging.JobFilter{
		InstanceID: id, Status: []string{"scheduled", "queued", "sending", "retrying", "paused"}, Limit: 500,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"total": total, "items": jobs})
}

// UazClearAsync cancels every queued message of the instance.
// @Summary Cancel pending queued messages (UAZAPI)
// @Tags uazapi-messages
// @Produce json
// @Param token header string true "Instance Token"
// @Success 200 {object} map[string]interface{}
// @Router /message/async [delete]
func (h *Handler) UazClearAsync(w http.ResponseWriter, r *http.Request) {
	id, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}
	n, err := h.messaging.Jobs().CancelPending(r.Context(), id, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "cancelled": n})
}

type delayInput struct {
	Min int `json:"msg_delay_min"`
	Max int `json:"msg_delay_max"`
}

// UazUpdateDelaySettings sets the random delay range (ms) applied before each queued send.
// @Summary Queue delay settings (UAZAPI)
// @Tags uazapi-instance
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body delayInput true "Delay range in milliseconds"
// @Success 200 {object} whatsapp.InstanceSettings
// @Router /instance/updateDelaySettings [post]
func (h *Handler) UazUpdateDelaySettings(w http.ResponseWriter, r *http.Request) {
	var in delayInput
	if err := decodeJSON(r, &in); err != nil || in.Min < 0 || in.Max < 0 || in.Max > 120000 {
		writeError(w, http.StatusBadRequest, "msg_delay_min and msg_delay_max must be between 0 and 120000 ms")
		return
	}
	id, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}
	inst, _ := h.manager.GetInstance(id)
	settings := inst.Settings
	settings.MsgDelayMin, settings.MsgDelayMax = in.Min, in.Max
	if err := h.manager.UpdateSettings(id, settings); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// PacingStatus shows today's send-pacing usage of an instance.
// @Summary Send pacing status
// @Tags device
// @Produce json
// @Param id path string true "Device ID"
// @Success 200 {object} map[string]interface{}
// @Router /device/{id}/pacing [get]
func (h *Handler) PacingStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := h.instanceFor(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	inst, _ := h.manager.GetInstance(id)
	writeJSON(w, http.StatusOK, h.messaging.Pacer().Status(r.Context(), id, inst.PairedAt))
}

// UploadMedia stores a file in MinIO so it can be sent later with media_url "media:<key>".
// @Summary Upload media
// @Description Accepts multipart/form-data (field "file") or JSON {"base64": "...", "mimetype": "...", "fileName": "..."}.
// @Tags media
// @Accept mpfd
// @Produce json
// @Param file formData file false "File"
// @Success 201 {object} map[string]interface{}
// @Router /media/upload [post]
func (h *Handler) UploadMedia(w http.ResponseWriter, r *http.Request) {
	if !h.storage.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "object storage (MinIO) is not configured")
		return
	}
	id, ok := h.instanceFor(w, r, r.FormValue("device_id"))
	if !ok {
		return
	}
	var data []byte
	var mime, name string
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		f, hdr, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "multipart field \"file\" is required")
			return
		}
		defer f.Close()
		data, err = io.ReadAll(f)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		mime, name = hdr.Header.Get("Content-Type"), hdr.Filename
	} else {
		var in struct {
			Base64   string `json:"base64"`
			MimeType string `json:"mimetype"`
			FileName string `json:"fileName"`
		}
		if err := decodeJSON(r, &in); err != nil || in.Base64 == "" {
			writeError(w, http.StatusBadRequest, "send multipart \"file\" or JSON {\"base64\": ...}")
			return
		}
		b64 := in.Base64
		if i := strings.Index(b64, ";base64,"); i != -1 {
			b64 = b64[i+8:]
		}
		var err error
		if data, err = base64.StdEncoding.DecodeString(b64); err != nil {
			writeError(w, http.StatusBadRequest, "invalid base64")
			return
		}
		mime, name = in.MimeType, in.FileName
	}
	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "empty file")
		return
	}
	if mime == "" || mime == "application/octet-stream" {
		mime = http.DetectContentType(data)
	}
	ext := path.Ext(name)
	if ext == "" {
		ext = messages.ExtFor(mime)
	}
	key := path.Join("uploads", id, time.Now().UTC().Format("2006/01/02"), whatsapp.GenerateAPIKey()+ext)
	if err := h.storage.Put(r.Context(), key, data, mime); err != nil {
		writeError(w, http.StatusBadGateway, "store media: "+err.Error())
		return
	}
	out := map[string]interface{}{"key": key, "media_ref": messaging.MediaRefPrefix + key, "mimetype": mime, "size": len(data)}
	if link, err := h.storage.PresignedURL(r.Context(), key, 24*time.Hour); err == nil {
		out["url"] = link
	}
	writeJSON(w, http.StatusCreated, out)
}
