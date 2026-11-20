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
	"qslate/services/market-data/internal/decoder"
	"qslate/services/market-data/internal/domain"
	"qslate/services/market-data/internal/platform/postgres"
	"qslate/services/market-data/internal/service"
	"qslate/services/market-data/migrations"
	"sync"
	"testing"
	"time"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	admin, e := postgres.Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := fmt.Sprintf("market_data_test_%d", time.Now().UnixNano())
	if _, e = admin.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema)); e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	db, e := postgres.Open(ctx, u.String())
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
func TestQueueImmutabilityHistoryOwnerAndDelete(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
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
		return
	}
	if os.Getenv("TEST_TIMESCALE") == "true" {
		if e := migrations.Timescale(ctx, db, true); e != nil {
			t.Fatal(e)
		}
	}
	r := New(db)
	parsed := domain.Parsed{Columns: []string{"time", "o", "h", "l", "c", "v"}, Preview: [][]string{{"2024-01-01T00:00:00Z", "10", "12", "9", "11", "5"}}, RowCount: 2}
	raw := []byte("original bytes")
	d, e := r.Create(ctx, 41, "Private", "csv", raw, parsed, 2, 10000)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.Get(ctx, 42, d.ID); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("owner leak")
	}
	var m domain.Mapping
	m.TimestampColumn = "time"
	m.Timezone = "UTC"
	m.TimestampUnit = "iso8601"
	m.Columns.Open = "o"
	m.Columns.High = "h"
	m.Columns.Low = "l"
	m.Columns.Close = "c"
	m.Columns.Volume = "v"
	initial := d.VersionID
	d, e = r.Map(ctx, 41, d.ID, initial, m, 5)
	if e != nil || d.VersionID == initial {
		t.Fatal(d, e)
	}
	if _, e = r.Map(ctx, 41, d.ID, initial, m, 5); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("stale mapping accepted")
	}
	job, e := r.Enqueue(ctx, 41, d.ID, "request-0000000001")
	if e != nil {
		t.Fatal(e)
	}
	again, e := r.Enqueue(ctx, 41, d.ID, "request-0000000001")
	if e != nil || again.ID != job.ID {
		t.Fatal("replay changed")
	}
	c, e := r.Claim(ctx, 8)
	if e != nil || string(c.Raw) != string(raw) {
		t.Fatal(c, e)
	}
	if _, e = r.Claim(ctx, 8); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("double claim")
	}
	rows := [][]string{{"2024-01-01T00:00:00Z", "10", "12", "9", "11", "5"}, {"2024-01-01T00:01:00Z", "11", "13", "10", "12", "6"}}
	if e = r.Finish(ctx, c, rows, "", 4194304); e != nil {
		t.Fatal(e)
	}
	snap, e := r.Snapshot(ctx, 41, d.ID, d.VersionID)
	if e != nil || snap.SHA256 == "" || snap.RowCount != 2 {
		t.Fatal(snap, e)
	}
	if _, e = r.Snapshot(ctx, 42, d.ID, d.VersionID); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("snapshot leak")
	}
	if _, e = db.Exec(`UPDATE md_versions SET csv='changed' WHERE id=$1`, d.VersionID); e == nil {
		t.Fatal("ready version mutable")
	}
	start, _ := time.Parse(time.RFC3339, "2024-01-01T00:00:00Z")
	history, e := r.History(ctx, 41, "custom:"+d.ID, "", "raw", start, start.Add(time.Hour), 0, 1, nil)
	if e != nil || len(history.Items) != 1 || history.NextCursor == nil {
		t.Fatal(history, e)
	}
	cursor, _ := time.Parse(time.RFC3339, *history.NextCursor)
	next, e := r.History(ctx, 41, "custom:"+d.ID, "", "raw", start, start.Add(time.Hour), 0, 1, &cursor)
	if e != nil || len(next.Items) != 1 || next.NextCursor != nil {
		t.Fatal(next, e)
	}
	agg, e := r.History(ctx, 41, "custom:"+d.ID, "", "1h", start, start.Add(time.Hour), 3600, 10, nil)
	if e != nil || len(agg.Items) != 1 {
		t.Fatal(agg, e)
	}
	d, e = r.Map(ctx, 41, d.ID, d.VersionID, m, 5)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.Snapshot(ctx, 41, d.ID, snap.VersionID); e != nil {
		t.Fatal("old ready version disappeared")
	}
	job, e = r.Enqueue(ctx, 41, d.ID, "request-0000000002")
	if e != nil {
		t.Fatal(e)
	}
	c, e = r.Claim(ctx, 8)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE md_jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, job.ID); e != nil {
		t.Fatal(e)
	}
	if e = r.Recover(ctx); e != nil {
		t.Fatal(e)
	}
	if e = r.Finish(ctx, c, rows, "", 4194304); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("stale worker accepted")
	}
	if e = r.Delete(ctx, 42, d.ID); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("foreign delete")
	}
	if e = r.Delete(ctx, 41, d.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Snapshot(ctx, 41, d.ID, snap.VersionID); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("deleted data retained")
	}
}
func TestRealParserAndTransactionalIndexing(t *testing.T) {
	origin := os.Getenv("TEST_PARSER_URL")
	if origin == "" {
		t.Skip("set TEST_PARSER_URL for real parser integration")
	}
	ctx := context.Background()
	db := testDB(t)
	if e := migrations.Apply(ctx, db); e != nil {
		t.Fatal(e)
	}
	cfg, e := service.LoadConfig("../../config/market-data.json")
	if e != nil {
		t.Fatal(e)
	}
	parser, e := decoder.New(origin, cfg)
	if e != nil {
		t.Fatal(e)
	}
	r := New(db)
	svc, e := service.New(r, parser, cfg)
	if e != nil {
		t.Fatal(e)
	}
	raw := []byte("date,o,h,l,c\n2024-01-01T00:00:00Z,10,12,9,11\n2024-01-01T00:01:00Z,11,13,10,12\n")
	d, e := svc.Upload(ctx, 41, "Real CSV", "csv", raw)
	if e != nil {
		t.Fatal(e)
	}
	var m domain.Mapping
	if e = json.Unmarshal([]byte(`{"timestamp_column":"date","timezone":"UTC","timestamp_unit":"iso8601","columns":{"open":"o","high":"h","low":"l","close":"c"}}`), &m); e != nil {
		t.Fatal(e)
	}
	d, e = svc.Map(ctx, 41, d.ID, d.VersionID, m)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = svc.Validate(ctx, 41, d.ID, "real-decoder-request-0001"); e != nil {
		t.Fatal(e)
	}
	c, e := r.Claim(ctx, 8)
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := parser.Parse(ctx, c.Format, c.Raw, &c.Mapping)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Finish(ctx, c, parsed.Rows, "", cfg.MaxSnapshotBytes); e != nil {
		t.Fatal(e)
	}
	snap, e := r.Snapshot(ctx, 41, d.ID, d.VersionID)
	if e != nil || snap.RowCount != 2 {
		t.Fatal(snap, e)
	}
}
