package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"qslate/services/marketplace/internal/artifacts"
	handler "qslate/services/marketplace/internal/http"
	"qslate/services/marketplace/internal/platform/config"
	"qslate/services/marketplace/internal/platform/httpx"
	"qslate/services/marketplace/internal/platform/postgres"
	"qslate/services/marketplace/internal/platform/session"
	"qslate/services/marketplace/internal/platform/token"
	"qslate/services/marketplace/internal/repository"
	"qslate/services/marketplace/internal/service"
	"qslate/services/marketplace/migrations"
)

func run(logger *slog.Logger) error {
	cfg, err := service.LoadConfig(config.Env("MARKETPLACE_CONFIG_FILE", "config/marketplace.json"))
	if err != nil {
		return err
	}
	artifacts, err := artifacts.New(os.Getenv("WIDGETS_SERVICE_URL"), os.Getenv("STRATEGY_SERVICE_URL"), cfg.ArtifactTimeoutSeconds)
	if err != nil {
		return err
	}
	sessions, err := session.New(os.Getenv("AUTH_SERVICE_URL"))
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
	svc, err := service.New(repository.New(db), artifacts, cfg)
	if err != nil {
		return err
	}
	// Public browsing and existing installations need no live artifact services.
	ready := func(ctx context.Context) error { return db.PingContext(ctx) }
	return httpx.Run(config.Env("HTTP_ADDR", ":8086"), handler.New(svc, cfg, verifier, sessions, ready, logger), logger)
}
func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "marketplace")
	if err := run(logger); err != nil {
		logger.Error("startup failed", "error", err)
		os.Exit(1)
	}
}
