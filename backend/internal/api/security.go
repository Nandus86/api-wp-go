package api

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/user/whatsmeow-basileia/internal/audit"
	"github.com/user/whatsmeow-basileia/internal/auth"
	"github.com/user/whatsmeow-basileia/pkg/logger"
	"go.uber.org/zap"
)

// authenticate resolves the caller. With AUTH_REQUIRED=false (legacy mode) requests without
// a valid key run as an anonymous admin, which reproduces the previous open behaviour.
func (h *Handler) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := auth.TokenFromRequest(r)
		var p *auth.Principal
		if token != "" {
			var err error
			p, err = h.auth.Authenticate(r.Context(), token)
			if err != nil && !errors.Is(err, auth.ErrInvalidKey) {
				logger.Error("Authentication backend error", zap.Error(err))
				writeError(w, http.StatusServiceUnavailable, "authentication temporarily unavailable")
				return
			}
		}
		if p == nil {
			if h.cfg.AuthRequired {
				w.Header().Set("WWW-Authenticate", `Bearer realm="whatsmeow-basileia"`)
				writeError(w, http.StatusUnauthorized, "missing or invalid API key")
				return
			}
			p = &auth.Principal{Kind: auth.KindAnonymous, ID: "anonymous", Name: "anonymous", Role: auth.RoleAdmin}
		}
		next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
	})
}

// requireRole rejects callers below min. Instance tokens count as operators.
func requireRole(min auth.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := auth.FromContext(r.Context())
			if !p.AtLeast(min) {
				writeError(w, http.StatusForbidden, "insufficient permissions: requires role "+string(min))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requireGlobalAdmin allows only admins that are not limited to specific instances.
func requireGlobalAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := auth.FromContext(r.Context())
		if !p.AtLeast(auth.RoleAdmin) || p.Scoped() {
			writeError(w, http.StatusForbidden, "requires an unscoped admin key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ipAllowlist enforces ALLOWED_CIDRS (empty = allow everyone).
func (h *Handler) ipAllowlist(next http.Handler) http.Handler {
	if len(h.allowedNets) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := net.ParseIP(clientIP(r))
		for _, n := range h.allowedNets {
			if ip != nil && n.Contains(ip) {
				next.ServeHTTP(w, r)
				return
			}
		}
		writeError(w, http.StatusForbidden, "client IP not allowed")
	})
}

func parseCIDRs(list []string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, c := range list {
		if !strings.Contains(c, "/") {
			if strings.Contains(c, ":") {
				c += "/128"
			} else {
				c += "/32"
			}
		}
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// rateLimit applies RATE_LIMIT_PER_MINUTE per credential (or per IP for anonymous callers).
// Long-lived streams (QR SSE, events) are not counted.
func (h *Handler) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.cfg.RateLimitPerMin <= 0 || isStream(r) {
			next.ServeHTTP(w, r)
			return
		}
		key := "ip:" + clientIP(r)
		if p := auth.FromContext(r.Context()); p != nil && p.Kind != auth.KindAnonymous {
			key = p.Kind + ":" + p.ID
		}
		res := h.limiter.Allow(r.Context(), key, h.cfg.RateLimitPerMin, time.Minute)
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(res.Limit))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))
		if !res.Allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(res.Reset.Seconds())+1))
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isStream(r *http.Request) bool {
	p := r.URL.Path
	return strings.HasSuffix(p, "/qr") || strings.HasSuffix(p, "/sse") || strings.HasSuffix(p, "/ws") ||
		strings.HasSuffix(p, "/events")
}

// Paths whose mutations are high-volume traffic (message sends), not management actions.
var auditSkipPrefixes = []string{
	"/api/send/", "/api/message/", "/api/sender/", "/api/chat/read", "/api/debug/",
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// auditTrail records every mutating management request with its outcome.
func (h *Handler) auditTrail(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions || h.audit == nil {
			next.ServeHTTP(w, r)
			return
		}
		for _, prefix := range auditSkipPrefixes {
			if strings.HasPrefix(r.URL.Path, prefix) {
				next.ServeHTTP(w, r)
				return
			}
		}
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		p := auth.FromContext(r.Context())
		entry := audit.Entry{
			Method:     r.Method,
			Path:       r.URL.Path,
			StatusCode: rec.status,
			IP:         clientIP(r),
			RequestID:  middleware.GetReqID(r.Context()),
		}
		if entry.StatusCode == 0 {
			entry.StatusCode = http.StatusOK
		}
		if rc := chi.RouteContext(r.Context()); rc != nil {
			entry.Route = rc.RoutePattern()
			if id := rc.URLParam("id"); id != "" && strings.HasPrefix(entry.Route, "/api/device/") {
				if canonical, ok := h.manager.ResolveID(id); ok {
					id = canonical
				}
				entry.InstanceID = id
			}
		}
		if p != nil {
			entry.ActorType, entry.ActorID, entry.ActorName = p.Kind, p.ID, p.Name
			if entry.InstanceID == "" && p.Kind == auth.KindInstance {
				entry.InstanceID = p.InstanceID
			}
		}
		h.audit.Record(entry)
	})
}

// instanceFor resolves the instance a request targets and enforces the caller's scope.
// requested may be an instance id or an instance api_key (both were accepted before).
// When empty it falls back to the X-Instance-Id header, the ?instance= query parameter
// and, for instance tokens, the token's own instance.
func (h *Handler) instanceFor(w http.ResponseWriter, r *http.Request, requested string) (string, bool) {
	p := auth.FromContext(r.Context())
	if requested == "" {
		requested = strings.TrimSpace(r.Header.Get("X-Instance-Id"))
	}
	if requested == "" {
		requested = strings.TrimSpace(r.URL.Query().Get("instance"))
	}

	if p != nil && p.Kind == auth.KindInstance {
		if requested == "" || requested == p.InstanceID {
			return p.InstanceID, true
		}
		if id, ok := h.manager.ResolveID(requested); ok && id == p.InstanceID {
			return id, true
		}
		writeError(w, http.StatusForbidden, "API key not authorized for this instance")
		return "", false
	}

	if requested == "" && p != nil && p.Kind == auth.KindAnonymous {
		// Legacy open mode: the token header itself selected the device.
		requested = auth.TokenFromRequest(r)
	}
	if requested == "" {
		writeError(w, http.StatusBadRequest, "instance not specified: use the instance token, device_id, the X-Instance-Id header or ?instance=")
		return "", false
	}
	id, ok := h.manager.ResolveID(requested)
	if !ok {
		writeError(w, http.StatusNotFound, "instance not found")
		return "", false
	}
	if !p.CanAccessInstance(id) {
		writeError(w, http.StatusForbidden, "API key not authorized for this instance")
		return "", false
	}
	return id, true
}
