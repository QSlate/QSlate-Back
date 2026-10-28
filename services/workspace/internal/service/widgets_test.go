package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"qslate/services/workspace/internal/domain"
)

type failedCatalogue struct{ err error }

func (c failedCatalogue) Validate(context.Context, int64, string, string, bool) error { return c.err }
func TestWidgetReferenceFailureStopsMutation(t *testing.T) {
	for _, want := range []error{domain.ErrCatalogueUnavailable, domain.ErrNotFound, domain.ErrUnauthorized} {
		svc, err := New(&recordingRepository{}, testConfig(), failedCatalogue{want})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = svc.AddWidget(context.Background(), 42, strings.Repeat("a", 43), "widget", "version", nil); !errors.Is(err, want) {
			t.Fatalf("catalogue failure not propagated: %v", err)
		}
	}
	if _, err := New(&recordingRepository{}, testConfig(), nil); err == nil {
		t.Fatal("missing catalogue accepted")
	}
}

func TestConfigurationPreservesNumbersAndRejectsUnstorableJSON(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"n":1e10001}`, `{"n":1e-10001}`, `{"n":1e999999999999999999999}`, `{"nested":[{"value":"\u0000"}]}`, `{"\u0000":true}`, string([]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'})} {
		if _, err := configuration(json.RawMessage(raw), false); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("unstorable JSON accepted: %v", err)
		}
	}
	got, err := configuration(json.RawMessage(`{"integer":9007199254740993,"array":[true,null,{"text":"<tag>"}],"surrogate":"\ud800"}`), false)
	if err != nil || !json.Valid(got) || !strings.Contains(string(got), "9007199254740993") || strings.Contains(string(got), `\ud800`) {
		t.Fatalf("normalization or numeric precision failed: %v", err)
	}
	if got, err := configuration(nil, true); err != nil || string(got) != `{}` {
		t.Fatal("missing configuration must default to an object")
	}
}
