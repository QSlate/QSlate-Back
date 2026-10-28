package domain

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrInvalid      = errors.New("invalid strategy input")
	ErrNotFound     = errors.New("strategy or version not found")
	ErrConflict     = errors.New("strategy version changed")
	ErrUnauthorized = errors.New("authenticated owner required")
	ErrUnavailable  = errors.New("static analysis unavailable")
)

type Input struct {
	Name       string          `json:"name"`
	Source     string          `json:"source"`
	Parameters json.RawMessage `json:"parameters"`
}
type SaveInput struct {
	Input
	BaseVersionID string `json:"base_version_id"`
}
type RestoreInput struct {
	VersionID     string `json:"version_id"`
	BaseVersionID string `json:"base_version_id"`
}
type Summary struct {
	ID        string    `json:"id"`
	VersionID string    `json:"version_id"`
	Revision  int64     `json:"revision"`
	Name      string    `json:"name"`
	ReadOnly  bool      `json:"read_only,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type Strategy struct {
	Summary
	Source     string          `json:"source"`
	Parameters json.RawMessage `json:"parameters"`
}
type Version struct {
	VersionID string `json:"version_id"`
	Revision  int64  `json:"revision"`
	Input
	RestoredFrom string    `json:"restored_from,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}
type Page struct {
	Items      []Summary `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
}
type VersionPage struct {
	Items      []Version `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
}
type LintInput struct {
	Source   string `json:"source"`
	Language string `json:"language,omitempty"`
}
type Diagnostic struct {
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	EndLine   int    `json:"end_line"`
	EndColumn int    `json:"end_column"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
}
type Diagnostics struct {
	Valid       bool         `json:"valid"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}
type Analyzer interface {
	Check(context.Context, LintInput) (Diagnostics, error)
}
type Repository interface {
	Snapshot(context.Context, int64, string, string) (Input, error)
	Create(context.Context, int64, string, string, Input) (Strategy, error)
	List(context.Context, int64, string, int) (Page, error)
	Get(context.Context, int64, string) (Strategy, error)
	Save(context.Context, int64, string, string, SaveInput) (Strategy, error)
	Delete(context.Context, int64, string) error
	Versions(context.Context, int64, string, int64, int) (VersionPage, error)
	Restore(context.Context, int64, string, string, RestoreInput) (Strategy, error)
}
