package database_test

import (
	"context"
	"os"
	"testing"

	"github.com/user/whatsmeow-basileia/internal/infrastructure/database"
)

func TestMigrationsAreSortedAndUnique(t *testing.T) {
	ms, err := database.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 {
		t.Fatal("no migrations embedded")
	}
	seen := map[string]bool{}
	for i, m := range ms {
		if seen[m.Version] {
			t.Fatalf("duplicate migration %s", m.Version)
		}
		seen[m.Version] = true
		if i > 0 && ms[i-1].Version >= m.Version {
			t.Fatalf("migrations out of order: %s before %s", ms[i-1].Version, m.Version)
		}
	}
}

// TestMigrateUpgradesLegacySchema starts from the tables created by 0.0.7 and checks
// that migrating keeps the data and is idempotent.
func TestMigrateUpgradesLegacySchema(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	ctx := context.Background()
	db, err := database.Open(ctx, "postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, q := range []string{
		`DROP SCHEMA public CASCADE; CREATE SCHEMA public;`,
		`CREATE TABLE instances (id VARCHAR PRIMARY KEY, name VARCHAR NOT NULL, jid VARCHAR, webhook_url VARCHAR, proxy_uri VARCHAR)`,
		`ALTER TABLE instances ADD COLUMN api_key VARCHAR`,
		`CREATE TABLE message_stats (id SERIAL PRIMARY KEY, instance_id VARCHAR NOT NULL, timestamp TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, direction VARCHAR NOT NULL, count INT DEFAULT 1)`,
		`INSERT INTO instances VALUES ('paired', 'Paired', '5511999999999.0:1@s.whatsapp.net', 'http://hook', '', 'k1'), ('new', 'New', '', '', '', 'k2')`,
		`INSERT INTO message_stats (instance_id, direction) VALUES ('paired', 'in')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	applied, err := database.Migrate(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	all, _ := database.Migrations()
	if len(applied) != len(all) {
		t.Fatalf("expected %d migrations applied, got %v", len(all), applied)
	}
	again, err := database.Migrate(ctx, db)
	if err != nil || len(again) != 0 {
		t.Fatalf("second run must be a no-op, got %v %v", again, err)
	}

	var webhook, apiKey string
	var pairedAtSet bool
	if err := db.QueryRowContext(ctx, `SELECT webhook_url, api_key, paired_at IS NOT NULL FROM instances WHERE id = 'paired'`).Scan(&webhook, &apiKey, &pairedAtSet); err != nil {
		t.Fatal(err)
	}
	if webhook != "http://hook" || apiKey != "k1" || !pairedAtSet {
		t.Fatalf("legacy data must survive and paired instances get paired_at: %s %s %v", webhook, apiKey, pairedAtSet)
	}
	var newPaired bool
	_ = db.QueryRowContext(ctx, `SELECT paired_at IS NOT NULL FROM instances WHERE id = 'new'`).Scan(&newPaired)
	if newPaired {
		t.Fatal("unpaired instances must not get paired_at")
	}
	var stats int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM message_stats`).Scan(&stats)
	if stats != 1 {
		t.Fatal("message stats must be kept")
	}
}
