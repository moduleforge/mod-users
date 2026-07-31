package migrations

import (
	"context"
	"database/sql"
	"embed"

	"github.com/pressly/goose/v3"
)

// SQL migration files live in sql/, one level below this file, rather than
// directly alongside it. `goose validate`'s CLI (unlike the CollectMigrations
// path Migrate below uses) globs every *.go and *.sql file in the directory
// it is pointed at and hard-fails if any lacks the NNNN_name version prefix
// -- it has no allowance for a co-located non-migration helper file like this
// one. Keeping this file one level above sql/ lets `goose -dir
// model/migrations/sql validate` (CI's migrate-check job, and `make
// verify`/`migrate.*` in this package's Makefile) see a directory containing
// only real migrations, while this file's exported Migrate/FS API -- and the
// package import path host apps' generated main.go depends on -- are
// unchanged.
//
//go:embed sql/*.sql
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
	return goose.UpContext(ctx, db, "sql")
}
