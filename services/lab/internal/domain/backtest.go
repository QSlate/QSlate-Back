package domain

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrInvalid      = errors.New("invalid Lab input")
	ErrNotFound     = errors.New("backtest or pinned resource not found")
	ErrConflict     = errors.New("operation conflicts with backtest state or idempotency key")
	ErrUnavailable  = errors.New("execution or pinned data unavailable")
	ErrUnauthorized = errors.New("active session required")
	ErrQuota        = errors.New("outstanding backtest limit reached")
)

func ID() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func ValidID(s string) bool {
	b, e := base64.RawURLEncoding.DecodeString(s)
	return e == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == s
}

type RunInput struct {
	StrategyID        string         `json:"strategy_id"`
	StrategyVersionID string         `json:"strategy_version_id"`
	DatasetID         string         `json:"dataset_id"`
	DatasetVersionID  string         `json:"dataset_version_id"`
	InitialCapital    string         `json:"initial_capital"`
	Parameters        map[string]any `json:"parameters,omitempty"`
	ExecutionTarget   string         `json:"execution_target,omitempty"`
	Window            int            `json:"window,omitempty"`
	CommissionRate    float64        `json:"commission_rate,omitempty"`
	SlippageBPS       float64        `json:"slippage_bps,omitempty"`
	ClassName         string         `json:"class_name,omitempty"`
}
type OptimizeInput struct {
	Run         RunInput             `json:"run"`
	Method      string               `json:"method"`
	SearchSpace map[string][]float64 `json:"search_space"`
	MaxRuns     int                  `json:"max_runs"`
	Seed        int64                `json:"seed,omitempty"`
}
type Job struct {
	EngineImage    string    `json:"engine_image,omitempty"`
	EngineRevision string    `json:"engine_revision"`
	StrategySHA256 string    `json:"strategy_sha256"`
	DatasetSHA256  string    `json:"dataset_sha256"`
	ID             string    `json:"backtest_id"`
	Status         string    `json:"status"`
	Progress       int       `json:"progress"`
	Input          RunInput  `json:"input"`
	OptimizationID string    `json:"optimization_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Error          string    `json:"error,omitempty"`
}

func (j Job) Terminal() bool {
	return j.Status == "completed" || j.Status == "failed" || j.Status == "cancelled"
}

type Page struct {
	Items      []Job  `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}
type Batch struct {
	OptimizationID string `json:"optimization_id,omitempty"`
	Seed           int64  `json:"seed,omitempty"`
	Backtests      []Job  `json:"backtests"`
}
type Snapshot struct {
	Source     string         `json:"source"`
	Parameters map[string]any `json:"configuration"`
	Language   string         `json:"language"`
}

// Frozen is private persisted input, never a response. No access token or host path.
type Frozen struct {
	Input          RunInput `json:"input"`
	Source         string   `json:"source"`
	CSV            string   `json:"csv"`
	SourceSHA256   string   `json:"source_sha256"`
	DatasetSHA256  string   `json:"dataset_sha256"`
	EngineRevision string   `json:"engine_revision"`
}
type Result struct {
	Status    string            `json:"status"`
	Summary   json.RawMessage   `json:"summary"`
	Trades    []json.RawMessage `json:"trades"`
	Equity    []json.RawMessage `json:"equity_curve"`
	Telemetry []json.RawMessage `json:"telemetry"`
	Logs      string            `json:"logs"`
	Error     string            `json:"error,omitempty"`
}
type Event struct {
	ID        int64           `json:"event_id"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
}
type Claim struct {
	ID, Token string
	Frozen    Frozen
}
