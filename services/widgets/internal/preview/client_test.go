package preview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"qslate/services/widgets/internal/domain"
	"qslate/services/widgets/internal/service"
	"strings"
	"testing"
)

func TestControllerDispatchAndFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		mode   string
		want   error
	}{{"queued", 202, "valid", nil}, {"bad identity", 202, "wrong", domain.ErrUnavailable}, {"redirect", 302, "valid", domain.ErrUnavailable}, {"upstream failed", 500, "valid", domain.ErrUnavailable}, {"invalid response", 202, "bad-json", domain.ErrUnavailable}} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("s", 32) || r.Header.Get("Idempotency-Key") != "job-identity" {
					t.Error("incorrect controller credential or identity")
				}
				var in domain.PreviewRequest
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Source != "untrusted source, never executed" || in.OwnerID != 42 {
					t.Error("incorrect immutable snapshot")
				}
				w.Header().Set("Location", "/must-not-follow")
				w.WriteHeader(test.status)
				if test.mode == "bad-json" {
					_, _ = w.Write([]byte(`oops`))
					return
				}
				id := in.JobID
				if test.mode == "wrong" {
					id = "other"
				}
				_ = json.NewEncoder(w).Encode(domain.Job{JobID: id, Status: "queued"})
			}))
			defer server.Close()
			t.Setenv("TEST_PREVIEW_ENDPOINT", server.URL)
			t.Setenv("TEST_PREVIEW_TOKEN", strings.Repeat("s", 32))
			client, err := New(service.PreviewConfig{Enabled: true, EndpointEnv: "TEST_PREVIEW_ENDPOINT", TokenEnv: "TEST_PREVIEW_TOKEN", TimeoutSeconds: 1, AllowInsecureHTTP: true})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Submit(context.Background(), domain.PreviewRequest{JobID: "job-identity", OwnerID: 42, Input: domain.Input{Source: "untrusted source, never executed"}})
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v want %v", err, test.want)
			}
		})
	}
	if client, err := New(service.PreviewConfig{}); err != nil || client != nil {
		t.Fatal("disabled controller should have no executor")
	}
	if _, err := New(service.PreviewConfig{Enabled: true, EndpointEnv: "ABSENT_TEST_URL", TokenEnv: "ABSENT_TEST_TOKEN"}); err == nil {
		t.Fatal("enabled controller without credentials accepted")
	}
}
