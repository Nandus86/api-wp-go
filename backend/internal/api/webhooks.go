package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/user/whatsmeow-basileia/internal/auth"
	"github.com/user/whatsmeow-basileia/internal/service/webhook"
)

type webhookInput struct {
	InstanceID      string            `json:"instance_id"`
	URL             string            `json:"url"`
	Events          []string          `json:"events"`
	ExcludeMessages []string          `json:"exclude_messages"`
	Secret          *string           `json:"secret"`
	Headers         map[string]string `json:"headers"`
	Enabled         *bool             `json:"enabled"`
}

func redact(w webhook.Webhook) webhook.Webhook {
	w.Secret = ""
	return w
}

func redactAll(list []webhook.Webhook) []webhook.Webhook {
	for i := range list {
		list[i] = redact(list[i])
	}
	return list
}

// authorizeWebhook checks the caller may manage wh (global webhooks need an unscoped admin).
func (h *Handler) authorizeWebhook(w http.ResponseWriter, r *http.Request, wh *webhook.Webhook) bool {
	p := auth.FromContext(r.Context())
	if wh.InstanceID == nil {
		if !p.AtLeast(auth.RoleAdmin) || p.Scoped() {
			writeError(w, http.StatusForbidden, "global webhooks require an unscoped admin key")
			return false
		}
		return true
	}
	if !p.CanAccessInstance(*wh.InstanceID) {
		writeError(w, http.StatusForbidden, "API key not authorized for this instance")
		return false
	}
	return true
}

// ListWebhooks lists registered webhooks.
// @Summary List webhooks
// @Description Without ?instance an unscoped key lists every webhook (global ones have instance_id null).
// @Tags webhooks
// @Produce json
// @Param instance query string false "Instance ID"
// @Success 200 {array} webhook.Webhook
// @Router /webhooks [get]
func (h *Handler) ListWebhooks(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	scope := "*"
	if r.URL.Query().Get("instance") != "" || p.Scoped() {
		id, ok := h.instanceFor(w, r, "")
		if !ok {
			return
		}
		scope = id
	}
	list, err := h.webhooks.Store().ListWebhooks(r.Context(), scope)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, redactAll(list))
}

// CreateWebhook registers a webhook. Omit instance_id for a global webhook (admin only).
// @Summary Create webhook
// @Description Deliveries are signed with HMAC-SHA256 when a secret is set: header X-Webhook-Signature = "sha256=" + hex(HMAC(secret, X-Webhook-Timestamp + "." + body)).
// @Tags webhooks
// @Accept json
// @Produce json
// @Param request body webhookInput true "Webhook"
// @Success 201 {object} webhook.Webhook
// @Router /webhooks [post]
func (h *Handler) CreateWebhook(w http.ResponseWriter, r *http.Request) {
	var in webhookInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	wh, ok := h.webhookFromInput(w, r, in, nil)
	if !ok {
		return
	}
	if err := h.webhooks.Store().CreateWebhook(r.Context(), wh); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	created, _ := h.webhooks.Store().GetWebhook(r.Context(), wh.ID)
	writeJSON(w, http.StatusCreated, redact(*created))
}

// webhookFromInput validates input and applies it onto base (nil = new webhook).
func (h *Handler) webhookFromInput(w http.ResponseWriter, r *http.Request, in webhookInput, base *webhook.Webhook) (*webhook.Webhook, bool) {
	wh := base
	if wh == nil {
		wh = &webhook.Webhook{Enabled: true}
		if in.InstanceID != "" {
			id, ok := h.instanceFor(w, r, in.InstanceID)
			if !ok {
				return nil, false
			}
			wh.InstanceID = &id
		} else if p := auth.FromContext(r.Context()); p.Kind == auth.KindInstance {
			wh.InstanceID = &p.InstanceID
		}
	}
	if !h.authorizeWebhook(w, r, wh) {
		return nil, false
	}
	if in.URL != "" || base == nil {
		if err := webhook.ValidateURL(in.URL); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return nil, false
		}
		wh.URL = strings.TrimSpace(in.URL)
	}
	if in.Events != nil {
		wh.Events = in.Events
	}
	if in.ExcludeMessages != nil {
		wh.ExcludeMessages = in.ExcludeMessages
	}
	if in.Secret != nil {
		wh.Secret = *in.Secret
	}
	if in.Headers != nil {
		wh.Headers = in.Headers
	}
	if in.Enabled != nil {
		wh.Enabled = *in.Enabled
	}
	return wh, true
}

