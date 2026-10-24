package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"qslate/internal/platform/config"
	"qslate/internal/platform/httpx"
	"qslate/internal/platform/postgres"
	"qslate/internal/platform/session"
	"qslate/internal/platform/token"
	handler "qslate/services/workspace/internal/http"
	"qslate/services/workspace/internal/repository"
	"qslate/services/workspace/internal/service"
	"qslate/services/workspace/migrations"
)

func run(logger *slog.Logger) error {
	cfg, err := service.LoadConfig(config.Env("WORKSPACE_LAYOUT_FILE", "services/workspace/config/layout.json"))
	if err != nil {
		return err
	}
	authURL, err := config.Required("AUTH_SERVICE_URL")
	if err != nil {
		return err
	}
	sessions, err := session.New(authURL)
	if err != nil {
		return err
	}
	verifier, err := token.NewVerifier(config.Env("JWT_PUBLIC_KEY_FILE", ".secrets/auth-public.pem"), config.Env("JWT_ISSUER", "qslate-auth"), config.Env("JWT_AUDIENCE", "qslate-api"))
	if err != nil {
		return err
	}
	dsn, err := config.Required("DATABASE_URL")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := postgres.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	if err = migrations.Apply(ctx, db); err != nil {
		return err
	}
	svc, err := service.New(repository.New(db), cfg)
	if err != nil {
		return err
	}
	ready := func(ctx context.Context) error {
		if err := db.PingContext(ctx); err != nil {
			return err
		}
		return sessions.Ready(ctx)
	}
	return httpx.Run(config.Env("HTTP_ADDR", ":8082"), handler.New(svc, verifier, sessions, ready, logger), logger)
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "workspace")
	if err := run(logger); err != nil {
		logger.Error("startup failed", "error", err)
		os.Exit(1)
	}
}
