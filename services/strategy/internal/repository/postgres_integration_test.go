//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lib/pq"
	"net/url"
	"os"
	"qslate/services/strategy/internal/domain"
	"qslate/services/strategy/internal/platform/postgres"
	"qslate/services/strategy/internal/service"
	"qslate/services/strategy/migrations"
	"sync"
	"testing"
	"time"
)

func testDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL required (development database only)")
	}
	admin, err := postgres.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("strategy_test_%d", time.Now().UnixNano())
	if _, err = admin.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema)); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := postgres.Open(context.Background(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
func TestVersionPersistenceOwnershipAtomicRestoreAndConcurrentSave(t *testing.T) {
	db := testDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := migrations.Apply(ctx, db); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if t.Failed() {
		t.FailNow()
	}
	cfg, err := service.LoadConfig("../../config/strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	repo := New(db)
	svc, err := service.New(repo, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	in := domain.Input{Name: "Original", Source: "def run(data):\n    return data\n", Parameters: json.RawMessage(`{"n":9007199254740993}`)}
	first, err := svc.Create(ctx, 42, in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Create(ctx, 42, domain.Input{Name: "Second", Source: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []func() error{
		func() error { _, err := svc.Get(ctx, 43, first.ID); return err },
		func() error {
			_, err := svc.Save(ctx, 43, first.ID, domain.SaveInput{Input: in, BaseVersionID: first.VersionID})
			return err
		},
		func() error { return svc.Delete(ctx, 43, first.ID) },
		func() error { _, err := svc.Versions(ctx, 43, first.ID, "", 1); return err },
		func() error {
			_, err := svc.Restore(ctx, 43, first.ID, domain.RestoreInput{VersionID: first.VersionID, BaseVersionID: first.VersionID})
			return err
		},
	} {
		if err := call(); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("ownership bypass: %v", err)
		}
	}
	page, err := svc.List(ctx, 42, "", 1)
	if err != nil || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatal("missing pagination", err)
	}
	next, err := svc.List(ctx, 42, page.NextCursor, 1)
	if err != nil || len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID {
		t.Fatal("invalid cursor", err)
	}
	other, err := svc.List(ctx, 43, "", 100)
	if err != nil || len(other.Items) != 0 {
		t.Fatal("owner list leaked")
	}
	encoded, _ := json.Marshal(page)
	var payload map[string]any
	json.Unmarshal(encoded, &payload)
	if _, ok := payload["source"]; ok {
		t.Fatal("source in summaries")
	}
	newInput := domain.Input{Name: "Changed", Source: "return invalid_draft", Parameters: json.RawMessage(`{"n":3}`)}
	// Two writers use the same base version: exactly one commits, the other conflicts.
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Save(ctx, 42, first.ID, domain.SaveInput{Input: newInput, BaseVersionID: first.VersionID})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, domain.ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("concurrent edits silently overwrite")
	}
	current, err := svc.Get(ctx, 42, first.ID)
	if err != nil || current.Revision != 2 || current.VersionID == first.VersionID {
		t.Fatal("save failed", err)
	}
	_, err = svc.Restore(ctx, 42, first.ID, domain.RestoreInput{VersionID: second.VersionID, BaseVersionID: current.VersionID})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("version from another strategy accepted", err)
	}
	_, err = svc.Restore(ctx, 42, first.ID, domain.RestoreInput{VersionID: first.VersionID, BaseVersionID: first.VersionID})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatal("stale restore accepted")
	}
	restored, err := svc.Restore(ctx, 42, first.ID, domain.RestoreInput{VersionID: first.VersionID, BaseVersionID: current.VersionID})
	if err != nil || restored.Revision != 3 || restored.VersionID == first.VersionID || restored.Source != first.Source || restored.Name != first.Name {
		t.Fatal("restore rewrote old snapshot", err)
	}
	history, err := svc.Versions(ctx, 42, first.ID, "", 1)
	if err != nil || len(history.Items) != 1 || history.Items[0].RestoredFrom != first.VersionID || history.NextCursor != "3" {
		t.Fatal("bad history", err)
	}
	rest, err := svc.Versions(ctx, 42, first.ID, history.NextCursor, 20)
	if err != nil || len(rest.Items) != 2 || rest.Items[1].VersionID != first.VersionID || rest.Items[1].Source != first.Source {
		t.Fatal("history not retained", err)
	}
	empty, err := svc.Versions(ctx, 42, first.ID, "1", 1)
	if err != nil || len(empty.Items) != 0 {
		t.Fatal("empty owned page must not be 404", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE strategy_versions SET source='overwritten' WHERE id=$1`, first.VersionID); err == nil {
		t.Fatal("version mutation allowed")
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM strategy_versions WHERE id=$1`, first.VersionID); err == nil {
		t.Fatal("version deletion allowed")
	}
	newService, _ := service.New(New(db), cfg, nil)
	persisted, err := newService.Get(ctx, 42, first.ID)
	if err != nil || persisted.VersionID != restored.VersionID {
		t.Fatal("state lost on reconstruction")
	}
	if err = svc.Delete(ctx, 42, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Get(ctx, 42, first.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("archived strategy readable")
	}
	if _, err = svc.Versions(ctx, 42, first.ID, "", 1); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("archived history readable")
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM strategy_versions WHERE strategy_id=$1`, first.ID).Scan(&count); err != nil || count != 3 {
		t.Fatal("history destroyed", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE schema_migrations SET checksum='tampered'`); err != nil {
		t.Fatal(err)
	}
	if err = migrations.Apply(ctx, db); err == nil {
		t.Fatal("migration checksum not checked")
	}
}
