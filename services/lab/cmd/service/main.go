package main

import (
	"context"
	"log/slog"
	"os"
	handler "qslate/services/lab/internal/http"
	"qslate/services/lab/internal/marketdata"
	"qslate/services/lab/internal/platform/config"
	"qslate/services/lab/internal/platform/httpx"
	"qslate/services/lab/internal/platform/postgres"
	"qslate/services/lab/internal/platform/session"
	"qslate/services/lab/internal/platform/token"
	"qslate/services/lab/internal/repository"
	"qslate/services/lab/internal/service"
	"qslate/services/lab/internal/strategy"
	"qslate/services/lab/migrations"
	"time"
)

func run(logger *slog.Logger) error {
	cfg, e := service.LoadConfig(config.Env("LAB_CONFIG_FILE", "config/lab.json"))
	if e != nil {
		return e
	}
	sessions, e := session.New(os.Getenv("AUTH_SERVICE_URL"))
	if e != nil {
		return e
	}
	strategies, e := strategy.New(os.Getenv("STRATEGY_SERVICE_URL"))
	if e != nil {
		return e
	}
	verifier, e := token.NewVerifier(config.Env("JWT_PUBLIC_KEY_FILE", ".secrets/auth-public.pem"), config.Env("JWT_ISSUER", "qslate-auth"), config.Env("JWT_AUDIENCE", "qslate-api"))
	if e != nil {
		return e
	}
	dsn, e := config.Required("DATABASE_URL")
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, e := postgres.Open(ctx, dsn)
	if e != nil {
		return e
	}
	defer db.Close()
	if e = migrations.Apply(ctx, db); e != nil {
		return e
	}
	datasets, e := marketdata.New(os.Getenv("MARKET_DATA_SERVICE_URL"), cfg.MaxDatasetBytes)
	if e != nil {
		return e
	}
	var sources []service.Datasets
	if datasets != nil {
		sources = append(sources, datasets)
	}
	svc, e := service.New(repository.New(db), strategies, cfg, sources...)
	if e != nil {
		return e
	}
	return httpx.Run(config.Env("HTTP_ADDR", ":8087"), handler.New(svc, verifier, sessions, db.PingContext, logger), logger)
}
func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "lab")
	if e := run(logger); e != nil {
		logger.Error("startup failed", "error", e)
		os.Exit(1)
	}
}
