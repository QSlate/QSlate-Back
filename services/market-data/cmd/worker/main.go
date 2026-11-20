package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"qslate/services/market-data/internal/app"
	"qslate/services/market-data/internal/repository"
	"qslate/services/market-data/internal/worker"
	"syscall"
)

func run(logger *slog.Logger) error {
	cfg, db, parser, e := app.Open()
	if e != nil {
		return e
	}
	defer db.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	worker.Worker{Repo: repository.New(db), Decoder: parser, Config: cfg, Logger: logger}.Run(ctx)
	return nil
}
func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "market-data-worker")
	if e := run(logger); e != nil {
		logger.Error("startup failed", "error", e)
		os.Exit(1)
	}
}
