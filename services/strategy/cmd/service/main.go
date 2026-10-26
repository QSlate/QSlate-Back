package main

import (
	"context"
	"log/slog"
	"os"
	"qslate/services/strategy/internal/analysis"
	"qslate/services/strategy/internal/domain"
	handler "qslate/services/strategy/internal/http"
	"qslate/services/strategy/internal/platform/config"
	"qslate/services/strategy/internal/platform/httpx"
	"qslate/services/strategy/internal/platform/postgres"
	"qslate/services/strategy/internal/platform/session"
	"qslate/services/strategy/internal/platform/token"
	"qslate/services/strategy/internal/repository"
	"qslate/services/strategy/internal/service"
	"qslate/services/strategy/migrations"
	"time"
)

func run(logger *slog.Logger) error {
	cfg, err := service.LoadConfig(config.Env("STRATEGY_CONFIG_FILE", "config/strategy.json"))
	if err != nil {
		return err
	}
	client, err := analysis.New(cfg.Analysis)
	if err != nil {
		return err
	}
	var analyzer domain.Analyzer
	if client != nil {
		analyzer = client
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
	svc, err := service.New(repository.New(db), cfg, analyzer)
	if err != nil {
		return err
	}
	ready := func(ctx context.Context) error {
		if err := db.PingContext(ctx); err != nil {
			return err
		}
		if err := sessions.Ready(ctx); err != nil {
			return err
		}
		if client != nil {
			return client.Ready(ctx)
		}
		return nil
	}
	return httpx.Run(config.Env("HTTP_ADDR", ":8084"), handler.New(svc, cfg, verifier, sessions, ready, logger), logger)
}
func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "strategy")
	if err := run(logger); err != nil {
		logger.Error("startup failed", "error", err)
		os.Exit(1)
	}
}
