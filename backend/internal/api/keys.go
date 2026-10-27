package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/user/whatsmeow-basileia/internal/audit"
	"github.com/user/whatsmeow-basileia/internal/auth"
)

// Me returns the authenticated principal (used by the dashboard login).
// @Summary Current credential
// @Tags auth
// @Produce json
// @Success 200 {object} auth.Principal
// @Router /auth/me [get]
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"principal":     p,
		"auth_required": h.cfg.AuthRequired,
		"version":       Version,
	})
}

// ListAPIKeys lists API keys (hashes and plaintexts are never returned).
// @Summary List API keys
// @Tags auth
// @Produce json
// @Success 200 {array} auth.APIKey
// @Router /auth/keys [get]
func (h *Handler) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.auth.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

// CreateAPIKey creates a key. The plaintext key is returned only in this response.
// @Summary Create API key
// @Tags auth
// @Accept json
// @Produce json
// @Param request body auth.CreateKeyInput true "Key"
// @Success 201 {object} map[string]interface{}
// @Router /auth/keys [post]
func (h *Handler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var in auth.CreateKeyInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !h.validInstances(w, in.AllowedInstances) {
		return
	}
	p := auth.FromContext(r.Context())
	key, plain, err := h.auth.Create(r.Context(), in, p.Name)
	if err != nil {
		h.keyError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"key":     plain,
		"api_key": key,
		"warning": "store this key now: it cannot be retrieved again",
	})
}

// UpdateAPIKey changes name, role, instance scope, expiry or revocation.
// @Summary Update API key
// @Tags auth
// @Accept json
// @Produce json
// @Param id path string true "Key ID"
// @Param request body auth.UpdateKeyInput true "Changes"
// @Success 200 {object} auth.APIKey
// @Router /auth/keys/{id} [put]
func (h *Handler) UpdateAPIKey(w http.ResponseWriter, r *http.Request) {
	var in auth.UpdateKeyInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if in.AllowedInstances != nil && !h.validInstances(w, *in.AllowedInstances) {
		return
	}
	key, err := h.auth.Update(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		h.keyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, key)
}

// DeleteAPIKey permanently deletes a key.
// @Summary Delete API key
// @Tags auth
// @Param id path string true "Key ID"
// @Success 204
// @Router /auth/keys/{id} [delete]
func (h *Handler) DeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	if err := h.auth.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		h.keyError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) validInstances(w http.ResponseWriter, ids []string) bool {
	for i, id := range ids {
		canonical, ok := h.manager.ResolveID(id)
		if !ok {
			writeError(w, http.StatusBadRequest, "unknown instance in allowed_instances: "+id)
			return false
		}
		ids[i] = canonical
	}
	return true
}

func (h *Handler) keyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, auth.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, auth.ErrLastAdmin):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// ListAudit returns the audit trail, newest first. Scoped admins only see their instances.
// @Summary Audit log
// @Tags auth
// @Produce json
// @Param instance query string false "Instance ID"
// @Param actor query string false "Actor (key) ID"
// @Param method query string false "HTTP method"
// @Param limit query int false "Max rows (default 100, max 500)"
// @Param offset query int false "Offset"
// @Success 200 {object} map[string]interface{}
// @Router /audit [get]
func (h *Handler) ListAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	f := audit.Filter{
		InstanceID: q.Get("instance"),
		ActorID:    q.Get("actor"),
		Method:     q.Get("method"),
		Limit:      limit,
		Offset:     offset,
	}
	if p := auth.FromContext(r.Context()); p.Scoped() {
		f.InstanceIDs = p.AllowedInstances
		if f.InstanceID != "" && !p.CanAccessInstance(f.InstanceID) {
			writeError(w, http.StatusForbidden, "API key not authorized for this instance")
			return
		}
	}
	entries, total, err := h.audit.List(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"total": total, "items": entries})
}
