package main

import (
	"log/slog"
	"os"

	"qslate/gateway/internal/config"
	handler "qslate/gateway/internal/http"
	"qslate/gateway/internal/middleware"
	common "qslate/internal/platform/config"
	"qslate/internal/platform/httpx"
	"qslate/internal/platform/token"
)

func run(logger *slog.Logger) error {
	routes, err := config.LoadRoutes(common.Env("ROUTES_FILE", "gateway/config/routes.json"))
	if err != nil {
		return err
	}
	verifier, err := token.NewVerifier(common.Env("JWT_PUBLIC_KEY_FILE", ".secrets/auth-public.pem"), common.Env("JWT_ISSUER", "qslate-auth"), common.Env("JWT_AUDIENCE", "qslate-api"))
	if err != nil {
		return err
	}
	router, err := handler.New(routes, verifier, logger)
	if err != nil {
		return err
	}
	return httpx.Run(common.Env("HTTP_ADDR", ":8080"), middleware.CORS(common.Env("ALLOWED_ORIGINS", "http://localhost:3000"), router), logger)
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "gateway")
	if err := run(logger); err != nil {
		logger.Error("startup failed", "error", err)
		os.Exit(1)
	}
}
