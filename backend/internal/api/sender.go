package api

import (
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/user/whatsmeow-basileia/internal/infrastructure/rabbitmq"
	"github.com/user/whatsmeow-basileia/internal/service/messaging"
)

// senderMessage is one message of a campaign (UAZAPI /sender fields).
type senderMessage struct {
	Number      string   `json:"number,omitempty"`
	Type        string   `json:"type"`
	Text        string   `json:"text,omitempty"`
	File        string   `json:"file,omitempty"`
	DocName     string   `json:"docName,omitempty"`
	MimeType    string   `json:"mimetype,omitempty"`
	FullName    string   `json:"fullName,omitempty"`
	PhoneNumber string   `json:"phoneNumber,omitempty"`
	Latitude    float64  `json:"latitude,omitempty"`
	Longitude   float64  `json:"longitude,omitempty"`
	Name        string   `json:"name,omitempty"`
	Address     string   `json:"address,omitempty"`
	FooterText  string   `json:"footerText,omitempty"`
	ListButton  string   `json:"listButton,omitempty"`
	ButtonText  string   `json:"buttonText,omitempty"`
	Choices     []string `json:"choices,omitempty"`
	Selectable  int      `json:"selectableCount,omitempty"`
	Mentions    string   `json:"mentions,omitempty"`
	Delay       int      `json:"delay,omitempty"`
	TrackSource string   `json:"track_source,omitempty"`
	TrackID     string   `json:"track_id,omitempty"`
}

type senderSimpleInput struct {
	senderMessage
	Numbers      []string `json:"numbers"`
	Folder       string   `json:"folder"`
	Info         string   `json:"info"`
	DelayMin     int      `json:"delayMin"`
	DelayMax     int      `json:"delayMax"`
	ScheduledFor int64    `json:"scheduled_for"`
}

type senderAdvancedInput struct {
	Info         string          `json:"info"`
	DelayMin     int             `json:"delayMin"`
	DelayMax     int             `json:"delayMax"`
	ScheduledFor int64           `json:"scheduled_for"`
	Messages     []senderMessage `json:"messages"`
}

const maxCampaignSize = 10000

// scheduleStart interprets UAZAPI's scheduled_for: a millisecond timestamp, or minutes from now.
func scheduleStart(v int64) time.Time {
	switch {
	case v <= 0:
		return time.Now()
	case v > 100_000_000_000: // looks like epoch milliseconds
		return time.UnixMilli(v)
	default:
		return time.Now().Add(time.Duration(v) * time.Minute)
	}
}

func isMediaType(t string) bool {
	switch strings.ToLower(t) {
	case "image", "video", "audio", "ptt", "myaudio", "document":
		return true
	}
	return false
}

// toItem converts a campaign message into a queue item.
func toItem(m senderMessage, at time.Time) messaging.BatchItem {
	opts := messaging.EnqueueOptions{ScheduledAt: at, Recipient: m.Number, TrackSource: m.TrackSource, TrackID: m.TrackID}
	send := messaging.SendOptions{Delay: m.Delay, Mentions: m.Mentions, TrackSource: m.TrackSource, TrackID: m.TrackID}
	if isMediaType(m.Type) {
		p := messaging.SendMediaPayload{Number: m.Number, Type: m.Type, Caption: m.Text, FileName: m.DocName, MimeType: m.MimeType, SendOptions: send}
		if strings.HasPrefix(m.File, "data:") || (!strings.Contains(m.File, "://") && !strings.HasPrefix(m.File, messaging.MediaRefPrefix) && len(m.File) > 200) {
			p.Base64 = m.File
		} else {
			p.MediaURL = m.File
		}
		return messaging.BatchItem{Queue: rabbitmq.QueueSendMedia, Payload: p, Options: opts}
	}
	typ := strings.ToLower(m.Type)
	switch typ {
	case "text", "":
		typ = ""
	case "menu":
		typ = "list"
	}
	listButton := m.ListButton
	if listButton == "" {
		listButton = m.ButtonText
	}
	p := messaging.SendMessagePayload{
		Number: m.Number, Type: typ, Message: m.Text, Footer: m.FooterText, ListButton: listButton, Choices: m.Choices,
		FullName: m.FullName, PhoneNumber: m.PhoneNumber, Latitude: m.Latitude, Longitude: m.Longitude, Name: m.Name, Address: m.Address,
		Question: m.Text, Options: m.Choices, MaxAnswer: m.Selectable, SendOptions: send,
	}
	return messaging.BatchItem{Queue: rabbitmq.QueueSendMessage, Payload: p, Options: opts}
}

