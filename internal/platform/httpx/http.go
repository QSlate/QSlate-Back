package httpx

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"time"
)

func JSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func Error(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

// Decode accepts exactly one JSON object, with a bounded body and no unknown fields.
func Decode(w http.ResponseWriter, r *http.Request, out any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		Error(w, 415, "unsupported_media_type", "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	defer r.Body.Close()
	var raw json.RawMessage
	decoder := json.NewDecoder(r.Body)
	if err = decoder.Decode(&raw); err == nil {
		var extra any
		if extraErr := decoder.Decode(&extra); extraErr != io.EOF {
			err = extraErr
			if err == nil {
				err = errors.New("expected one JSON object")
			}
		}
	}
	if err == nil && (len(raw) == 0 || raw[0] != '{') {
		err = errors.New("expected object")
	}
	if err == nil {
		// Use the strict decoder only after confirming the top-level object.
		strict := json.NewDecoder(bytes.NewReader(raw))
		strict.DisallowUnknownFields()
		err = strict.Decode(out)
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			Error(w, 413, "body_too_large", "request body exceeds 16 KiB")
		} else {
			Error(w, 400, "invalid_json", "expected one JSON object with supported fields")
		}
		return false
	}
	return true
}

type requestIDKey struct{}

func RequestID(ctx context.Context) string { id, _ := ctx.Value(requestIDKey{}).(string); return id }

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *responseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(data)
}
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func Middleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		var validID bool
		if len(id) == 32 {
			_, err := hex.DecodeString(id)
			validID = err == nil
		}
		if !validID {
			buffer := make([]byte, 16)
			_, _ = rand.Read(buffer)
			id = hex.EncodeToString(buffer)
		}
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("Cache-Control", "no-store")
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id))
		started := time.Now()
		writer := &responseWriter{ResponseWriter: w}
		defer func() {
			if recovered := recover(); recovered != nil {
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}
				logger.Error("request panic", "request_id", id)
				if writer.status == 0 {
					Error(writer, 500, "internal_error", "internal server error")
				} else {
					panic(http.ErrAbortHandler)
				}
			}
			status := writer.status
			if status == 0 {
				status = 200
			}
			logger.Info("request", "method", r.Method, "path", r.URL.Path, "status", status, "duration_ms", time.Since(started).Milliseconds(), "request_id", id)
		}()
		next.ServeHTTP(writer, r)
	})
}

func Probes(mux *http.ServeMux, ready func(context.Context) error) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { JSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if ready != nil && ready(ctx) != nil {
			Error(w, 503, "not_ready", "service dependencies unavailable")
			return
		}
		JSON(w, 200, map[string]string{"status": "ready"})
	})
}
