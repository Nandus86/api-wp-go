package api

import (
	"context"
	"net/http"
	"time"
)

type componentStatus struct {
	Status  string `json:"status"` // ok, down, disabled
	Error   string `json:"error,omitempty"`
	Latency string `json:"latency,omitempty"`
}

// Health is the liveness probe: the process is up and serving HTTP.
// @Summary Liveness probe
// @Tags app
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /health [get]
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"version": Version,
		"uptime":  time.Since(startedAt).Round(time.Second).String(),
	})
}

// Ready is the readiness probe: every required dependency answers.
// Postgres and RabbitMQ are required; Redis and MinIO are reported but optional.
// @Summary Readiness probe
// @Tags app
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /ready [get]
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	components := map[string]componentStatus{}
	ready := true

	check := func(name string, required bool, enabled bool, fn func(context.Context) error) {
		if !enabled {
			components[name] = componentStatus{Status: "disabled"}
			if required {
				ready = false
			}
			return
		}
		start := time.Now()
		err := fn(ctx)
		st := componentStatus{Status: "ok", Latency: time.Since(start).Round(time.Millisecond).String()}
		if err != nil {
			st.Status = "down"
			st.Error = err.Error()
			if required {
				ready = false
			}
		}
		components[name] = st
	}

	check("postgres", true, h.db != nil, func(ctx context.Context) error { return h.db.PingContext(ctx) })
	check("rabbitmq", true, h.rmqClient != nil, func(ctx context.Context) error {
		if !h.rmqClient.IsReady() {
			return errNotConnected
		}
		return nil
	})
	check("redis", false, h.redis != nil, func(ctx context.Context) error { return h.redis.Ping(ctx).Err() })
	check("minio", false, h.storage.Enabled(), func(ctx context.Context) error { return h.storage.Ping(ctx) })

	connected, total := h.manager.ConnectedCount()
	status := http.StatusOK
	state := "ready"
	if !ready {
		status = http.StatusServiceUnavailable
		state = "not_ready"
	}
	writeJSON(w, status, map[string]interface{}{
		"status":     state,
		"components": components,
		"instances":  map[string]int{"connected": connected, "total": total},
	})
}
