package main

import (
	"context"
	"log/slog"
	"os"
	handler "qslate/services/widgets/internal/http"
	"qslate/services/widgets/internal/platform/config"
	"qslate/services/widgets/internal/platform/httpx"
	"qslate/services/widgets/internal/platform/postgres"
	"qslate/services/widgets/internal/platform/session"
	"qslate/services/widgets/internal/platform/token"
	"qslate/services/widgets/internal/preview"
	"qslate/services/widgets/internal/repository"
	"qslate/services/widgets/internal/service"
	"qslate/services/widgets/migrations"
	"time"
)

func run(logger *slog.Logger) error {
	cfg, err := service.LoadConfig(config.Env("WIDGETS_CONFIG_FILE", "config/catalogue.json"))
	if err != nil {
		return err
	}
	executor, err := preview.New(cfg.Preview)
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
	svc, err := service.New(repository.New(db), cfg, executor)
	if err != nil {
		return err
	}
	if err = svc.Sync(ctx); err != nil {
		return err
	}
	ready := func(ctx context.Context) error {
		if err := db.PingContext(ctx); err != nil {
			return err
		}
		return sessions.Ready(ctx)
	}
	return httpx.Run(config.Env("HTTP_ADDR", ":8083"), handler.New(svc, cfg, verifier, sessions, ready, logger), logger)
}
func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "widgets")
	if err := run(logger); err != nil {
		logger.Error("startup failed", "error", err)
		os.Exit(1)
	}
}
