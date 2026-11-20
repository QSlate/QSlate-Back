package httpx

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeRejectsMalformedOrUnboundedRequests(t *testing.T) {
	for _, test := range []struct {
		name, body, contentType string
		want                    int
	}{
		{"null", "null", "application/json", 400},
		{"array", "[]", "application/json", 400},
		{"unknown_field", `{"unexpected":true}`, "application/json", 400},
		{"second_value", `{"email":"a"} {}`, "application/json", 400},
		{"trailing_garbage", `{"email":"a"} !`, "application/json", 400},
		{"oversized", `{"email":"` + strings.Repeat("x", 17<<10) + `"}`, "application/json", 413},
		{"oversized_trailing_whitespace", `{"email":"a"}` + strings.Repeat(" ", 17<<10), "application/json", 413},
		{"content_type", `{"email":"a"}`, "text/plain", 415},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "/", strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.contentType)
			response := httptest.NewRecorder()
			var input struct {
				Email string `json:"email"`
			}
			if Decode(response, request, &input) {
				t.Fatal("invalid body accepted")
			}
			if response.Code != test.want {
				t.Fatalf("status=%d want=%d", response.Code, test.want)
			}
		})
	}
}
