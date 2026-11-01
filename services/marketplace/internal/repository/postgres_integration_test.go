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
	"qslate/services/marketplace/internal/domain"
	"qslate/services/marketplace/internal/platform/postgres"
	"qslate/services/marketplace/internal/service"
	"qslate/services/marketplace/migrations"
	"strings"
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
	admin, e := postgres.Open(context.Background(), dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := fmt.Sprintf("marketplace_test_%d", time.Now().UnixNano())
	if _, e = admin.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema)); e != nil {
		admin.Close()
		t.Fatal(e)
	}
	u, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, e := postgres.Open(context.Background(), u.String())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		db.Close()
		if _, e := admin.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`); e != nil {
			t.Error(e)
		}
		admin.Close()
	})
	return db
}

type artifacts struct{}

func (artifacts) Snapshot(_ context.Context, _, _, version string) (domain.Snapshot, error) {
	return domain.Snapshot{Name: "Immutable", Language: "python", Source: "# " + version + "\nraise RuntimeError('never execute')", Configuration: json.RawMessage(`{"n":9007199254740993}`)}, nil
}
func TestPersistenceOwnershipPinsReviewsAndConcurrentWrites(t *testing.T) {
	db := testDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for n := 0; n < 3; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := migrations.Apply(ctx, db); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if t.Failed() {
		t.FailNow()
	}
	cfg, e := service.LoadConfig("../../config/marketplace.json")
	if e != nil {
		t.Fatal(e)
	}
	repo := New(db)
	svc, e := service.New(repo, artifacts{}, cfg)
	if e != nil {
		t.Fatal(e)
	}
	a, b, c := int64(41), int64(42), int64(43)
	v1, v2 := strings.Repeat("a", 43), strings.Repeat("b", 43)
	input := domain.PublishInput{Kind: "strategy", ResourceID: strings.Repeat("r", 43), VersionID: v1, Name: "Alpha", Description: "A searchable strategy", Category: "strategies"}
	item, e := svc.Publish(ctx, a, input)
	if e != nil {
		t.Fatal(e)
	}
	if item.Revision != 1 || item.AuthorID != a {
		t.Fatal("invalid listing")
	}
	if _, e = svc.Publish(ctx, a, input); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("duplicate publication accepted", e)
	}
	if _, e = svc.Patch(ctx, b, item.ID, domain.Patch{BaseRevision: 1, Name: &input.Name}); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("non-owner patch", e)
	}
	if e = svc.Delete(ctx, b, item.ID); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("non-owner delete", e)
	}
	if _, e = svc.Review(ctx, b, item.ID, domain.ReviewInput{Rating: 5}); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("review without installation", e)
	}
	// Concurrent duplicate installs retain one row and stable pin.
	for n := 0; n < 4; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := svc.Install(ctx, b, item.ID, v1); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if t.Failed() {
		t.FailNow()
	}
	installed, e := repo.Installed(ctx, b, item.ID, v1, false)
	if e != nil || !strings.Contains(installed.Source, v1) || installed.CatalogID != "market-"+item.ID {
		t.Fatal("missing snapshot", e)
	}
	if _, e = repo.Installed(ctx, c, item.ID, v1, true); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("cross-user entitlement", e)
	}
	r, e := svc.Review(ctx, b, item.ID, domain.ReviewInput{Rating: 5, Comment: "Useful"})
	if e != nil || r.AuthorID != b {
		t.Fatal(e)
	}
	if _, e = svc.Review(ctx, b, item.ID, domain.ReviewInput{Rating: 1}); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("duplicate review accepted", e)
	}
	if _, e = svc.Install(ctx, a, item.ID, v1); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.Review(ctx, a, item.ID, domain.ReviewInput{Rating: 5}); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("self review", e)
	}
	metrics, e := svc.Get(ctx, item.ID)
	if e != nil || metrics.InstallCount != 2 || metrics.ReviewCount != 1 || metrics.AverageRating != 5 {
		t.Fatal("bad metrics", metrics, e)
	}
	page, e := svc.List(ctx, domain.Filter{Query: "SEARCHABLE", Kind: "strategy", Category: "strategies"})
	if e != nil || len(page.Items) != 1 {
		t.Fatal("search", e)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), `"source"`) || strings.Contains(string(raw), `"resource_id"`) {
		t.Fatal("source/identifier leaked")
	}
	// Concurrent metadata changes use the same base: one wins, one conflicts.
	results := make(chan error, 2)
	name := "Updated"
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := svc.Patch(ctx, a, item.ID, domain.Patch{BaseRevision: 1, Name: &name})
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	ok, conflicts := 0, 0
	for e := range results {
		if e == nil {
			ok++
		} else if errors.Is(e, domain.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if ok != 1 || conflicts != 1 {
		t.Fatal("lost update")
	}
	updated, e := svc.Patch(ctx, a, item.ID, domain.Patch{BaseRevision: 2, VersionID: &v2})
	if e != nil || updated.VersionID != v2 {
		t.Fatal("version publication", e)
	}
	old, e := svc.Install(ctx, b, item.ID, v1)
	if e != nil || old.VersionID != v1 || old.Source != installed.Source {
		t.Fatal("install silently upgraded", e)
	}
	// Snapshot mutation is rejected both by the database and duplicate insertion.
	if _, e = db.ExecContext(ctx, `UPDATE market_versions SET snapshot='{}' WHERE item_id=$1`, item.ID); e == nil {
		t.Fatal("snapshot update accepted")
	}
	if _, e = db.ExecContext(ctx, `DELETE FROM market_versions WHERE item_id=$1`, item.ID); e == nil {
		t.Fatal("snapshot delete accepted")
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	e = insertSnapshot(ctx, tx, item.ID, v1, domain.Snapshot{Name: "Changed"})
	tx.Rollback()
	if !errors.Is(e, domain.ErrConflict) {
		t.Fatal("revision content overwritten", e)
	}
	unlisted := "unlisted"
	updated, e = svc.Patch(ctx, a, item.ID, domain.Patch{BaseRevision: updated.Revision, Visibility: &unlisted})
	if e != nil {
		t.Fatal(e)
	}
	page, e = svc.List(ctx, domain.Filter{})
	if e != nil || len(page.Items) != 0 {
		t.Fatal("unlisted item discovered", e)
	}
	if _, e = svc.Get(ctx, item.ID); e != nil {
		t.Fatal("unlisted link unavailable", e)
	}
	if e = svc.Delete(ctx, a, item.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.Get(ctx, item.ID); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("unpublished visible", e)
	}
	if _, e = svc.Install(ctx, c, item.ID, v1); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("new install after unpublish", e)
	}
	retained, e := svc.Install(ctx, b, item.ID, v1)
	if e != nil || retained.Source != installed.Source {
		t.Fatal("installed snapshot lost", e)
	}
	private, e := svc.Installations(ctx, b, "strategy", "", 1)
	if e != nil || len(private.Items) != 1 || private.Items[0].Source != "" {
		t.Fatal("installation summaries not bounded", e)
	}
	if e = svc.Uninstall(ctx, b, item.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = repo.Installed(ctx, b, item.ID, v1, false); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("new placement after uninstall", e)
	}
	if _, e = repo.Installed(ctx, b, item.ID, v1, true); e != nil {
		t.Fatal("existing pin lost", e)
	}
	if _, e = svc.Install(ctx, b, item.ID, v1); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("reinstall unpublished", e)
	}
	// Restarting the repository retains another account's installed content.
	if _, e = New(db).Installed(ctx, a, item.ID, v1, false); e != nil {
		t.Fatal("persistence", e)
	}
	empty, e := repo.Reviews(ctx, item.ID, "", 10)
	if !errors.Is(e, domain.ErrNotFound) || len(empty.Items) != 0 {
		t.Fatal("unpublished reviews leaked", e)
	}
	if _, e = db.ExecContext(ctx, `UPDATE schema_migrations SET checksum='tampered'`); e != nil {
		t.Fatal(e)
	}
	if e = migrations.Apply(ctx, db); e == nil {
		t.Fatal("checksum drift ignored")
	}
}
