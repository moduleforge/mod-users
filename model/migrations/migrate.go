package migrations

import (
	"context"
	"database/sql"
	"embed"

	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var FS embed.FS

// TableName is this module's dedicated goose version-tracking table.
const TableName = "goose_db_version_users"

// Migrate applies all pending migrations for this module against db,
// tracking applied state in TableName. Migrations run strictly
// sequentially and never concurrently, so goose's classic global-state
// API is appropriate.
func Migrate(ctx context.Context, db *sql.DB) error {
	goose.SetBaseFS(FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	goose.SetTableName(TableName)
	return goose.UpContext(ctx, db, ".")
}
