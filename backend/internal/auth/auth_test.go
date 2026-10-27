package auth

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRoleOrdering(t *testing.T) {
	admin := &Principal{Role: RoleAdmin}
	op := &Principal{Role: RoleOperator}
	inst := &Principal{Role: RoleInstance, Kind: KindInstance, InstanceID: "a"}
	viewer := &Principal{Role: RoleViewer}

	cases := []struct {
		p    *Principal
		min  Role
		want bool
	}{
		{admin, RoleAdmin, true},
		{admin, RoleViewer, true},
		{op, RoleAdmin, false},
		{op, RoleOperator, true},
		{inst, RoleOperator, true},
		{inst, RoleAdmin, false},
		{viewer, RoleOperator, false},
		{viewer, RoleViewer, true},
		{nil, RoleViewer, false},
	}
	for _, c := range cases {
		if got := c.p.AtLeast(c.min); got != c.want {
			t.Errorf("%v.AtLeast(%s) = %v, want %v", c.p, c.min, got, c.want)
		}
	}
}

func TestInstanceScope(t *testing.T) {
	global := &Principal{Kind: KindKey, Role: RoleOperator}
	if global.Scoped() || !global.CanAccessInstance("anything") {
		t.Fatal("unscoped key must reach every instance")
	}

	scoped := &Principal{Kind: KindKey, Role: RoleOperator, AllowedInstances: []string{"a", "b"}}
	if !scoped.Scoped() || !scoped.CanAccessInstance("a") || scoped.CanAccessInstance("c") {
		t.Fatal("scoped key must reach only its allowlist")
	}

	inst := &Principal{Kind: KindInstance, Role: RoleInstance, InstanceID: "a"}
	if !inst.Scoped() || !inst.CanAccessInstance("a") || inst.CanAccessInstance("b") {
		t.Fatal("instance token must reach only its own instance")
	}
}

func TestTokenFromRequest(t *testing.T) {
	cases := []struct {
		name   string
		header map[string]string
		query  string
		want   string
	}{
		{"bearer", map[string]string{"Authorization": "Bearer abc"}, "", "abc"},
		{"x-api-key", map[string]string{"X-API-Key": "k1"}, "", "k1"},
		{"api_key header", map[string]string{"api_key": "k2"}, "", "k2"},
		{"token header (uazapi)", map[string]string{"token": "k3"}, "", "k3"},
		{"admintoken header (uazapi)", map[string]string{"admintoken": "k4"}, "", "k4"},
		{"query for SSE", nil, "api_key=q1", "q1"},
		{"bearer wins", map[string]string{"Authorization": "Bearer first", "X-API-Key": "second"}, "", "first"},
		{"none", nil, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/device?"+c.query, nil)
			for k, v := range c.header {
				r.Header.Set(k, v)
			}
			if got := TokenFromRequest(r); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestGenerateKeyAndHash(t *testing.T) {
	k1, p1 := GenerateKey()
	k2, _ := GenerateKey()
	if k1 == k2 {
		t.Fatal("keys must be random")
	}
	if !strings.HasPrefix(k1, "wmb_") || len(k1) != 52 || !strings.HasPrefix(k1, p1) {
		t.Fatalf("unexpected key format %q (prefix %q)", k1, p1)
	}
	if HashKey(k1) == HashKey(k2) || len(HashKey(k1)) != 64 {
		t.Fatal("hash must be a distinct sha256 hex digest")
	}
	if MaskKey("abcdefghijkl") != "abcdefgh********" {
		t.Fatalf("unexpected mask %q", MaskKey("abcdefghijkl"))
	}
}
