package migrations

import (
	"context"
	"database/sql"
	"embed"
	"qslate/services/market-data/internal/platform/migrate"
)

//go:embed *.sql
var files embed.FS

func Apply(ctx context.Context, db *sql.DB) error { return migrate.Apply(ctx, db, files, 71849128) }

// Optional for ordinary PostgreSQL tests; required in the supplied Timescale deployment.
func Timescale(ctx context.Context, db *sql.DB, required bool) error {
	if !required {
		return nil
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(71849129)`); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS timescaledb`); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `SELECT create_hypertable('md_points','ts',if_not_exists=>TRUE,migrate_data=>TRUE)`); e != nil {
		return e
	}
	return tx.Commit()
}
