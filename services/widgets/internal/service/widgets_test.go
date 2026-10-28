package service

import (
	"context"
	"encoding/json"
	"errors"
	"qslate/services/widgets/internal/domain"
	"strings"
	"testing"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	c, err := LoadConfig("../../config/catalogue.json")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

type repoStub struct {
	domain.Repository
	widget domain.Widget
}

func (r *repoStub) Create(_ context.Context, uid int64, wid, version string, in domain.Input) (domain.Widget, error) {
	r.widget = domain.Widget{Summary: domain.Summary{ID: wid, VersionID: version, OwnerID: uid, Kind: "custom", Name: in.Name, Language: in.Language}, Source: in.Source, Configuration: in.Configuration}
	return r.widget, nil
}
func (r *repoStub) Get(_ context.Context, uid int64, wid string) (domain.Widget, error) {
	if uid != r.widget.OwnerID || wid != r.widget.ID {
		return domain.Widget{}, domain.ErrNotFound
	}
	return r.widget, nil
}

type executor struct{ request domain.PreviewRequest }

func (e *executor) Submit(_ context.Context, in domain.PreviewRequest) (domain.Job, error) {
	e.request = in
	return domain.Job{JobID: in.JobID, Status: "queued"}, nil
}
func TestInputValidationAndImmutablePreviewSnapshot(t *testing.T) {
	cfg := testConfig(t)
	r := &repoStub{}
	exec := &executor{}
	svc, err := New(r, cfg, exec)
	if err != nil {
		t.Fatal(err)
	}
	valid := domain.Input{Name: " Chart ", Language: "python", Source: "def render(data, parameters):\n    return data\n", Configuration: json.RawMessage(`{"large":9007199254740993}`)}
	w, err := svc.Create(context.Background(), 42, valid)
	if err != nil || w.Name != "Chart" || !referencePattern.MatchString(w.ID) || len(w.VersionID) != 43 || !strings.Contains(string(w.Configuration), "9007199254740993") {
		t.Fatalf("invalid creation: %v", err)
	}
	for i := 0; i < 200; i++ {
		value, err := id()
		if err != nil || !referencePattern.MatchString(value) || len(value) != 43 {
			t.Fatal("generated incompatible identifier")
		}
	}
	for _, change := range []func(*domain.Input){func(v *domain.Input) { v.Name = " " }, func(v *domain.Input) { v.Language = "go" }, func(v *domain.Input) { v.Source = "" }, func(v *domain.Input) { v.Source = "\x00" }, func(v *domain.Input) { v.Source = strings.Repeat("a", cfg.MaxSourceBytes+1) }, func(v *domain.Input) { v.Configuration = json.RawMessage(`null`) }, func(v *domain.Input) { v.Configuration = json.RawMessage(`{"a":"\u0000"}`) }, func(v *domain.Input) { v.Configuration = json.RawMessage(`{"n":1e999999999}`) }} {
		in := valid
		change(&in)
		if _, err := svc.Create(context.Background(), 42, in); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal("invalid definition accepted")
		}
	}
	input := domain.PreviewInput{DatasetID: "private-dataset", Parameters: json.RawMessage(`{"bars":20}`)}
	first, err := svc.Preview(context.Background(), 42, w.ID, "request-key-0001", input)
	if err != nil || first.Status != "queued" || exec.request.VersionID != w.VersionID || exec.request.Source != w.Source || exec.request.OwnerID != 42 {
		t.Fatalf("snapshot not pinned: %v", err)
	}
	second, _ := svc.Preview(context.Background(), 42, w.ID, "request-key-0001", input)
	if first.JobID != second.JobID {
		t.Fatal("same idempotency request changed identity")
	}
	if _, err := svc.Preview(context.Background(), 43, w.ID, "request-key-0001", input); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("preview source leaked")
	}
	disabled, _ := New(r, cfg, nil)
	if _, err := disabled.Preview(context.Background(), 42, w.ID, "", input); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("missing sandbox must fail closed")
	}
	if _, err := New(r, cfg, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := New(nil, cfg, nil); err == nil {
		t.Fatal("nil repo accepted")
	}
	bad := cfg
	bad.MaxBodyBytes = 16 << 10
	if bad.Validate() == nil {
		t.Fatal("source payload cap incompatible with JSON escapes")
	}
}
