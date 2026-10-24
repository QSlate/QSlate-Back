package service

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"qslate/services/workspace/internal/domain"
)

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