func (h *Handler) loadWebhook(w http.ResponseWriter, r *http.Request) (*webhook.Webhook, bool) {
	wh, err := h.webhooks.Store().GetWebhook(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, webhook.ErrNotFound) {
		writeError(w, http.StatusNotFound, "webhook not found")
		return nil, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	if !h.authorizeWebhook(w, r, wh) {
		return nil, false
	}
	return wh, true
}

// GetWebhook returns one webhook.
// @Summary Get webhook
// @Tags webhooks
// @Produce json
// @Param id path string true "Webhook ID"
// @Success 200 {object} webhook.Webhook
// @Router /webhooks/{id} [get]
func (h *Handler) GetWebhook(w http.ResponseWriter, r *http.Request) {
	if wh, ok := h.loadWebhook(w, r); ok {
		writeJSON(w, http.StatusOK, redact(*wh))
	}
}

// UpdateWebhook changes a webhook (only the fields sent are changed).
// @Summary Update webhook
// @Tags webhooks
// @Accept json
// @Produce json
// @Param id path string true "Webhook ID"
// @Param request body webhookInput true "Changes"
// @Success 200 {object} webhook.Webhook
// @Router /webhooks/{id} [put]
func (h *Handler) UpdateWebhook(w http.ResponseWriter, r *http.Request) {
	wh, ok := h.loadWebhook(w, r)
	if !ok {
		return
	}
	var in webhookInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if wh, ok = h.webhookFromInput(w, r, in, wh); !ok {
		return
	}
	if err := h.webhooks.Store().UpdateWebhook(r.Context(), wh); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, _ := h.webhooks.Store().GetWebhook(r.Context(), wh.ID)
	writeJSON(w, http.StatusOK, redact(*updated))
}

// DeleteWebhook removes a webhook.
// @Summary Delete webhook
// @Tags webhooks
// @Param id path string true "Webhook ID"
// @Success 204
// @Router /webhooks/{id} [delete]
func (h *Handler) DeleteWebhook(w http.ResponseWriter, r *http.Request) {
	wh, ok := h.loadWebhook(w, r)
	if !ok {
		return
	}
	if err := h.webhooks.Store().DeleteWebhook(r.Context(), wh.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// TestWebhook queues a "webhook.test" delivery (single attempt).
// @Summary Send a test event
// @Tags webhooks
// @Produce json
// @Param id path string true "Webhook ID"
// @Success 202 {object} map[string]interface{}
// @Router /webhooks/{id}/test [post]
func (h *Handler) TestWebhook(w http.ResponseWriter, r *http.Request) {
	wh, ok := h.loadWebhook(w, r)
	if !ok {
		return
	}
	id, err := h.webhooks.Test(r.Context(), wh)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]interface{}{"delivery_id": id, "status": "queued"})
}

// deliveryScope returns the instance restriction for delivery queries.
func (h *Handler) deliveryScope(w http.ResponseWriter, r *http.Request) (instanceID string, allowed []string, ok bool) {
	p := auth.FromContext(r.Context())
	if r.URL.Query().Get("instance") != "" || p.Kind == auth.KindInstance {
		id, ok := h.instanceFor(w, r, "")
		return id, nil, ok
	}
	if p.Scoped() {
		return "", p.AllowedInstances, true
	}
	return "", nil, true
}

// ListDeliveries lists webhook deliveries (outbox rows), newest first.
// @Summary List webhook deliveries
// @Tags webhooks
// @Produce json
// @Param instance query string false "Instance ID"
// @Param status query string false "pending, delivering, retrying, delivered or failed"
// @Param event query string false "Event name"
// @Param webhook query string false "Webhook ID"
// @Param limit query int false "Max rows (default 50, max 500)"
// @Param offset query int false "Offset"
// @Success 200 {object} map[string]interface{}
// @Router /webhooks/deliveries [get]
func (h *Handler) ListDeliveries(w http.ResponseWriter, r *http.Request) {
	instanceID, allowed, ok := h.deliveryScope(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	list, total, err := h.webhooks.Store().ListDeliveries(r.Context(), webhook.DeliveryFilter{
		InstanceID: instanceID, InstanceIDs: allowed, Status: q.Get("status"), Event: q.Get("event"),
		WebhookID: q.Get("webhook"), Limit: limit, Offset: offset,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"total": total, "items": list})
}

func (h *Handler) loadDelivery(w http.ResponseWriter, r *http.Request) (*webhook.Delivery, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid delivery id")
		return nil, false
	}
	d, err := h.webhooks.Store().GetDelivery(r.Context(), id)
	if errors.Is(err, webhook.ErrNotFound) {
		writeError(w, http.StatusNotFound, "delivery not found")
		return nil, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	p := auth.FromContext(r.Context())
	if p.Scoped() && !p.CanAccessInstance(d.InstanceID) {
		writeError(w, http.StatusForbidden, "API key not authorized for this instance")
		return nil, false
	}
	return d, true
}

// GetDelivery returns one delivery including its body.
// @Summary Get webhook delivery
// @Tags webhooks
// @Produce json
// @Param id path int true "Delivery ID"
// @Success 200 {object} webhook.Delivery
// @Router /webhooks/deliveries/{id} [get]
func (h *Handler) GetDelivery(w http.ResponseWriter, r *http.Request) {
	if d, ok := h.loadDelivery(w, r); ok {
		writeJSON(w, http.StatusOK, d)
	}
}

// RedriveDelivery delivers one failed (or delivered) row again.
// @Summary Redrive a delivery
// @Tags webhooks
// @Produce json
// @Param id path int true "Delivery ID"
// @Success 202 {object} map[string]interface{}
// @Router /webhooks/deliveries/{id}/redrive [post]
func (h *Handler) RedriveDelivery(w http.ResponseWriter, r *http.Request) {
	d, ok := h.loadDelivery(w, r)
	if !ok {
		return
	}
	n, err := h.webhooks.RedriveAndEnqueue(r.Context(), []int64{d.ID}, nil, d.Status)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]interface{}{"redriven": n})
}

