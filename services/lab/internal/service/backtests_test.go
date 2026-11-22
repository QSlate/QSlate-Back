package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"qslate/services/lab/internal/domain"
	"reflect"
	"strings"
	"testing"
	"time"
)

func config(t *testing.T) Config {
	t.Helper()
	c, e := LoadConfig("../../config/lab.json")
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func TestSearchBoundsAndDeterministicSampling(t *testing.T) {
	in := domain.OptimizeInput{Method: "grid_search", MaxRuns: 4, SearchSpace: map[string][]float64{"a": {1, 2}, "b": {3, 4}}}
	out, e := search(in, 25)
	if e != nil || len(out) != 4 {
		t.Fatalf("grid: %v %v", out, e)
	}
	in.MaxRuns = 3
	if _, e = search(in, 25); !errors.Is(e, domain.ErrInvalid) {
		t.Fatal("grid must not truncate")
	}
	in.Method = "monte_carlo"
	in.Seed = 42
	a, _ := search(in, 25)
	b, _ := search(in, 25)
	if !reflect.DeepEqual(a, b) || len(a) != 3 {
		t.Fatal("not deterministic")
	}
	in.SearchSpace["a"] = []float64{1, 1}
	if _, e = search(in, 25); e == nil {
		t.Fatal("duplicate values accepted")
	}
	in.MaxRuns = 26
	if _, e = search(in, 25); e == nil {
		t.Fatal("configured cap ignored")
	}
}
func TestDatasetPinOwnerIntegrityAndTraversal(t *testing.T) {
	c := config(t)
	c.DataRoot = t.TempDir()
	csv := "Datetime,Open,High,Low,Close\n2024-01-01T00:00:00Z,1,3,1,2\n2024-01-01T01:00:00Z,2,4,2,3\n"
	sum := sha256.Sum256([]byte(csv))
	entry := Dataset{ID: "test", VersionID: "v1", File: "data.csv", SHA256: hex.EncodeToString(sum[:]), OwnerID: 41}
	c.Datasets = []Dataset{entry}
	if e := os.WriteFile(filepath.Join(c.DataRoot, "data.csv"), []byte(csv), 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e := c.Dataset(41, "test", "v1", 1); e != nil {
		t.Fatal(e)
	}
	if _, _, e := c.Dataset(42, "test", "v1", 1); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("owner leak")
	}
	c.Datasets[0].SHA256 = hex.EncodeToString(make([]byte, 32))
	if _, _, e := c.Dataset(41, "test", "v1", 1); !errors.Is(e, domain.ErrUnavailable) {
		t.Fatal("mutable bytes accepted")
	}
	c.Datasets[0].File = "../escape.csv"
	if c.Validate() == nil {
		t.Fatal("traversal accepted")
	}
	c.Datasets = nil
	c.Execution.Runtime = "runc"
	if c.Validate() == nil {
		t.Fatal("unsafe runtime accepted")
	}
}

func TestLocalExecutionConfigurationRequiresExplicitGuard(t *testing.T) {
	t.Setenv("LAB_ALLOW_LOCAL_EXECUTION", "")
	if _, err := LoadConfig("../../config/lab.local.json"); err == nil {
		t.Fatal("local execution loaded without opt-in")
	}
	t.Setenv("LAB_ALLOW_LOCAL_EXECUTION", "true")
	cfg, err := LoadConfig("../../config/lab.local.json")
	if err != nil || !cfg.Execution.Enabled || cfg.Execution.Runtime != "runc" {
		t.Fatal("guarded local execution unavailable", err)
	}
}

func TestParameterDefinitionsValidateOverrides(t *testing.T) {
	minimum, maximum := 2.0, 200.0
	schema := []domain.ParameterDefinition{{Name: "window", Type: "integer", Default: json.Number("20"), Min: &minimum, Max: &maximum, Description: "Rolling window"}}
	if err := validateParameters(map[string]any{"window": json.Number("30")}, schema); err != nil {
		t.Fatal(err)
	}
	for _, values := range []map[string]any{{"window": json.Number("1")}, {"window": "20"}, {"unknown": json.Number("1")}} {
		if err := validateParameters(values, schema); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal("invalid override accepted")
		}
	}
}

type replayRepo struct {
	Repository
	prior domain.Batch
	seen  bool
}

func (r *replayRepo) Replay(context.Context, int64, string, string, string) (domain.Batch, bool, error) {
	r.seen = true
	return r.prior, true, nil
}
func TestIdempotentReplayNeedsNoAvailableStrategyDatasetOrWorker(t *testing.T) {
	expected := domain.Job{ID: domain.ID(), Status: "queued"}
	r := &replayRepo{prior: domain.Batch{Backtests: []domain.Job{expected}}}
	l, _ := New(r, nil, config(t))
	job, e := l.Run(context.Background(), 41, "unique-request-0001", domain.RunInput{})
	if e != nil || job.ID != expected.ID || !r.seen {
		t.Fatal("replay contacted unavailable dependencies")
	}
}
func TestValidationAndCanonicalKeys(t *testing.T) {
	in := domain.RunInput{StrategyID: domain.ID(), StrategyVersionID: domain.ID(), DatasetID: "prices", DatasetVersionID: "v1", InitialCapital: "10000"}
	if e := validate(&in); e != nil {
		t.Fatal(e)
	}
	in.ExecutionTarget = "local"
	if validate(&in) == nil {
		t.Fatal("unimplemented local target accepted")
	}
	in.ExecutionTarget = "cloud"
	for _, capital := range []string{"NaN", "0", "-1", "1e10", "1000000000001"} {
		in.InitialCapital = capital
		if validate(&in) == nil {
			t.Fatal(capital)
		}
	}
	for _, key := range []string{"short", "key with whitespace", "abcdefghijklmnop\n"} {
		if ValidKey(key) {
			t.Fatal("invalid key accepted")
		}
	}
	b, _ := json.Marshal(in)
	if hash(b) != hash(b) {
		t.Fatal("unstable hash")
	}
}

func TestPeriodIsBoundedAndFiltersFrozenCSV(t *testing.T) {
	start := time.Date(2024, 1, 1, 1, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	in := domain.RunInput{StrategyID: domain.ID(), StrategyVersionID: domain.ID(), DatasetID: "prices", DatasetVersionID: "v1", InitialCapital: "10000", Window: 1, PeriodStart: &start, PeriodEnd: &end}
	if err := validate(&in); err != nil {
		t.Fatal(err)
	}
	csv := "Datetime,Open,High,Low,Close,Volume\n2024-01-01T00:00:00Z,1,2,1,2,1\n2024-01-01T01:00:00Z,2,3,2,3,1\n2024-01-01T02:00:00Z,3,4,3,4,1\n2024-01-01T03:00:00Z,4,5,4,5,1\n"
	filtered, err := filterCSVPeriod(csv, start, end, 1, 10)
	if err != nil || strings.Contains(filtered, "T00:") || strings.Contains(filtered, "T03:") || !strings.Contains(filtered, "T01:") || !strings.Contains(filtered, "T02:") {
		t.Fatalf("unexpected filtered dataset: %q %v", filtered, err)
	}
	in.PeriodEnd = nil
	if validate(&in) == nil {
		t.Fatal("one-sided period accepted")
	}
	in.PeriodEnd = &end
	tooLong := start.Add(367 * 24 * time.Hour)
	in.PeriodEnd = &tooLong
	if validate(&in) == nil {
		t.Fatal("unbounded period accepted")
	}
}
