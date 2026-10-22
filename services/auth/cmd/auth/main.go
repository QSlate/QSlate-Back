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
	"qslate/services/auth/internal/notification"
	"qslate/services/auth/internal/oauth"
	"qslate/services/auth/internal/repository"
	"qslate/services/auth/internal/secretbox"
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
	repo := repository.New(db)
	box, err := secretbox.New(cfg.PrivateKeyFile)
	if err != nil {
		return err
	}
	mailer, err := notification.New(notification.Config{Address: cfg.SMTPAddress, From: cfg.SMTPFrom, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, TLSMode: cfg.SMTPTLS, AppURL: cfg.AppURL}, box, repo, logger)
	if err != nil {
		return err
	}
	svc, err := service.New(repo, signer, mailer, service.Config{AccessTTL: cfg.TTL, RefreshTTL: cfg.RefreshTTL, BcryptCost: cfg.BcryptCost})
	if err != nil {
		return err
	}
	providers, err := oauth.Load(cfg.ProvidersFile)
	if err != nil {
		return err
	}
	httpConfig := handler.Config{SecureCookies: cfg.SecureCookies, AllowedOrigins: cfg.AllowedOrigins}
	oauthHandler := oauth.New(providers, repo, box, svc, httpConfig, cfg.AppURL)
	workerCtx, stopWorker := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); mailer.Run(workerCtx) }()
	err = httpx.Run(cfg.Address, handler.New(svc, signer, db.PingContext, logger, httpConfig, oauthHandler.Routes), logger)
	stopWorker()
	<-done
	return err
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "auth")
	if err := run(logger); err != nil {
		logger.Error("startup failed", "error", err)
		os.Exit(1)
	}
}
