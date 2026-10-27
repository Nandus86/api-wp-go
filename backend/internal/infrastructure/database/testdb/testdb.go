// Package testdb gives integration tests a migrated, empty Postgres database.
// Tests are skipped unless TEST_DATABASE_URL points to a database they may wipe.
package testdb

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/user/whatsmeow-basileia/internal/infrastructure/database"
)

// Open migrates TEST_DATABASE_URL after dropping every table, so each test package
// starts from a clean schema. Never point it at a real database.
func Open(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	ctx := context.Background()
	db, err := database.Open(ctx, "postgres", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if _, err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// Exec runs a statement or fails the test.
func Exec(t *testing.T, db *sql.DB, q string, args ...interface{}) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}
