package session

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestActiveSessionValidationFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"active", 200, `{"user":{"id":42,"username":"test"}}`, nil},
		{"revoked", 401, `{}`, ErrUnauthorized}, {"forbidden", 403, `{}`, ErrUnauthorized},
		{"wrong identity", 200, `{"user":{"id":43}}`, ErrUnauthorized}, {"zero identity", 200, `{"user":{"id":0}}`, ErrUnavailable},
		{"database failure", 500, `{}`, ErrUnavailable}, {"malformed", 200, `oops`, ErrUnavailable},
		{"multiple objects", 200, `{"user":{"id":42}}{}`, ErrUnavailable}, {"redirect", 302, `{}`, ErrUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path != "/users/me" || req.Header.Get("Authorization") != "Bearer test" || req.Header.Get("X-User-ID") != "" {
					t.Error("unexpected authority request")
				}
				w.Header().Set("Location", "/unexpected")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := New(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			if err = client.Validate(context.Background(), "Bearer test", 42); !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}
}

func TestAuthorityConfigurationAndNetworkFailure(t *testing.T) {
	for _, origin := range []string{"", "file:///tmp/auth", "https://u:p@auth", "http://auth/users", "http://auth?query=true", "https://auth/#fragment"} {
		if _, err := New(origin); err == nil {
			t.Fatalf("invalid origin accepted: %s", origin)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { w.WriteHeader(200) }))
	client, _ := New(server.URL)
	if err := client.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	server.Close()
	if err := client.Validate(context.Background(), "Bearer test", 42); !errors.Is(err, ErrUnavailable) {
		t.Fatal("network failure must fail closed")
	}
}
