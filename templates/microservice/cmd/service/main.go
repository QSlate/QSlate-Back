package main

import (
	"log/slog"
	"os"

	"qslate/internal/platform/config"
	"qslate/internal/platform/httpx"
	handler "qslate/templates/microservice/internal/http"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "microservice-template")
	// Compose repositories, use cases and handlers here. Persistence is optional.
	if err := httpx.Run(config.Env("HTTP_ADDR", ":8082"), handler.New(), logger); err != nil {
		logger.Error("startup failed", "error", err)
		os.Exit(1)
	}
}
