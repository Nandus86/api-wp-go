// Package auth implements API key authentication and authorization.
//
// Two kinds of credentials are accepted:
//   - API keys (table api_keys) with a role (admin, operator, viewer) and an optional
//     allowlist of instances;
//   - the per-instance api_key (instances.api_key), which keeps working exactly as before
//     and grants operator rights on that single instance.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
)

type Role string

const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
	// RoleInstance is the role of a per-instance token: operator rights on one instance.
	RoleInstance Role = "instance"
)

func (r Role) Valid() bool {
	return r == RoleAdmin || r == RoleOperator || r == RoleViewer
}

func (r Role) rank() int {
	switch r {
	case RoleAdmin:
		return 3
	case RoleOperator, RoleInstance:
		return 2
	case RoleViewer:
		return 1
	}
	return 0
}

const (
	KindKey       = "api_key"
	KindInstance  = "instance"
	KindAnonymous = "anonymous"
)

// Principal is the authenticated caller.
type Principal struct {
	Kind             string   `json:"kind"`
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Role             Role     `json:"role"`
	AllowedInstances []string `json:"allowed_instances"`
	// InstanceID is set for per-instance tokens.
	InstanceID string `json:"instance_id,omitempty"`
}

// AtLeast reports whether the principal's role is >= min.
func (p *Principal) AtLeast(min Role) bool {
	return p != nil && p.Role.rank() >= min.rank()
}

// Scoped reports whether the principal is limited to a subset of instances.
func (p *Principal) Scoped() bool {
	return p.Kind == KindInstance || len(p.AllowedInstances) > 0
}

// CanAccessInstance reports whether the principal may act on the instance id.
func (p *Principal) CanAccessInstance(id string) bool {
	if p == nil {
		return false
	}
	if p.Kind == KindInstance {
		return p.InstanceID == id
	}
	if len(p.AllowedInstances) == 0 {
		return true
	}
	for _, a := range p.AllowedInstances {
		if a == id {
			return true
		}
	}
	return false
}

type ctxKey struct{}

func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

func FromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(ctxKey{}).(*Principal)
	return p
}

// TokenFromRequest extracts a credential from the usual places, in priority order.
// Query parameters are accepted because EventSource and WebSocket clients cannot set headers.
func TokenFromRequest(r *http.Request) string {
	if v := r.Header.Get("Authorization"); v != "" {
		if strings.HasPrefix(strings.ToLower(v), "bearer ") {
			return strings.TrimSpace(v[7:])
		}
	}
	for _, h := range []string{"X-API-Key", "api_key", "token", "admintoken"} {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			return v
		}
	}
	q := r.URL.Query()
	for _, k := range []string{"api_key", "token"} {
		if v := strings.TrimSpace(q.Get(k)); v != "" {
			return v
		}
	}
	return ""
}

// HashKey returns the hex SHA-256 of a key. Keys are high-entropy random strings,
// so a fast hash is appropriate (no need for bcrypt-style stretching).
func HashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// GenerateKey returns a new random key ("wmb_" + 48 hex chars) and its display prefix.
func GenerateKey() (key, prefix string) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	key = "wmb_" + hex.EncodeToString(b)
	return key, key[:12]
}

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// MaskKey hides all but the first characters of a secret.
func MaskKey(k string) string {
	if len(k) <= 8 {
		return strings.Repeat("*", len(k))
	}
	return k[:8] + strings.Repeat("*", 8)
}
