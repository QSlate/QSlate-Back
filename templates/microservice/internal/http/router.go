package http

import (
	"net/http"

	"qslate/templates/microservice/internal/platform/httpx"
)

func New() http.Handler {
	mux := http.NewServeMux()
	// Replace nil with a dependency readiness check when adding persistence.
	httpx.Probes(mux, nil)
	return mux
}
