package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"qslate/internal/platform/httpx"
	"qslate/internal/platform/postgres"
	"qslate/internal/platform/token"
	"qslate/services/auth/internal/config"
	handler "qslate/services/auth/internal/http"
	"qslate/services/auth/internal/repository"
	"qslate/services/auth/internal/service"
	"qslate/services/auth/migrations"
)

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	signer, err := token.NewSigner(cfg.PrivateKeyFile, cfg.Issuer, cfg.Audience, cfg.TTL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err = migrations.Apply(ctx, db); err != nil {
		return err
	}
	svc, err := service.New(repository.New(db), signer, cfg.BcryptCost, cfg.TTL)
	if err != nil {
		return err
	}
	return httpx.Run(cfg.Address, handler.New(svc, signer, db.PingContext, logger), logger)
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "auth")
	if err := run(logger); err != nil {
		logger.Error("startup failed", "error", err)
		os.Exit(1)
	}
}
