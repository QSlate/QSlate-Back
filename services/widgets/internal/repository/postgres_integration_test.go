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
	"qslate/services/widgets/internal/domain"
	"qslate/services/widgets/internal/platform/postgres"
	"qslate/services/widgets/internal/service"
	"qslate/services/widgets/migrations"
	"reflect"
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
	schema := fmt.Sprintf("widgets_test_%d", time.Now().UnixNano())
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
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	q.Set("search_path", schema)
	parsed.RawQuery = q.Encode()
	db, err := postgres.Open(context.Background(), parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
func TestPrivateVersionedCatalogueAndInstalledImmutability(t *testing.T) {
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
	cfg, err := service.LoadConfig("../../config/catalogue.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Installed = []domain.Installed{{ID: "builtin-chart", VersionID: "builtin-chart-v1", Input: domain.Input{Name: "Installed chart", Language: "python", Source: "def render(data, parameters):\n    return data\n", Configuration: json.RawMessage(`{}`)}}}
	repo := New(db)
	svc, err := service.New(repo, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err = svc.Sync(ctx); err != nil {
		t.Fatal("idempotent config sync failed", err)
	}
	in := domain.Input{Name: "Alice chart", Language: "rust", Source: "fn main() {}", Configuration: json.RawMessage(`{"n":9007199254740993}`)}
	a, err := svc.Create(ctx, 1, in)
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.Create(ctx, 2, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Get(ctx, 2, a.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("cross-owner source read")
	}
	if _, err = svc.Replace(ctx, 2, a.ID, in); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("cross-owner replace")
	}
	if err = svc.Delete(ctx, 2, a.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("cross-owner delete")
	}
	for _, installed := range []bool{false, true} {
		page, err := svc.List(ctx, 1, installed, "", 1)
		if err != nil || len(page.Items) != 1 {
			t.Fatal("invalid first page", err)
		}
		all, err := svc.List(ctx, 1, installed, "", 100)
		want := 1
		if installed {
			want = 2
		}
		if err != nil || len(all.Items) != want {
			t.Fatalf("list scope incorrect: %v", err)
		}
		for _, item := range all.Items {
			if item.ID == b.ID {
				t.Fatal("another owner's definition leaked")
			}
		}
		if installed && page.NextCursor == "" {
			t.Fatal("missing cursor")
		}
		if installed {
			next, err := svc.List(ctx, 1, true, page.NextCursor, 1)
			if err != nil || len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID || next.NextCursor != "" {
				t.Fatal("invalid keyset pagination")
			}
		}
	}
	in.Source = "fn main() { println!(\"updated\"); }"
	updated, err := svc.Replace(ctx, 1, a.ID, in)
	if err != nil || updated.VersionID == a.VersionID {
		t.Fatal("update did not create immutable version", err)
	}
	if _, err = svc.Resolve(ctx, 1, a.ID, a.VersionID, false); err != nil {
		t.Fatal("old pinned version lost", err)
	}
	if _, err = svc.Resolve(ctx, 2, a.ID, a.VersionID, true); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("old version authorization bypass")
	}
	if _, err = svc.Resolve(ctx, 1, b.ID, a.VersionID, false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("mismatched definition/version accepted")
	}
	if _, err = db.ExecContext(ctx, `UPDATE widget_versions SET source='tampered' WHERE id=$1`, a.VersionID); err == nil {
		t.Fatal("database allowed mutation of immutable source")
	}
	fresh, _ := service.New(New(db), cfg, nil)
	stored, err := fresh.Get(ctx, 1, a.ID)
	if err != nil || !reflect.DeepEqual(stored, updated) {
		t.Fatal("versioned source not persisted", err)
	}
	changes := make(chan domain.Widget, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { w, e := svc.Replace(ctx, 1, a.ID, in); changes <- w; errs <- e }()
	}
	one, two := <-changes, <-changes
	if err = <-errs; err != nil {
		t.Fatal(err)
	}
	if err = <-errs; err != nil {
		t.Fatal(err)
	}
	if one.VersionID == two.VersionID {
		t.Fatal("concurrent versions collided")
	}
	for _, v := range []string{a.VersionID, updated.VersionID, one.VersionID, two.VersionID} {
		if _, err = svc.Resolve(ctx, 1, a.ID, v, false); err != nil {
			t.Fatal("concurrent version discarded", err)
		}
	}
	if err = svc.Delete(ctx, 1, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Get(ctx, 1, a.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("deleted definition still visible")
	}
	if _, err = svc.Resolve(ctx, 1, a.ID, a.VersionID, false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("deleted definition can be newly instantiated")
	}
	if _, err = svc.Resolve(ctx, 1, a.ID, a.VersionID, true); err != nil {
		t.Fatal("existing private instances lost retained version")
	}
	bad := cfg
	bad.Installed = append([]domain.Installed{}, cfg.Installed...)
	bad.Installed[0].Source = "modified without new version"
	broken, _ := service.New(repo, bad, nil)
	if err = broken.Sync(ctx); err == nil {
		t.Fatal("installed immutable version changed")
	}
	if _, err = svc.Resolve(ctx, 2, "builtin-chart", "builtin-chart-v1", false); err != nil {
		t.Fatal("failed config sync partially archived catalogue", err)
	}
	empty := cfg
	empty.Installed = nil
	disabled, _ := service.New(repo, empty, nil)
	if err = disabled.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Resolve(ctx, 1, "builtin-chart", "builtin-chart-v1", false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("uninstalled definition still active")
	}
	if _, err = svc.Resolve(ctx, 1, "builtin-chart", "builtin-chart-v1", true); err != nil {
		t.Fatal("uninstalled pinned version lost")
	}
	if _, err = db.ExecContext(ctx, `UPDATE schema_migrations SET checksum='tampered'`); err != nil {
		t.Fatal(err)
	}
	if err = migrations.Apply(ctx, db); err == nil {
		t.Fatal("migration checksum change accepted")
	}
}
