// Package app composes only this service's infrastructure.
package app

import (
	"context"
	"database/sql"
	"os"
	"qslate/services/market-data/internal/decoder"
	"qslate/services/market-data/internal/platform/config"
	"qslate/services/market-data/internal/platform/postgres"
	"qslate/services/market-data/internal/service"
	"qslate/services/market-data/migrations"
	"time"
)

func Open() (service.Config, *sql.DB, *decoder.Client, error) {
	cfg, e := service.LoadConfig(config.Env("MARKET_DATA_CONFIG_FILE", "config/market-data.json"))
	if e != nil {
		return cfg, nil, nil, e
	}
	parser, e := decoder.New(os.Getenv("MARKET_DATA_PARSER_URL"), cfg)
	if e != nil {
		return cfg, nil, nil, e
	}
	dsn, e := config.Required("DATABASE_URL")
	if e != nil {
		return cfg, nil, nil, e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, e := postgres.Open(ctx, dsn)
	if e != nil {
		return cfg, nil, nil, e
	}
	if e = migrations.Apply(ctx, db); e == nil {
		e = migrations.Timescale(ctx, db, cfg.RequireTimescale)
	}
	if e != nil {
		db.Close()
		return cfg, nil, nil, e
	}
	return cfg, db, parser, nil
}