// spread assigns each item a send time: start, then a random gap in [min, max] seconds.
func spread(n int, start time.Time, minSec, maxSec int) []time.Time {
	if minSec < 1 {
		minSec = 1
	}
	if maxSec < minSec {
		maxSec = minSec
	}
	out := make([]time.Time, n)
	at := start
	for i := 0; i < n; i++ {
		out[i] = at
		at = at.Add(time.Duration(minSec+rand.Intn(maxSec-minSec+1)) * time.Second)
	}
	return out
}

func (h *Handler) createCampaign(w http.ResponseWriter, r *http.Request, info string, items []messaging.BatchItem) {
	id, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}
	if len(items) == 0 {
		writeError(w, http.StatusBadRequest, "no messages to send")
		return
	}
	if len(items) > maxCampaignSize {
		writeError(w, http.StatusBadRequest, "campaign too large (max 10000 messages)")
		return
	}
	b, err := h.messaging.EnqueueBatch(r.Context(), id, info, items)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"folder_id": b.ID, "count": len(items), "status": "queued",
		"first_at": items[0].Options.ScheduledAt, "last_at": items[len(items)-1].Options.ScheduledAt,
	})
}

// UazSenderSimple creates a campaign: one message to many numbers.
// @Summary Create campaign (UAZAPI /sender/simple)
// @Description delayMin/delayMax are seconds between messages; scheduled_for is epoch ms or minutes from now.
// @Tags uazapi-sender
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body senderSimpleInput true "Campaign"
// @Success 200 {object} map[string]interface{}
// @Router /sender/simple [post]
func (h *Handler) UazSenderSimple(w http.ResponseWriter, r *http.Request) {
	var in senderSimpleInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	times := spread(len(in.Numbers), scheduleStart(in.ScheduledFor), in.DelayMin, in.DelayMax)
	items := make([]messaging.BatchItem, 0, len(in.Numbers))
	for i, n := range in.Numbers {
		m := in.senderMessage
		m.Number = n
		items = append(items, toItem(m, times[i]))
	}
	info := in.Folder
	if in.Info != "" {
		info = strings.TrimSpace(info + " " + in.Info)
	}
	h.createCampaign(w, r, info, items)
}

// UazSenderAdvanced creates a campaign with a different message per recipient.
// @Summary Create advanced campaign (UAZAPI /sender/advanced)
// @Tags uazapi-sender
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body senderAdvancedInput true "Campaign"
// @Success 200 {object} map[string]interface{}
// @Router /sender/advanced [post]
func (h *Handler) UazSenderAdvanced(w http.ResponseWriter, r *http.Request) {
	var in senderAdvancedInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	times := spread(len(in.Messages), scheduleStart(in.ScheduledFor), in.DelayMin, in.DelayMax)
	items := make([]messaging.BatchItem, 0, len(in.Messages))
	for i, m := range in.Messages {
		if m.Number == "" {
			writeError(w, http.StatusBadRequest, "every message needs a number")
			return
		}
		items = append(items, toItem(m, times[i]))
	}
	h.createCampaign(w, r, in.Info, items)
}

type senderEditInput struct {
	FolderID string `json:"folder_id"`
	Action   string `json:"action"`
}

