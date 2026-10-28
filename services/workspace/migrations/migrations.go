package migrations

import (
	"context"
	"database/sql"
	"embed"

	"qslate/services/workspace/internal/platform/migrate"
)

//go:embed *.sql
var files embed.FS

func Apply(ctx context.Context, db *sql.DB) error { return migrate.Apply(ctx, db, files, 71849122) }