type redriveInput struct {
	InstanceID string  `json:"instance_id"`
	Status     string  `json:"status"`
	IDs        []int64 `json:"ids"`
}

// RedriveDeliveries re-delivers failed rows in bulk (optionally for one instance).
// @Summary Redrive deliveries in bulk
// @Tags webhooks
// @Accept json
// @Produce json
// @Param request body redriveInput false "Filter (default: every failed delivery)"
// @Success 202 {object} map[string]interface{}
// @Router /webhooks/deliveries/redrive [post]
func (h *Handler) RedriveDeliveries(w http.ResponseWriter, r *http.Request) {
	var in redriveInput
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	p := auth.FromContext(r.Context())
	var instances []string
	if in.InstanceID != "" || p.Kind == auth.KindInstance {
		id, ok := h.instanceFor(w, r, in.InstanceID)
		if !ok {
			return
		}
		instances = []string{id}
	} else if p.Scoped() {
		instances = p.AllowedInstances
	}
	if in.Status != "" && in.Status != "failed" && in.Status != "delivered" {
		writeError(w, http.StatusBadRequest, "status must be failed or delivered")
		return
	}
	n, err := h.webhooks.RedriveAndEnqueue(r.Context(), in.IDs, instances, in.Status)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]interface{}{"redriven": n})
}

