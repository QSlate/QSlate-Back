package service

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"qslate/services/lab/internal/domain"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Repository interface {
	Replay(context.Context, int64, string, string, string) (domain.Batch, bool, error)
	Enqueue(context.Context, int64, string, string, string, string, domain.Batch, []domain.Frozen, int) (domain.Batch, error)
	Ready(context.Context, string) error
	Get(context.Context, int64, string) (domain.Job, error)
	List(context.Context, int64, string, int) (domain.Page, error)
	Cancel(context.Context, int64, string) (domain.Job, error)
	Delete(context.Context, int64, string) error
	Result(context.Context, int64, string) (domain.Result, error)
	Events(context.Context, int64, string, int64) ([]domain.Event, error)
}
type Strategies interface {
	Snapshot(context.Context, string, string) (domain.Snapshot, error)
}
type Datasets interface {
	Snapshot(context.Context, string, string) (string, string, error)
}
type Lab struct {
	Repo       Repository
	strategies Strategies
	Config     Config
	datasets   Datasets
}

func New(repo Repository, strategies Strategies, c Config, datasets ...Datasets) (*Lab, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	l := &Lab{Repo: repo, strategies: strategies, Config: c}
	if len(datasets) > 0 {
		l.datasets = datasets[0]
	}
	return l, nil
}
func hash(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func (c Config) ExecutionHash() string {
	b, _ := json.Marshal(struct {
		Execution ExecutionConfig
		Revision  string
	}{c.Execution, EngineRevision})
	return hash(b)
}

var ident = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
var decimal = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

func ValidKey(key string) bool {
	if len(key) < 16 || len(key) > 128 {
		return false
	}
	for _, c := range key {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}
func validate(in *domain.RunInput) error {
	for _, s := range []string{in.StrategyID, in.StrategyVersionID, in.DatasetID, in.DatasetVersionID} {
		if len(s) < 1 || len(s) > 128 || strings.TrimSpace(s) != s {
			return domain.ErrInvalid
		}
	}
	if !domain.ValidID(in.StrategyVersionID) || (!domain.ValidID(in.StrategyID) && !(strings.HasPrefix(in.StrategyID, "market-") && domain.ValidID(strings.TrimPrefix(in.StrategyID, "market-")))) {
		return domain.ErrInvalid
	}
	v, e := strconv.ParseFloat(in.InitialCapital, 64)
	if !decimal.MatchString(in.InitialCapital) || e != nil || math.IsInf(v, 0) || v <= 0 || v > 1e12 {
		return domain.ErrInvalid
	}
	if in.ExecutionTarget == "" {
		in.ExecutionTarget = "cloud"
	}
	if in.ExecutionTarget != "cloud" {
		return fmt.Errorf("%w: local runner belongs to section 12", domain.ErrInvalid)
	}
	if in.Window == 0 {
		in.Window = 20
	}
	if in.Window < 1 || in.Window > 10000 || in.CommissionRate < 0 || in.CommissionRate > 0.1 || in.SlippageBPS < 0 || in.SlippageBPS > 1000 || math.IsNaN(in.CommissionRate) || math.IsNaN(in.SlippageBPS) || math.IsInf(in.CommissionRate, 0) || math.IsInf(in.SlippageBPS, 0) || (in.ClassName != "" && !ident.MatchString(in.ClassName)) {
		return domain.ErrInvalid
	}
	b, e := json.Marshal(in.Parameters)
	if e != nil || len(b) > 4096 {
		return domain.ErrInvalid
	}
	for k := range in.Parameters {
		if !ident.MatchString(k) {
			return domain.ErrInvalid
		}
	}
	return nil
}
func (l *Lab) frozen(ctx context.Context, owner int64, in domain.RunInput) (domain.Frozen, error) {
	var out domain.Frozen
	if e := validate(&in); e != nil {
		return out, e
	}
	if !l.Config.Execution.Enabled {
		return out, domain.ErrUnavailable
	}
	if e := l.Repo.Ready(ctx, l.Config.ExecutionHash()); e != nil {
		return out, e
	}
	snapshot, e := l.strategies.Snapshot(ctx, in.StrategyID, in.StrategyVersionID)
	if e != nil {
		return out, e
	}
	params := map[string]any{}
	for k, v := range snapshot.Parameters {
		params[k] = v
	}
	for k, v := range in.Parameters {
		params[k] = v
	}
	in.Parameters = params
	if e = validate(&in); e != nil {
		return out, e
	}
	var csv, sum string
	if l.datasets != nil {
		csv, sum, e = l.datasets.Snapshot(ctx, in.DatasetID, in.DatasetVersionID)
		if e == nil {
			e = validateCSV(csv, in.Window, l.Config.MaxDatasetRows)
		}
	}
	if csv == "" {
		csv, sum, e = l.Config.Dataset(owner, in.DatasetID, in.DatasetVersionID, in.Window)
	}
	if e != nil {
		return out, e
	}
	return domain.Frozen{Input: in, Source: snapshot.Source, CSV: csv, SourceSHA256: hash([]byte(snapshot.Source)), DatasetSHA256: sum, EngineRevision: EngineRevision}, nil
}
func (l *Lab) Run(ctx context.Context, owner int64, key string, in domain.RunInput) (domain.Job, error) {
	if owner <= 0 {
		return domain.Job{}, domain.ErrUnauthorized
	}
	if !ValidKey(key) {
		return domain.Job{}, domain.ErrInvalid
	}
	b, e := json.Marshal(in)
	if e != nil {
		return domain.Job{}, domain.ErrInvalid
	}
	h := hash(b)
	prior, found, e := l.Repo.Replay(ctx, owner, "run", key, h)
	if e != nil {
		return domain.Job{}, e
	}
	if found {
		return prior.Backtests[0], nil
	}
	f, e := l.frozen(ctx, owner, in)
	if e != nil {
		return domain.Job{}, e
	}
	out, e := l.Repo.Enqueue(ctx, owner, "run", key, h, l.Config.ExecutionHash(), domain.Batch{}, []domain.Frozen{f}, l.Config.MaxOutstandingRuns)
	if e != nil {
		return domain.Job{}, e
	}
	return out.Backtests[0], nil
}

// Search creates bounded, unique trials. The same seed and search space yield the same order.
func search(in domain.OptimizeInput, cap int) ([]map[string]any, error) {
	if in.MaxRuns < 1 || in.MaxRuns > cap || len(in.SearchSpace) < 1 || len(in.SearchSpace) > 8 || (in.Method != "grid_search" && in.Method != "monte_carlo") {
		return nil, domain.ErrInvalid
	}
	keys := make([]string, 0, len(in.SearchSpace))
	total := 1
	for k, values := range in.SearchSpace {
		if !ident.MatchString(k) || len(values) < 1 || len(values) > 100 {
			return nil, domain.ErrInvalid
		}
		seen := map[float64]bool{}
		for _, v := range values {
			if math.IsNaN(v) || math.IsInf(v, 0) || seen[v] {
				return nil, domain.ErrInvalid
			}
			seen[v] = true
		}
		if total > 10000/len(values) {
			return nil, domain.ErrInvalid
		}
		total *= len(values)
		keys = append(keys, k)
	}
	sort.Strings(keys)
	count := total
	if in.Method == "grid_search" && total > in.MaxRuns {
		return nil, domain.ErrInvalid
	}
	if count > in.MaxRuns {
		count = in.MaxRuns
	}
	order := make([]int, total)
	for i := range order {
		order[i] = i
	}
	if in.Method == "monte_carlo" {
		rng := rand.New(rand.NewSource(in.Seed))
		rng.Shuffle(total, func(i, j int) { order[i], order[j] = order[j], order[i] })
	}
	out := make([]map[string]any, 0, count)
	for _, idx := range order[:count] {
		p := map[string]any{}
		for k, v := range in.Run.Parameters {
			p[k] = v
		}
		for _, k := range keys {
			values := in.SearchSpace[k]
			p[k] = values[idx%len(values)]
			idx /= len(values)
		}
		out = append(out, p)
	}
	return out, nil
}
func (l *Lab) Optimize(ctx context.Context, owner int64, key string, in domain.OptimizeInput) (domain.Batch, error) {
	var out domain.Batch
	if owner <= 0 {
		return out, domain.ErrUnauthorized
	}
	if !ValidKey(key) {
		return out, domain.ErrInvalid
	}
	b, e := json.Marshal(in)
	if e != nil {
		return out, domain.ErrInvalid
	}
	h := hash(b)
	prior, found, e := l.Repo.Replay(ctx, owner, "optimize", key, h)
	if e != nil || found {
		return prior, e
	}
	if in.Seed == 0 {
		n, e := cryptorand.Int(cryptorand.Reader, big.NewInt(math.MaxInt64))
		if e != nil {
			return out, e
		}
		in.Seed = n.Int64() + 1
	}
	params, e := search(in, l.Config.MaxOptimizationRuns)
	if e != nil {
		return out, e
	}
	f, e := l.frozen(ctx, owner, in.Run)
	if e != nil {
		return out, e
	}
	trials := make([]domain.Frozen, 0, len(params))
	for _, overrides := range params {
		trial := f
		trial.Input.Parameters = map[string]any{}
		for k, v := range f.Input.Parameters {
			trial.Input.Parameters[k] = v
		}
		for k, v := range overrides {
			trial.Input.Parameters[k] = v
		}
		if e = validate(&trial.Input); e != nil {
			return out, e
		}
		trials = append(trials, trial)
	}
	out.OptimizationID = domain.ID()
	out.Seed = in.Seed
	return l.Repo.Enqueue(ctx, owner, "optimize", key, h, l.Config.ExecutionHash(), out, trials, l.Config.MaxOutstandingRuns)
}
func (l *Lab) Summary(ctx context.Context, owner int64, id string) (map[string]any, error) {
	j, e := l.Repo.Get(ctx, owner, id)
	if e != nil {
		return nil, e
	}
	if j.Status != "completed" {
		return nil, domain.ErrConflict
	}
	result, e := l.Repo.Result(ctx, owner, id)
	if e != nil {
		return nil, e
	}
	var summary struct {
		Metrics map[string]any `json:"metrics"`
	}
	if e = json.Unmarshal(result.Summary, &summary); e != nil {
		return nil, e
	}
	return map[string]any{"backtest_id": id, "metrics": summary.Metrics, "strategy_version_id": j.Input.StrategyVersionID, "dataset_version_id": j.Input.DatasetVersionID}, nil
}
func (l *Lab) Compare(ctx context.Context, owner int64, ids []string) ([]map[string]any, error) {
	if len(ids) < 2 || len(ids) > 20 {
		return nil, domain.ErrInvalid
	}
	seen := map[string]bool{}
	out := []map[string]any{}
	for _, id := range ids {
		if !domain.ValidID(id) || seen[id] {
			return nil, domain.ErrInvalid
		}
		seen[id] = true
		v, e := l.Summary(ctx, owner, id)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
