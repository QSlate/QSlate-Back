//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
	"qslate/services/workspace/internal/domain"
	"qslate/services/workspace/internal/platform/postgres"
	"qslate/services/workspace/internal/service"
	"qslate/services/workspace/migrations"
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
	schema := fmt.Sprintf("workspace_test_%d", time.Now().UnixNano())
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
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := postgres.Open(context.Background(), parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestWidgetsLayoutDuplicationAndConcurrentRevision(t *testing.T) {
	db := testDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	cfg, err := service.LoadConfig("../../config/layout.json")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := service.New(New(db), cfg, testCatalogue{})
	if err != nil {
		t.Fatal(err)
	}
	d, err := svc.Create(ctx, 1, domain.Metadata{Title: "Lab", Tags: []string{"private"}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := svc.AddWidget(ctx, 1, d.ID, "chart", "version-1", json.RawMessage(`{"precision":9007199254740993,"symbol":"EURUSD"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.AddWidget(ctx, 1, d.ID, "orders", "version-2", nil)
	if err != nil {
		t.Fatal(err)
	}
	d, err = svc.Get(ctx, 1, d.ID)
	if err != nil || d.Revision != 3 || len(d.Widgets) != 2 || len(d.Layout) != 2 || d.Layout[1].X != 6 {
		t.Fatalf("widgets and default layout not atomic: %v", err)
	}
	if !strings.Contains(string(d.Widgets[0].Configuration), "9007199254740993") {
		t.Fatal("configuration integer precision lost")
	}
	// Every operation must hide resources belonging to another account.
	if _, err := svc.Duplicate(ctx, 2, d.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("cross-owner duplication")
	}
	if _, err := svc.AddWidget(ctx, 2, d.ID, "chart", "v1", nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("cross-owner add")
	}
	if _, err := svc.PatchWidget(ctx, 2, d.ID, a.InstanceID, json.RawMessage(`{}`)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("cross-owner patch")
	}
	if err := svc.DeleteWidget(ctx, 2, d.ID, a.InstanceID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("cross-owner remove")
	}
	if _, err := svc.SaveLayout(ctx, 2, d.ID, d.Revision, d.Layout); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("cross-owner layout")
	}
	if _, err := svc.ResetLayout(ctx, 2, d.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("cross-owner reset")
	}
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`[]`), json.RawMessage(`{"a":"` + strings.Repeat("x", 4096) + `"}`)} {
		if _, err := svc.PatchWidget(ctx, 1, d.ID, a.InstanceID, raw); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatal("invalid config accepted")
		}
	}
	for _, invalid := range [][]domain.LayoutItem{
		{}, {d.Layout[0]}, {d.Layout[0], d.Layout[0]},
		{{InstanceID: a.InstanceID, Width: 6, Height: 4}, {InstanceID: b.InstanceID, X: 1, Width: 6, Height: 4}},
		{{InstanceID: a.InstanceID, X: -1, Width: 6, Height: 4}, d.Layout[1]},
		{{InstanceID: a.InstanceID, X: int(^uint(0) >> 1), Width: 6, Height: 4}, d.Layout[1]},
		{{InstanceID: a.InstanceID, Width: 13, Height: 4}, d.Layout[1]},
		{{InstanceID: a.InstanceID, Y: 1000, Width: 6, Height: 4}, d.Layout[1]},
		{{InstanceID: strings.Repeat("z", 43), Width: 6, Height: 4}, d.Layout[1]},
	} {
		if _, err := svc.SaveLayout(ctx, 1, d.ID, d.Revision, invalid); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("invalid layout accepted: %v", err)
		}
		stored, err := svc.Get(ctx, 1, d.ID)
		if err != nil || !reflect.DeepEqual(stored, d) {
			t.Fatal("rejected layout partially persisted")
		}
	}
	// Two concurrent writes with the same expected revision: exactly one succeeds.
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, err := svc.SaveLayout(ctx, 1, d.ID, d.Revision, d.Layout); results <- err }()
	}
	success, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, domain.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatal("lost update: concurrent revisions were not serialized")
	}
	d, _ = svc.Get(ctx, 1, d.ID)
	copy, err := svc.Duplicate(ctx, 1, d.ID)
	if err != nil || copy.ID == d.ID || copy.Revision != 1 || len(copy.Widgets) != 2 || copy.Widgets[0].InstanceID == a.InstanceID || copy.Layout[0].InstanceID != copy.Widgets[0].InstanceID || copy.Layout[1].InstanceID != copy.Widgets[1].InstanceID || copy.Title != d.Title || !reflect.DeepEqual(copy.Widgets[0].Configuration, d.Widgets[0].Configuration) {
		t.Fatalf("duplicate is not an independent complete snapshot: %v", err)
	}
	patched, err := svc.PatchWidget(ctx, 1, copy.ID, copy.Widgets[0].InstanceID, json.RawMessage(`{"symbol":"BTCUSD"}`))
	if err != nil || patched.WidgetVersionID != a.WidgetVersionID {
		t.Fatal("configuration patch changed version")
	}
	original, _ := svc.Get(ctx, 1, d.ID)
	if !reflect.DeepEqual(original, d) {
		t.Fatal("mutating duplicate modified original")
	}
	if err := svc.DeleteWidget(ctx, 1, copy.ID, a.InstanceID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("instance from another dashboard accepted")
	}
	if err := svc.DeleteWidget(ctx, 1, copy.ID, copy.Widgets[0].InstanceID); err != nil {
		t.Fatal(err)
	}
	copy, _ = svc.Get(ctx, 1, copy.ID)
	if copy.Revision != 3 || len(copy.Widgets) != 1 || len(copy.Layout) != 1 {
		t.Fatal("widget deletion must atomically remove position")
	}
	copy.Layout[0].X, copy.Layout[0].Y = 2, 10
	copy, err = svc.SaveLayout(ctx, 1, copy.ID, copy.Revision, copy.Layout)
	if err != nil {
		t.Fatal(err)
	}
	// A different config affects reset without losing widget configuration or IDs.
	cfg.DefaultWidget.Width, cfg.DefaultWidget.Height = 3, 2
	custom, _ := service.New(New(db), cfg, testCatalogue{})
	reset, err := custom.ResetLayout(ctx, 1, copy.ID)
	if err != nil || reset.Revision != copy.Revision+1 || reset.Layout[0].X != 0 || reset.Layout[0].Y != 0 || reset.Layout[0].Width != 3 || reset.Layout[0].Height != 2 || !reflect.DeepEqual(reset.Widgets, copy.Widgets) {
		t.Fatal("configurable reset failed")
	}
	// A new service/repository instance reads exactly the durable state.
	fresh, _ := service.New(New(db), cfg, testCatalogue{})
	stored, err := fresh.Get(ctx, 1, copy.ID)
	if err != nil || !reflect.DeepEqual(stored, reset) {
		t.Fatal("state not durable")
	}
	if err := fresh.DeleteWidget(ctx, 1, copy.ID, copy.Widgets[0].InstanceID); err != nil {
		t.Fatal(err)
	}
	empty, _ := fresh.ResetLayout(ctx, 1, copy.ID)
	if empty.Widgets == nil || empty.Layout == nil || len(empty.Widgets) != 0 || len(empty.Layout) != 0 {
		t.Fatal("empty arrays must not be null")
	}
	// Capacity errors roll back the mutation, including its revision.
	cfg.MaxWidgets = 1
	limited, _ := service.New(New(db), cfg, testCatalogue{})
	if _, err := limited.AddWidget(ctx, 1, copy.ID, "first", "v1", nil); err != nil {
		t.Fatal(err)
	}
	before, _ := limited.Get(ctx, 1, copy.ID)
	if _, err := limited.AddWidget(ctx, 1, copy.ID, "second", "v1", nil); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("widget limit ignored")
	}
	after, _ := limited.Get(ctx, 1, copy.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("capacity rejection modified dashboard")
	}
}

func TestDashboardPersistenceOwnershipAndPagination(t *testing.T) {
	db := testDatabase(t)
	ctx := context.Background()
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
	cfg, err := service.LoadConfig("../../config/layout.json")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := service.New(New(db), cfg, testCatalogue{})
	if err != nil {
		t.Fatal(err)
	}
	a, err := svc.Create(ctx, 1, domain.Metadata{Title: "Alice", Description: "private", Tags: []string{"research"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Create(ctx, 1, domain.Metadata{Title: "Alice's second dashboard"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Create(ctx, 2, domain.Metadata{Title: "Bob"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Revision != 1 || a.CreatedAt.IsZero() || a.UpdatedAt.IsZero() {
		t.Fatal("database defaults missing")
	}
	first, err := svc.List(ctx, 1, "", 1)
	if err != nil || len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("first page invalid: %v", err)
	}
	second, err := svc.List(ctx, 1, first.NextCursor, 1)
	if err != nil || len(second.Items) != 1 || second.NextCursor != "" || first.Items[0].ID == second.Items[0].ID {
		t.Fatalf("keyset pagination invalid: %v", err)
	}
	for _, page := range []domain.Page{first, second} {
		if page.Items[0].OwnerID != 1 {
			t.Fatal("another owner's dashboard leaked")
		}
	}
	empty, err := svc.List(ctx, 3, "", 10)
	if err != nil || empty.Items == nil || len(empty.Items) != 0 {
		t.Fatal("empty list must encode as []")
	}
	if _, err = svc.Get(ctx, 2, a.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("cross-owner read accepted")
	}
	if _, err = svc.ReplaceMetadata(ctx, 2, a.ID, domain.Metadata{Title: "Stolen"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("cross-owner write accepted")
	}
	if err = svc.Delete(ctx, 2, a.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("cross-owner deletion accepted")
	}
	updated, err := svc.ReplaceMetadata(ctx, 1, a.ID, domain.Metadata{Title: "Owner's 'quoted' title"})
	if err != nil || updated.Revision != 2 || len(updated.Tags) != 0 || updated.Description != "" {
		t.Fatalf("metadata replacement invalid: %v", err)
	}
	stored, err := svc.Get(ctx, 1, a.ID)
	if err != nil || !reflect.DeepEqual(stored, updated) {
		t.Fatal("metadata not persisted")
	}
	if err = svc.Delete(ctx, 1, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Get(ctx, 1, a.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("deleted dashboard still present")
	}
	if err = svc.Delete(ctx, 1, a.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("missing dashboard deletion accepted")
	}
	if _, err = db.Exec(`UPDATE schema_migrations SET checksum='tampered'`); err != nil {
		t.Fatal(err)
	}
	if err = migrations.Apply(ctx, db); err == nil {
		t.Fatal("modified migration accepted")
	}
}
