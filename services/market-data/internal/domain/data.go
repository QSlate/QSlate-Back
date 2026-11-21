package domain

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrInvalid      = errors.New("invalid market data input")
	ErrNotFound     = errors.New("dataset not found")
	ErrConflict     = errors.New("dataset state conflict")
	ErrUnavailable  = errors.New("data dependency unavailable")
	ErrUnauthorized = errors.New("active session required")
	ErrQuota        = errors.New("dataset quota exceeded")
)

func ID() string {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func ValidID(s string) bool {
	b, e := base64.RawURLEncoding.DecodeString(s)
	return e == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == s
}

type Mapping struct {
	TimestampColumn string `json:"timestamp_column"`
	Timezone        string `json:"timezone"`
	Columns         struct {
		Open   string `json:"open"`
		High   string `json:"high"`
		Low    string `json:"low"`
		Close  string `json:"close"`
		Volume string `json:"volume,omitempty"`
	} `json:"columns"`
	TimestampUnit string `json:"timestamp_unit"`
}
type Dataset struct {
	ID        string     `json:"id"`
	VersionID string     `json:"version_id"`
	Name      string     `json:"name"`
	Status    string     `json:"status"`
	Format    string     `json:"format"`
	Columns   []string   `json:"columns"`
	Preview   [][]string `json:"preview"`
	RowCount  int        `json:"row_count"`
	Mapping   *Mapping   `json:"mapping,omitempty"`
	SHA256    string     `json:"sha256,omitempty"`
	Symbol    string     `json:"symbol"`
	Error     string     `json:"error,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}
type Page struct {
	Items      []Dataset `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
}
type Parsed struct {
	Columns  []string   `json:"columns"`
	Preview  [][]string `json:"preview"`
	RowCount int        `json:"row_count"`
	Rows     [][]string `json:"rows,omitempty"`
}
type Job struct {
	ID        string `json:"job_id"`
	DatasetID string `json:"dataset_id"`
	VersionID string `json:"version_id"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}
type Claim struct {
	Job
	Token   string
	Format  string
	Raw     []byte
	Mapping Mapping
}
type Candle struct {
	Time   time.Time `json:"time"`
	Open   string    `json:"open"`
	High   string    `json:"high"`
	Low    string    `json:"low"`
	Close  string    `json:"close"`
	Volume string    `json:"volume"`
}
type History struct {
	Symbol     string   `json:"symbol"`
	Resolution string   `json:"resolution"`
	Items      []Candle `json:"items"`
	NextCursor *string  `json:"next_cursor"`
}
type Snapshot struct {
	DatasetID string `json:"dataset_id"`
	VersionID string `json:"version_id"`
	CSV       string `json:"csv"`
	SHA256    string `json:"sha256"`
	RowCount  int    `json:"row_count"`
}
type Symbol struct {
	Symbol     string `json:"symbol"`
	Name       string `json:"name"`
	DatasetID  string `json:"dataset_id,omitempty"`
	VersionID  string `json:"version_id,omitempty"`
	Resolution string `json:"resolution,omitempty"`
	Live       bool   `json:"live"`
}
type Tick struct {
	ID       string    `json:"id"`
	Symbol   string    `json:"symbol"`
	Time     time.Time `json:"time"`
	Price    string    `json:"price"`
	Quantity string    `json:"quantity"`
}
type Event struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}
