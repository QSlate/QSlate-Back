package migrations

import (
	"context"
	"database/sql"
	"embed"

	"qslate/internal/platform/migrate"
)

//go:embed *.sql
var files embed.FS

// Apply retains Auth's lock ID and immutable migration history.
func Apply(ctx context.Context, db *sql.DB) error { return migrate.Apply(ctx, db, files, 71849121) }
