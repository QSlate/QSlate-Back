//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lib/pq"
	"net/url"
	"os"
	"path/filepath"
	"qslate/services/lab/internal/domain"
	"qslate/services/lab/internal/platform/postgres"
	"qslate/services/lab/internal/service"
	"qslate/services/lab/migrations"
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
	schema := fmt.Sprintf("lab_test_%d", time.Now().UnixNano())
	if _, e = admin.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema)); e != nil {
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
		_, e := admin.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`)
		if e != nil {
			t.Error(e)
		}
		admin.Close()
	})
	return db
}

type strategySnapshot struct{}

func (strategySnapshot) Snapshot(context.Context, string, string) (domain.Snapshot, error) {
	return domain.Snapshot{Source: "from simulator import Strategy\nclass S(Strategy): pass", Language: "python", Parameters: map[string]any{"period": 20}}, nil
}

func TestAtomicOptimizationQuotaAndConcurrentIdempotency(t *testing.T) {
	db := testDatabase(t)
	ctx := context.Background()
	if e := migrations.Apply(ctx, db); e != nil {
		t.Fatal(e)
	}
	r := New(db)
	cfg, e := service.LoadConfig("../../config/lab.json")
	if e != nil {
		t.Fatal(e)
	}
	cfg.Execution.Enabled = true
	cfg.DataRoot = t.TempDir()
	cfg.MaxOptimizationRuns = 4
	cfg.MaxOutstandingRuns = 4
	csv := "Datetime,Open,High,Low,Close\n2024-01-01T00:00:00Z,1,3,1,2\n2024-01-01T01:00:00Z,2,4,2,3\n"
	sum := sha256.Sum256([]byte(csv))
	cfg.Datasets = []service.Dataset{{ID: "prices", VersionID: "v1", File: "data.csv", SHA256: hex.EncodeToString(sum[:])}}
	if e = os.WriteFile(filepath.Join(cfg.DataRoot, "data.csv"), []byte(csv), 0600); e != nil {
		t.Fatal(e)
	}
	if e = r.Heartbeat(ctx, "worker", cfg.ExecutionHash(), true); e != nil {
		t.Fatal(e)
	}
	svc, e := service.New(r, strategySnapshot{}, cfg)
	if e != nil {
		t.Fatal(e)
	}
	input := domain.RunInput{StrategyID: domain.ID(), StrategyVersionID: domain.ID(), DatasetID: "prices", DatasetVersionID: "v1", InitialCapital: "10000", Window: 1, Parameters: map[string]any{"period": 30}}
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, e := svc.Run(ctx, 41, "concurrent-request-0001", input)
			if e != nil {
				t.Error(e)
				return
			}
			ids <- job.ID
		}()
	}
	wg.Wait()
	close(ids)
	unique := map[string]bool{}
	for id := range ids {
		unique[id] = true
	}
	if len(unique) != 1 {
		t.Fatalf("duplicate concurrent execution: %v", unique)
	}
	run, e := r.Claim(ctx, cfg.ExecutionHash(), 10, "sha256:trusted-test")
	if e != nil || run.Frozen.Input.Parameters["period"] != json.Number("30") || !strings.Contains(run.Frozen.Source, "class S") {
		t.Fatal(run, e)
	}
	batch, e := svc.Optimize(ctx, 42, "optimization-request-0001", domain.OptimizeInput{Run: input, Method: "grid_search", SearchSpace: map[string][]float64{"period": {10, 20, 30, 40}}, MaxRuns: 4})
	if e != nil || len(batch.Backtests) != 4 || batch.OptimizationID == "" {
		t.Fatal(batch, e)
	}
	if _, e = svc.Run(ctx, 42, "over-quota-request-0001", input); !errors.Is(e, domain.ErrQuota) {
		t.Fatal("quota bypassed", e)
	}
	replay, e := svc.Optimize(ctx, 42, "optimization-request-0001", domain.OptimizeInput{Run: input, Method: "grid_search", SearchSpace: map[string][]float64{"period": {10, 20, 30, 40}}, MaxRuns: 4})
	if e != nil || replay.OptimizationID != batch.OptimizationID {
		t.Fatal("batch replay changed", e)
	}
	input.InitialCapital = "20000"
	if _, e = svc.Run(ctx, 41, "concurrent-request-0001", input); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("changed replay payload accepted", e)
	}
}
func TestDurableQueueOwnerIdempotencyFencingRecoveryAndArtifacts(t *testing.T) {
	ctx := context.Background()
	db := testDatabase(t)
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
	r := New(db)
	f := domain.Frozen{Input: domain.RunInput{InitialCapital: "10000", StrategyVersionID: domain.ID(), DatasetVersionID: "v1"}, Source: "# frozen", CSV: "frozen bytes", SourceSHA256: "source", DatasetSHA256: "dataset", EngineRevision: service.EngineRevision}
	enqueue := func(key string) domain.Job {
		t.Helper()
		b, e := r.Enqueue(ctx, 41, "run", key, key, "hash", domain.Batch{}, []domain.Frozen{f}, 50)
		if e != nil {
			t.Fatal(e)
		}
		return b.Backtests[0]
	}
	job := enqueue("first")
	again := enqueue("first")
	if job.ID != again.ID {
		t.Fatal("duplicate run")
	}
	if _, _, e := r.Replay(ctx, 41, "run", "first", "different"); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("idempotency conflict ignored")
	}
	if _, e := r.Get(ctx, 42, job.ID); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("owner leak")
	}
	page, e := r.List(ctx, 41, "", 1)
	if e != nil || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	if e = r.Ready(ctx, "hash"); !errors.Is(e, domain.ErrUnavailable) {
		t.Fatal("missing worker accepted")
	}
	if e = r.Heartbeat(ctx, "worker", "hash", true); e != nil {
		t.Fatal(e)
	}
	if e = r.Ready(ctx, "hash"); e != nil {
		t.Fatal(e)
	}
	claim, e := r.Claim(ctx, "hash", 2, "sha256:test")
	if e != nil || claim.ID != job.ID || claim.Frozen.CSV != f.CSV {
		t.Fatal(claim, e)
	}
	if _, e = r.Claim(ctx, "hash", 2, "sha256:test"); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("double claim")
	}
	if e = r.Progress(ctx, claim, 42); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Cancel(ctx, 41, job.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Cancel(ctx, 41, job.ID); e != nil {
		t.Fatal("cancel not idempotent")
	}
	if e = r.Delete(ctx, 41, job.ID); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("deleted before cleanup")
	}
	result := domain.Result{Status: "completed", Summary: json.RawMessage(`{"metrics":{"return_pct":12.3}}`), Trades: []json.RawMessage{json.RawMessage(`{"pnl_usd":123}`)}, Equity: []json.RawMessage{}, Telemetry: []json.RawMessage{json.RawMessage(`{"price":12}`)}, Logs: "actual captured logs"}
	if e = r.Finish(ctx, claim, result); e != nil {
		t.Fatal(e)
	}
	cancelled, _ := r.Get(ctx, 41, job.ID)
	if cancelled.Status != "cancelled" {
		t.Fatal("stale result overwrote cancellation")
	}
	if e = r.Release(ctx, claim); e != nil {
		t.Fatal(e)
	}
	if e = r.Delete(ctx, 41, job.ID); e != nil {
		t.Fatal(e)
	}
	completed := enqueue("completed")
	claim, e = r.Claim(ctx, "hash", 2, "sha256:test")
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Finish(ctx, claim, result); e != nil {
		t.Fatal(e)
	}
	if e = r.Release(ctx, claim); e != nil {
		t.Fatal(e)
	}
	artifacts, e := r.Result(ctx, 41, completed.ID)
	if e != nil || artifacts.Logs != result.Logs {
		t.Fatal(artifacts, e)
	}
	if _, e = r.Result(ctx, 42, completed.ID); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("artifact owner leak")
	}
	events, e := r.Events(ctx, 41, completed.ID, 0)
	if e != nil || len(events) != 5 || events[len(events)-1].Type != "status" {
		t.Fatal(events, e)
	}
	resumed, e := r.Events(ctx, 41, completed.ID, events[len(events)-1].ID)
	if e != nil || len(resumed) != 0 {
		t.Fatal("duplicate replay")
	}
	interrupted := enqueue("interrupted")
	claim, e = r.Claim(ctx, "hash", 2, "sha256:test")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE lab_runs SET lease_until=now()-interval '1 second' WHERE id=$1`, interrupted.ID); e != nil {
		t.Fatal(e)
	}
	expired, e := r.Expired(ctx)
	if e != nil || len(expired) != 1 {
		t.Fatal(expired, e)
	}
	if e = r.Recover(ctx, claim); e != nil {
		t.Fatal(e)
	}
	j, _ := r.Get(ctx, 41, interrupted.ID)
	if j.Status != "failed" {
		t.Fatal("lost worker not recovered")
	}
	if e = r.Finish(ctx, claim, result); e != nil {
		t.Fatal(e)
	}
	j, _ = r.Get(ctx, 41, interrupted.ID)
	if j.Status != "failed" {
		t.Fatal("late worker result accepted")
	}
	enqueue("page-a")
	enqueue("page-b")
	page, e = r.List(ctx, 41, "", 1)
	if e != nil || page.NextCursor == "" {
		t.Fatal(page, e)
	}
	next, e := r.List(ctx, 41, page.NextCursor, 1)
	if e != nil || len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID {
		t.Fatal(next, e)
	}
}
