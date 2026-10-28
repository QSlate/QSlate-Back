package httpx

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeRejectsInvalidUTF8(t *testing.T) {
	req := httptest.NewRequest("POST", "/", strings.NewReader("{\"name\":\"\xff\"}"))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	var v struct {
		Name string `json:"name"`
	}
	if Decode(res, req, &v) || res.Code != 400 {
		t.Fatal("invalid wire UTF-8 accepted")
	}
}
