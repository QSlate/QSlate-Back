package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"qslate/services/strategy/internal/domain"
	"strings"
	"testing"
)

type repositoryStub struct {
	domain.Repository
	input domain.Input
	calls int
}

func (r *repositoryStub) Create(_ context.Context, uid int64, sid, vid string, in domain.Input) (domain.Strategy, error) {
	r.calls++
	r.input = in
	return domain.Strategy{Summary: domain.Summary{ID: sid, VersionID: vid, Name: in.Name, Revision: 1}, Source: in.Source, Parameters: in.Parameters}, nil
}
func testConfig(t *testing.T) Config {
	t.Helper()
	c, err := LoadConfig("../../config/strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestValidationAndNoImplicitExecution(t *testing.T) {
	cfg := testConfig(t)
	repo := &repositoryStub{}
	svc, err := New(repo, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Invalid Python may be saved while editing. Lint is an independent operation.
	in := domain.Input{Name: " Draft ", Source: "def incomplete(", Parameters: json.RawMessage(`{"n":9007199254740993}`)}
	s, err := svc.Create(context.Background(), 42, in)
	if err != nil || s.Name != "Draft" || !idPattern.MatchString(s.ID) || !idPattern.MatchString(s.VersionID) || !strings.Contains(string(s.Parameters), "9007199254740993") {
		t.Fatalf("invalid create: %v", err)
	}
	for _, change := range []func(*domain.Input){func(v *domain.Input) { v.Name = " " }, func(v *domain.Input) { v.Name = "x\n" + strings.Repeat("x", 151) }, func(v *domain.Input) { v.Source = "" }, func(v *domain.Input) { v.Source = "\xff" }, func(v *domain.Input) { v.Source = "x\x00" }, func(v *domain.Input) { v.Source = strings.Repeat("x", cfg.MaxSourceBytes+1) }, func(v *domain.Input) { v.Parameters = json.RawMessage(`null`) }, func(v *domain.Input) { v.Parameters = json.RawMessage(`{"n":1e99999999}`) }, func(v *domain.Input) { v.Parameters = json.RawMessage(`{"x":"\u0000"}`) }} {
		v := in
		change(&v)
		if _, err := svc.Create(context.Background(), 42, v); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("invalid input accepted: %v", err)
		}
	}
	if repo.calls != 1 {
		t.Fatal("invalid input reached database")
	}
	if _, err := svc.Create(context.Background(), 0, in); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatal(err)
	}
	if _, err := svc.Save(context.Background(), 42, s.ID, domain.SaveInput{Input: in}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("missing optimistic version accepted")
	}
	if _, err := svc.Restore(context.Background(), 42, s.ID, domain.RestoreInput{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("missing restore version accepted")
	}
	if _, err := svc.List(context.Background(), 42, "invalid", 1); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("invalid cursor")
	}
	for _, cursor := range []string{"-1", "01", "x", "0"} {
		if _, err := svc.Versions(context.Background(), 42, s.ID, cursor, 1); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal("invalid version cursor")
		}
	}
	if _, err := svc.Versions(context.Background(), 42, s.ID, "", cfg.MaxVersionPageSize+1); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("unbounded history")
	}
	if _, err := svc.Lint(context.Background(), 42, domain.LintInput{Source: ""}); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("missing analyzer must not claim success")
	}
	if _, err := svc.Lint(context.Background(), 42, domain.LintInput{Language: "rust"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("unsupported language accepted")
	}
}
func TestStrictFileConfiguration(t *testing.T) {
	if _, err := New(nil, testConfig(t), nil); err == nil {
		t.Fatal("nil repository accepted")
	}
	for _, body := range []string{"null", "{} {}", `{"unknown":1}`, strings.Repeat("x", 65537)} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil {
			t.Fatal("malformed config accepted")
		}
	}
	c := testConfig(t)
	c.MaxBodyBytes = 16384
	if c.Validate() == nil {
		t.Fatal("source cannot fit configured request cap")
	}
}
