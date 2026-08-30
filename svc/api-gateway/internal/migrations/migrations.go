// Package migrations owns API Gateway's service-local migration history.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

//go:embed sql/*.sql
var migrationFS embed.FS

func NewProvider(db *sql.DB) (*goose.Provider, error) {
	if db == nil {
		return nil, errors.New("migrations: db is nil")
	}
	scoped, err := fs.Sub(migrationFS, "sql")
	if err != nil {
		return nil, fmt.Errorf("scope migrations: %w", err)
	}
	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		scoped,
		goose.WithDisableGlobalRegistry(true),
		goose.WithTableName("api_gateway.goose_db_version"),
	)
	if err != nil {
		return nil, fmt.Errorf("goose provider: %w", err)
	}
	return provider, nil
}

func ApplyUp(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errors.New("migrations: db is nil")
	}
	if _, err := db.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS api_gateway`); err != nil {
		return fmt.Errorf("create api_gateway schema: %w", err)
	}
	provider, err := NewProvider(db)
	if err != nil {
		return err
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}
