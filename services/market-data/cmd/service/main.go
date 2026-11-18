package main

import (
	"log/slog"
	"os"
	"qslate/services/market-data/internal/app"
	"qslate/services/market-data/internal/feed"
	handler "qslate/services/market-data/internal/http"
	"qslate/services/market-data/internal/platform/config"
	"qslate/services/market-data/internal/platform/httpx"
	"qslate/services/market-data/internal/platform/session"
	"qslate/services/market-data/internal/platform/token"
	"qslate/services/market-data/internal/repository"
	"qslate/services/market-data/internal/service"
)

func run(logger *slog.Logger) error {
	cfg, db, parser, e := app.Open()
	if e != nil {
		return e
	}
	defer db.Close()
	sessions, e := session.New(os.Getenv("AUTH_SERVICE_URL"))
	if e != nil {
		return e
	}
	verifier, e := token.NewVerifier(config.Env("JWT_PUBLIC_KEY_FILE", ".secrets/auth-public.pem"), config.Env("JWT_ISSUER", "qslate-auth"), config.Env("JWT_AUDIENCE", "qslate-api"))
	if e != nil {
		return e
	}
	feeds, e := feed.New(cfg.Live)
	if e != nil {
		return e
	}
	svc, e := service.New(repository.New(db), parser, cfg)
	if e != nil {
		return e
	}
	return httpx.Run(config.Env("HTTP_ADDR", ":8088"), handler.New(svc, feeds, verifier, sessions, db.PingContext, logger), logger)
}
func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "market-data")
	if e := run(logger); e != nil {
		logger.Error("startup failed", "error", e)
		os.Exit(1)
	}
}
