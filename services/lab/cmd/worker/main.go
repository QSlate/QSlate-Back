package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"qslate/services/lab/internal/execution"
	"qslate/services/lab/internal/platform/config"
	"qslate/services/lab/internal/platform/postgres"
	"qslate/services/lab/internal/repository"
	"qslate/services/lab/internal/service"
	"qslate/services/lab/migrations"
	"syscall"
	"time"
)

func run(logger *slog.Logger) error {
	cfg, e := service.LoadConfig(config.Env("LAB_CONFIG_FILE", "config/lab.json"))
	if e != nil {
		return e
	}
	if !cfg.Execution.Enabled {
		logger.Warn("execution disabled in configuration")
	}
	dsn, e := config.Required("DATABASE_URL")
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db, e := postgres.Open(startup, dsn)
	if e != nil {
		return e
	}
	defer db.Close()
	if e = migrations.Apply(startup, db); e != nil {
		return e
	}
	worker := execution.Worker{Repo: repository.New(db), Runner: &execution.Docker{Config: cfg}, Config: cfg, Logger: logger}
	return worker.Run(ctx)
}
func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "lab-worker")
	if e := run(logger); e != nil {
		logger.Error("startup failed", "error", e)
		os.Exit(1)
	}
}
