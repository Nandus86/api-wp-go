package auth_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/user/whatsmeow-basileia/internal/auth"
	"github.com/user/whatsmeow-basileia/internal/infrastructure/database/testdb"
)

func TestServiceLifecycle(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	lookup := func(token string) (string, string, bool) {
		if token == "instance-token" {
			return "inst1", "Instance 1", true
		}
		return "", "", false
	}
	svc := auth.NewService(db, "", lookup)

	// Bootstrap creates exactly one admin key and writes it to disk.
	dir := t.TempDir()
	key, path, err := svc.Bootstrap(ctx, dir)
	if err != nil || key == "" || path != filepath.Join(dir, ".api-key") {
		t.Fatalf("bootstrap: %q %q %v", key, path, err)
	}
	if b, _ := os.ReadFile(path); string(b) != key {
		t.Fatal("bootstrap key must be written to DATA_DIR/.api-key")
	}
	if again, _, _ := svc.Bootstrap(ctx, dir); again != "" {
		t.Fatal("bootstrap must not create a second admin key")
	}

	p, err := svc.Authenticate(ctx, key)
	if err != nil || p.Role != auth.RoleAdmin || p.Kind != auth.KindKey {
		t.Fatalf("admin key: %+v %v", p, err)
	}
	if p, err := svc.Authenticate(ctx, "instance-token"); err != nil || p.Kind != auth.KindInstance || p.InstanceID != "inst1" {
		t.Fatalf("instance token: %+v %v", p, err)
	}
	if _, err := svc.Authenticate(ctx, "nope"); !errors.Is(err, auth.ErrInvalidKey) {
		t.Fatalf("unknown token must fail, got %v", err)
	}

	// Scoped operator key, then revoke it.
	op, plain, err := svc.Create(ctx, auth.CreateKeyInput{Name: "op", Role: auth.RoleOperator, AllowedInstances: []string{"inst1"}}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if p, err := svc.Authenticate(ctx, plain); err != nil || !p.Scoped() || !p.CanAccessInstance("inst1") || p.CanAccessInstance("inst2") {
		t.Fatalf("scoped key: %+v %v", p, err)
	}
	revoked := true
	if _, err := svc.Update(ctx, op.ID, auth.UpdateKeyInput{Revoked: &revoked}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, plain); !errors.Is(err, auth.ErrInvalidKey) {
		t.Fatal("revoked key must stop working immediately")
	}

	// Expired keys are rejected.
	past := time.Now().Add(-time.Hour)
	_, expiredPlain, err := svc.Create(ctx, auth.CreateKeyInput{Name: "old", Role: auth.RoleViewer, ExpiresAt: &past}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, expiredPlain); !errors.Is(err, auth.ErrInvalidKey) {
		t.Fatal("expired key must be rejected")
	}

	// The last admin key is protected.
	keys, _ := svc.List(ctx)
	var adminID string
	for _, k := range keys {
		if k.Role == auth.RoleAdmin {
			adminID = k.ID
		}
	}
	if err := svc.Delete(ctx, adminID); !errors.Is(err, auth.ErrLastAdmin) {
		t.Fatalf("deleting the last admin must fail, got %v", err)
	}
	viewer := auth.RoleViewer
	if _, err := svc.Update(ctx, adminID, auth.UpdateKeyInput{Role: &viewer}); !errors.Is(err, auth.ErrLastAdmin) {
		t.Fatalf("downgrading the last admin must fail, got %v", err)
	}
}