// UazSenderEdit pauses (stop), resumes (continue) or deletes a campaign.
// @Summary Control campaign (UAZAPI /sender/edit)
// @Tags uazapi-sender
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body senderEditInput true "folder_id and action (stop, continue, delete)"
// @Success 200 {object} map[string]interface{}
// @Router /sender/edit [post]
func (h *Handler) UazSenderEdit(w http.ResponseWriter, r *http.Request) {
	var in senderEditInput
	if err := decodeJSON(r, &in); err != nil || in.FolderID == "" {
		writeError(w, http.StatusBadRequest, "folder_id and action are required")
		return
	}
	id, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}
	found, err := h.messaging.Jobs().SetBatchState(r.Context(), id, in.FolderID, strings.ToLower(in.Action))
	if !found {
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "folder_id": in.FolderID, "action": in.Action})
}

// UazSenderListFolders lists campaigns with per-status counts.
// @Summary List campaigns (UAZAPI /sender/listfolders)
// @Tags uazapi-sender
// @Produce json
// @Param token header string true "Instance Token"
// @Param status query string false "scheduled, running, paused, done or cancelled"
// @Success 200 {array} messaging.Batch
// @Router /sender/listfolders [get]
func (h *Handler) UazSenderListFolders(w http.ResponseWriter, r *http.Request) {
	id, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}
	list, err := h.messaging.Jobs().ListBatches(r.Context(), id, r.URL.Query().Get("status"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type senderListMessagesInput struct {
	FolderID      string `json:"folder_id"`
	MessageStatus string `json:"messageStatus"`
	Limit         int    `json:"limit"`
	Offset        int    `json:"offset"`
}

// UazSenderListMessages lists the messages (jobs) of a campaign.
// @Summary Campaign messages (UAZAPI /sender/listmessages)
// @Tags uazapi-sender
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body senderListMessagesInput true "Filter"
// @Success 200 {object} map[string]interface{}
// @Router /sender/listmessages [post]
func (h *Handler) UazSenderListMessages(w http.ResponseWriter, r *http.Request) {
	var in senderListMessagesInput
	if err := decodeJSON(r, &in); err != nil || in.FolderID == "" {
		writeError(w, http.StatusBadRequest, "folder_id is required")
		return
	}
	id, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}
	f := messaging.JobFilter{InstanceID: id, BatchID: in.FolderID, Limit: in.Limit, Offset: in.Offset}
	if in.MessageStatus != "" {
		f.Status = strings.Split(strings.ToLower(in.MessageStatus), ",")
	}
	jobs, total, err := h.messaging.Jobs().List(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"total": total, "messages": jobs})
}

// UazSenderClearDone deletes finished campaigns older than N hours (default 168).
// @Summary Clear finished campaigns (UAZAPI /sender/cleardone)
// @Tags uazapi-sender
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Success 200 {object} map[string]interface{}
// @Router /sender/cleardone [post]
func (h *Handler) UazSenderClearDone(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Hours int `json:"hours"`
	}
	if r.ContentLength != 0 {
		_ = decodeJSON(r, &in)
	}
	if in.Hours <= 0 {
		in.Hours = 168
	}
	id, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}
	n, err := h.messaging.Jobs().ClearDone(r.Context(), id, in.Hours)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "deleted": n})
}

// UazSenderClearAll deletes every campaign of the instance (pending messages are dropped).
// @Summary Delete all campaigns (UAZAPI /sender/clearall)
// @Tags uazapi-sender
// @Produce json
// @Param token header string true "Instance Token"
// @Success 200 {object} map[string]interface{}
// @Router /sender/clearall [delete]
func (h *Handler) UazSenderClearAll(w http.ResponseWriter, r *http.Request) {
	id, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}
	n, err := h.messaging.Jobs().ClearAllBatches(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "success", "deleted": n})
}
