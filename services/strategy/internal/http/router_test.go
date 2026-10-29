package http

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"qslate/services/strategy/internal/domain"
	"qslate/services/strategy/internal/platform/session"
	"qslate/services/strategy/internal/service"
	"strings"
	"testing"
)

type verifier struct{}

func (verifier) Verify(raw string) (int64, error) {
	if raw != "valid" {
		return 0, errors.New("invalid token")
	}
	return 42, nil
}

type sessions struct {
	err   error
	calls int
}

func (s *sessions) Validate(_ context.Context, auth string, uid int64) error {
	s.calls++
	if uid != 42 || auth != "Bearer valid" {
		return session.ErrUnauthorized
	}
	return s.err
}

type repo struct {
	domain.Repository
	creates int
}

func (r *repo) Create(_ context.Context, _ int64, sid, vid string, in domain.Input) (domain.Strategy, error) {
	r.creates++
	return domain.Strategy{Summary: domain.Summary{ID: sid, VersionID: vid, Name: in.Name}, Source: in.Source, Parameters: in.Parameters}, nil
}
func (r *repo) Get(context.Context, int64, string) (domain.Strategy, error) {
	return domain.Strategy{}, errors.New("password must not leak")
}
func routerFor(t *testing.T, active *sessions) (http.Handler, *repo) {
	t.Helper()
	cfg, err := service.LoadConfig("../../config/strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	db := &repo{}
	svc, err := service.New(db, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	return New(svc, cfg, verifier{}, active, nil, slog.New(slog.NewTextHandler(io.Discard, nil))), db
}
func request(handler http.Handler, method, path, content string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Authorization", "Bearer valid")
	req.Header.Set("Content-Type", content)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}
func TestAllNineOperationsCheckLiveSessionsBeforePayloads(t *testing.T) {
	active := &sessions{err: session.ErrUnauthorized}
	handler, _ := routerFor(t, active)
	for _, path := range []string{"GET /strategies", "POST /strategies", "GET /strategies/id", "PUT /strategies/id", "DELETE /strategies/id", "POST /strategies/import", "GET /strategies/id/versions", "POST /strategies/id/restore", "POST /strategies/lint"} {
		parts := strings.SplitN(path, " ", 2)
		for _, bearer := range []string{"", "Bearer forged", "Bearer valid"} {
			req := httptest.NewRequest(parts[0], parts[1], nil)
			req.Header.Set("Authorization", bearer)
			req.Header.Set("X-User-ID", "42")
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != 401 {
				t.Fatalf("%s bypassed auth: %d", path, res.Code)
			}
		}
	}
	if active.calls != 9 {
		t.Fatal("unverified tokens reached Auth")
	}
	active.err = session.ErrUnavailable
	if res := request(handler, "GET", "/strategies", "", nil); res.Code != 503 {
		t.Fatal("Auth outage did not fail closed")
	}
}
func TestStrictJSONAndErrors(t *testing.T) {
	handler, _ := routerFor(t, &sessions{})
	for _, test := range []struct {
		method, path, body, media string
		status                    int
	}{
		{"POST", "/strategies", `{"name":"Draft","source":"` + strings.Repeat("x", 50000) + `"}`, "application/json", 201},
		{"POST", "/strategies", `{"name":"Draft","source":"x","owner_id":1}`, "application/json", 400},
		{"POST", "/strategies", `{"name":"Draft","source":"x","parameters":null}`, "application/json", 400},
		{"POST", "/strategies", `{} {}`, "application/json", 400}, {"POST", "/strategies", `null`, "application/json", 400},
		{"POST", "/strategies", strings.Repeat(" ", 1100000), "application/json", 413},
		{"POST", "/strategies", `{}`, "text/plain", 415}, {"PUT", "/strategies/" + strings.Repeat("a", 43), `{"name":"x","source":"x"}`, "application/json", 400},
		{"POST", "/strategies/lint", `{"source":"x"}`, "application/json", 503},
		{"POST", "/strategies/lint", `{}`, "application/json", 400},
		{"POST", "/strategies/lint", `{"source":null}`, "application/json", 400},
		{"GET", "/strategies?limit=0", "", "", 400}, {"GET", "/strategies?limit=1&limit=2", "", "", 400}, {"GET", "/strategies?owner_id=42", "", "", 400},
		{"GET", "/strategies/" + strings.Repeat("a", 43), "", "", 500},
	} {
		res := request(handler, test.method, test.path, test.media, strings.NewReader(test.body))
		if res.Code != test.status || strings.Contains(res.Body.String(), "password") {
			t.Fatalf("%s: %d want %d", test.path, res.Code, test.status)
		}
	}
}
func TestStrictStreamingPythonImport(t *testing.T) {
	for _, test := range []struct {
		name, filename, source, extra string
		status                        int
	}{
		{"imported", "strategy.py", "\ufeffprint('ok')", "", 201}, {"large", "strategy.py", strings.Repeat("x", 100000), "", 201},
		{"wrong", "strategy.txt", "x", "", 400}, {"path", "../evil.py", "x", "", 400}, {"bad", "strategy.py", "\xff", "", 400}, {"nul", "strategy.py", "x\x00", "", 400},
		{"empty", "strategy.py", "", "", 400}, {"too big", "strategy.py", strings.Repeat("x", 100001), "", 413},
		{"double", "strategy.py", "x", "file", 400}, {"extra", "strategy.py", "x", "owner_id", 400}, {"names", "strategy.py", "x", "name", 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, db := routerFor(t, &sessions{})
			var body bytes.Buffer
			w := multipart.NewWriter(&body)
			w.WriteField("name", test.name)
			header := make(textproto.MIMEHeader)
			header.Set("Content-Disposition", `form-data; name="file"; filename="`+test.filename+`"`)
			part, _ := w.CreatePart(header)
			io.WriteString(part, test.source)
			if test.extra != "" {
				w.WriteField(test.extra, "x")
			}
			w.Close()
			res := request(handler, "POST", "/strategies/import", w.FormDataContentType(), &body)
			if res.Code != test.status {
				t.Fatalf("got %d want %d: %s", res.Code, test.status, res.Body.String())
			}
			if test.status != 201 && db.creates != 0 {
				t.Fatal("rejected upload persisted")
			}
		})
	}
}