// DeliveryStats counts deliveries per status.
// @Summary Webhook delivery stats
// @Tags webhooks
// @Produce json
// @Param instance query string false "Instance ID"
// @Success 200 {object} map[string]int
// @Router /webhooks/stats [get]
func (h *Handler) DeliveryStats(w http.ResponseWriter, r *http.Request) {
	instanceID, allowed, ok := h.deliveryScope(w, r)
	if !ok {
		return
	}
	if instanceID != "" {
		allowed = []string{instanceID}
	}
	stats, err := h.webhooks.Store().Stats(r.Context(), allowed)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// ---- UAZAPI compatible endpoints (/webhook, /globalwebhook) ----

type uazWebhook struct {
	ID                  string   `json:"id"`
	Enabled             bool     `json:"enabled"`
	URL                 string   `json:"url"`
	Events              []string `json:"events"`
	ExcludeMessages     []string `json:"excludeMessages"`
	AddURLEvents        bool     `json:"addUrlEvents"`
	AddURLTypesMessages bool     `json:"addUrlTypesMessages"`
	HasSecret           bool     `json:"hasSecret"`
}

func toUaz(list []webhook.Webhook) []uazWebhook {
	out := make([]uazWebhook, 0, len(list))
	for _, wh := range list {
		out = append(out, uazWebhook{ID: wh.ID, Enabled: wh.Enabled, URL: wh.URL, Events: wh.Events, ExcludeMessages: wh.ExcludeMessages, HasSecret: wh.Secret != ""})
	}
	return out
}

type uazWebhookInput struct {
	ID              string   `json:"id"`
	Enabled         *bool    `json:"enabled"`
	URL             string   `json:"url"`
	Events          []string `json:"events"`
	ExcludeMessages []string `json:"excludeMessages"`
	Secret          *string  `json:"secret"`
	Action          string   `json:"action"` // add, update, delete; empty = simple mode (single webhook)
}

// UazGetWebhook lists the webhooks of the instance (UAZAPI format).
// @Summary List instance webhooks (UAZAPI)
// @Tags uazapi-webhooks
// @Produce json
// @Param token header string true "Instance Token"
// @Success 200 {array} uazWebhook
// @Router /webhook [get]
func (h *Handler) UazGetWebhook(w http.ResponseWriter, r *http.Request) {
	id, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}
	list, err := h.webhooks.Store().ListWebhooks(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toUaz(list))
}

// UazSetWebhook creates, updates or deletes an instance webhook (UAZAPI format).
// @Summary Configure instance webhook (UAZAPI)
// @Description Without "action" the instance keeps a single webhook (created or replaced).
// @Tags uazapi-webhooks
// @Accept json
// @Produce json
// @Param token header string true "Instance Token"
// @Param request body uazWebhookInput true "Webhook"
// @Success 200 {array} uazWebhook
// @Router /webhook [post]
func (h *Handler) UazSetWebhook(w http.ResponseWriter, r *http.Request) {
	id, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}
	h.uazSetWebhook(w, r, &id)
}

