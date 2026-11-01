//go:build integration

package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
	"qslate/services/auth/internal/domain"
	"qslate/services/auth/internal/platform/postgres"
	"qslate/services/auth/migrations"
)

func testDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL is required for integration tests")
	}
	ctx := context.Background()
	admin, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("auth_test_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+pq.QuoteIdentifier(schema)); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+pq.QuoteIdentifier(schema)+" CASCADE")
		if err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := postgres.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrationsAndRepository(t *testing.T) {
	db := testDatabase(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for index := 0; index < 3; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := migrations.Apply(ctx, db); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var applied int
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&applied); err != nil || applied != 3 {
		t.Fatalf("migration count=%d err=%v", applied, err)
	}
	repo := New(db)
	user, err := repo.Create(ctx, "alice@example.com", "alice", "bcrypt-hash")
	if err != nil {
		t.Fatal(err)
	}
	if user.ID <= 0 || user.CreatedAt.IsZero() {
		t.Fatal("database defaults missing")
	}
	for _, identifier := range []string{"alice@example.com", "alice"} {
		got, err := repo.FindByIdentifier(ctx, identifier)
		if err != nil || got.ID != user.ID {
			t.Fatalf("lookup failed: %v", err)
		}
	}
	got, err := repo.FindByID(ctx, user.ID)
	if err != nil || got.Email != user.Email {
		t.Fatalf("id lookup failed: %v", err)
	}
	if _, err = repo.Create(ctx, "alice@example.com", "other", "hash"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("conflict not mapped: %v", err)
	}
	if _, err = repo.FindByIdentifier(ctx, "' OR 1=1 --"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("SQL parameterization failed")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = repo.FindByID(cancelled, user.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if _, err = db.Exec(`UPDATE schema_migrations SET checksum = 'tampered'`); err != nil {
		t.Fatal(err)
	}
	if err = migrations.Apply(ctx, db); err == nil {
		t.Fatal("migration checksum mismatch accepted")
	}
}
