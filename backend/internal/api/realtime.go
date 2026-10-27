package api

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/user/whatsmeow-basileia/internal/auth"
	"github.com/user/whatsmeow-basileia/internal/realtime"
)

// realtimeFilter builds a subscriber filter from the query string and the caller's scope.
func (h *Handler) realtimeFilter(w http.ResponseWriter, r *http.Request) (realtime.Filter, bool) {
	q := r.URL.Query()
	f := realtime.Filter{
		Events:          realtime.SplitList(q.Get("events")),
		ExcludeMessages: realtime.SplitList(q.Get("excludeMessages")),
	}
	p := auth.FromContext(r.Context())
	if q.Get("instance") != "" || p.Kind == auth.KindInstance {
		id, ok := h.instanceFor(w, r, "")
		if !ok {
			return f, false
		}
		f.Instances = map[string]bool{id: true}
	} else if p.Scoped() {
		f.Instances = map[string]bool{}
		for _, id := range p.AllowedInstances {
			f.Instances[id] = true
		}
	}
	return f, true
}

// WebSocket streams events as JSON frames: {"event","instance_id","timestamp","data"}.
// @Summary Realtime events (WebSocket)
// @Description Connect to /ws (or /api/ws) with ?api_key=... (browsers cannot set headers), optional ?instance=, ?events=messages,connection.update and ?excludeMessages=fromMeYes.
// @Tags realtime
// @Param api_key query string false "API key or instance token"
// @Param instance query string false "Instance ID"
// @Param events query string false "Comma separated events"
// @Router /ws [get]
func (h *Handler) WebSocket(w http.ResponseWriter, r *http.Request) {
	f, ok := h.realtimeFilter(w, r)
	if !ok {
		return
	}
	opts := &websocket.AcceptOptions{}
	if len(h.cfg.CORSOrigins) == 0 || h.cfg.CORSOrigins[0] == "*" {
		opts.InsecureSkipVerify = true // origin checks are delegated to CORS_ORIGINS
	} else {
		for _, o := range h.cfg.CORSOrigins {
			opts.OriginPatterns = append(opts.OriginPatterns, strings.TrimPrefix(strings.TrimPrefix(o, "https://"), "http://"))
		}
	}
	conn, err := websocket.Accept(w, r, opts)
	if err != nil {
		return
	}
	defer conn.CloseNow()

	sub := h.hub.Subscribe(f)
	defer h.hub.Unsubscribe(sub)

	// Reads only serve to notice the client going away (and answer pings).
	ctx := conn.CloseRead(r.Context())
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()

	_ = h.wsWrite(ctx, conn, []byte(`{"event":"ready","data":{}}`))
	for {
		select {
		case <-ctx.Done():
			conn.Close(websocket.StatusNormalClosure, "")
			return
		case msg := <-sub.C:
			if err := h.wsWrite(ctx, conn, msg); err != nil {
				return
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func (h *Handler) wsWrite(ctx context.Context, conn *websocket.Conn, msg []byte) error {
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, msg)
}

// SSE streams the same events as Server-Sent Events (UAZAPI /sse).
// @Summary Realtime events (SSE)
// @Tags realtime
// @Produce text/event-stream
// @Param token query string false "Instance token or API key"
// @Param events query string false "Comma separated events"
// @Param excludeMessages query string false "Comma separated message filters"
// @Router /sse [get]
func (h *Handler) SSE(w http.ResponseWriter, r *http.Request) {
	f, ok := h.realtimeFilter(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	sub := h.hub.Subscribe(f)
	defer h.hub.Unsubscribe(sub)

	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case msg := <-sub.C:
			if _, err := fmt.Fprintf(w, "data: %s\n\n", msg); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// Metrics serves Prometheus metrics. Access: METRICS_TOKEN as bearer, or an admin key.
// @Summary Prometheus metrics
// @Tags app
// @Produce plain
// @Router /metrics [get]
func (h *Handler) Metrics(w http.ResponseWriter, r *http.Request) {
	h.metrics.Handler().ServeHTTP(w, r)
}

// metricsAuth lets METRICS_TOKEN through, otherwise requires an admin credential.
func (h *Handler) metricsAuth(next http.Handler) http.Handler {
	adminChain := h.authenticate(requireRole(auth.RoleAdmin)(next))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.cfg.MetricsToken != "" && subtle.ConstantTimeCompare([]byte(auth.TokenFromRequest(r)), []byte(h.cfg.MetricsToken)) == 1 {
			next.ServeHTTP(w, r)
			return
		}
		adminChain.ServeHTTP(w, r)
	})
}