// UazGetGlobalWebhook lists global webhooks (UAZAPI format).
// @Summary List global webhooks (UAZAPI)
// @Tags uazapi-webhooks
// @Produce json
// @Param admintoken header string true "Admin key"
// @Success 200 {array} uazWebhook
// @Router /globalwebhook [get]
func (h *Handler) UazGetGlobalWebhook(w http.ResponseWriter, r *http.Request) {
	list, err := h.webhooks.Store().ListWebhooks(r.Context(), "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toUaz(list))
}

// UazSetGlobalWebhook configures a global webhook (UAZAPI format).
// @Summary Configure global webhook (UAZAPI)
// @Tags uazapi-webhooks
// @Accept json
// @Produce json
// @Param admintoken header string true "Admin key"
// @Param request body uazWebhookInput true "Webhook"
// @Success 200 {array} uazWebhook
// @Router /globalwebhook [post]
func (h *Handler) UazSetGlobalWebhook(w http.ResponseWriter, r *http.Request) {
	h.uazSetWebhook(w, r, nil)
}

func (h *Handler) uazSetWebhook(w http.ResponseWriter, r *http.Request, instanceID *string) {
	var in uazWebhookInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	store := h.webhooks.Store()
	scope := ""
	if instanceID != nil {
		scope = *instanceID
	}
	existing, err := store.ListWebhooks(r.Context(), scope)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	find := func(id string) *webhook.Webhook {
		for i := range existing {
			if existing[i].ID == id {
				return &existing[i]
			}
		}
		return nil
	}

	action := strings.ToLower(in.Action)
	if action == "" {
		// Simple mode: one webhook per instance.
		if len(existing) > 0 {
			action, in.ID = "update", existing[0].ID
		} else {
			action = "add"
		}
	}
	apply := func(wh *webhook.Webhook) bool {
		if in.URL != "" || wh.URL == "" {
			if err := webhook.ValidateURL(in.URL); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return false
			}
			wh.URL = strings.TrimSpace(in.URL)
		}
		if in.Events != nil {
			wh.Events = in.Events
		}
		if in.ExcludeMessages != nil {
			wh.ExcludeMessages = in.ExcludeMessages
		}
		if in.Enabled != nil {
			wh.Enabled = *in.Enabled
		}
		if in.Secret != nil {
			wh.Secret = *in.Secret
		}
		return true
	}

	switch action {
	case "add":
		wh := &webhook.Webhook{InstanceID: instanceID, Enabled: true}
		if !apply(wh) {
			return
		}
		err = store.CreateWebhook(r.Context(), wh)
	case "update":
		wh := find(in.ID)
		if wh == nil {
			writeError(w, http.StatusNotFound, "webhook not found")
			return
		}
		if !apply(wh) {
			return
		}
		err = store.UpdateWebhook(r.Context(), wh)
	case "delete":
		if find(in.ID) == nil {
			writeError(w, http.StatusNotFound, "webhook not found")
			return
		}
		err = store.DeleteWebhook(r.Context(), in.ID)
	default:
		writeError(w, http.StatusBadRequest, "action must be add, update or delete")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	list, _ := store.ListWebhooks(r.Context(), scope)
	writeJSON(w, http.StatusOK, toUaz(list))
}

// UazWebhookErrors lists failed deliveries of the instance (UAZAPI format).
// @Summary Failed webhook deliveries (UAZAPI)
// @Tags uazapi-webhooks
// @Produce json
// @Param token header string true "Instance Token"
// @Success 200 {array} webhook.Delivery
// @Router /webhook/errors [get]
func (h *Handler) UazWebhookErrors(w http.ResponseWriter, r *http.Request) {
	id, ok := h.instanceFor(w, r, "")
	if !ok {
		return
	}
	list, _, err := h.webhooks.Store().ListDeliveries(r.Context(), webhook.DeliveryFilter{InstanceID: id, Status: "failed", Limit: 100, WithBody: true})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// UazGlobalWebhookErrors lists failed deliveries of global webhooks.
// @Summary Failed global webhook deliveries (UAZAPI)
// @Tags uazapi-webhooks
// @Produce json
// @Param admintoken header string true "Admin key"
// @Success 200 {array} webhook.Delivery
// @Router /globalwebhook/errors [get]
func (h *Handler) UazGlobalWebhookErrors(w http.ResponseWriter, r *http.Request) {
	list, _, err := h.webhooks.Store().ListDeliveries(r.Context(), webhook.DeliveryFilter{Status: "failed", Limit: 100, WithBody: true})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}
