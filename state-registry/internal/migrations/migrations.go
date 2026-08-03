// Package migrations owns the State Registry service-local Goose v3
// migration Provider and the embedded SQL migration files.
//
// The Provider is created on demand from a caller-supplied *sql.DB so
// that the caller — production wiring or the integration test fixture
// — retains full ownership of the database lifecycle. No helper in this
// package closes the *sql.DB. Helper functions that wrap Provider.Up,
// Provider.Down, Provider.DownTo return only errors and explicitly
// avoid calling Provider.Close (which would close the caller's DB).
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

// FS is the embedded file system that contains every Goose SQL
// migration applied by the State Registry service.
//
// Exposed so production wiring and integration tests can share the
// exact same migration files without reading them off the host
// filesystem. Only *.sql files inside the sql/ sub-directory are
// embedded so non-migration files cannot accidentally be picked up
// by Goose's filename-pattern scanner.
//
//go:embed sql/*.sql
var FS embed.FS

// newFS scopes the embedded FS to the migrations sub-directory so
// Goose's flat `*.sql` glob pattern can locate the versioned
// migration files. Without the sub-scope, embed.FS stores paths like
// `sql/00001_initial_schema.sql` which fs.Glob cannot match against
// `*.sql`.
func newFS() (fs.FS, error) {
	sub, err := fs.Sub(FS, "sql")
	if err != nil {
		return nil, fmt.Errorf("scope migrations fs to %q: %w", "sql", err)
	}
	return sub, nil
}

// NewProvider constructs a Goose v3 Provider bound to the caller-owned
// *sql.DB. The Provider is intentionally narrow: it only runs
// migrations sourced from the embedded FS, with the global migration
// registry disabled and the default Goose version table.
//
// The caller owns the *sql.DB. NewProvider does NOT close the
// database; nor do any of the Apply* helpers below. Closing the
// caller-owned *sql.DB is the caller's responsibility.
//
// The returned Provider exposes Goose's Close, but the package-level
// helpers in this file deliberately never call it: doing so would
// close the caller's DB.
func NewProvider(db *sql.DB) (*goose.Provider, error) {
	if db == nil {
		return nil, errors.New("migrations: db is nil")
	}
	fsys, err := newFS()
	if err != nil {
		return nil, fmt.Errorf("migrations fs: %w", err)
	}
	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		fsys,
		goose.WithDisableGlobalRegistry(true),
		goose.WithTableName(goose.DefaultTablename),
	)
	if err != nil {
		return nil, fmt.Errorf("goose provider: %w", err)
	}
	return provider, nil
}

// ApplyUp runs every pending embedded migration. The caller owns db
// and is responsible for opening and closing the connection. ApplyUp
// does NOT call Provider.Close (which would close the caller's DB).
func ApplyUp(ctx context.Context, db *sql.DB) error {
	provider, err := NewProvider(db)
	if err != nil {
		return err
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}

// ApplyDown runs the most recent down migration. The caller owns db
// and is responsible for opening and closing the connection. ApplyDown
// does NOT call Provider.Close.
func ApplyDown(ctx context.Context, db *sql.DB) error {
	provider, err := NewProvider(db)
	if err != nil {
		return err
	}
	if _, err := provider.Down(ctx); err != nil {
		return fmt.Errorf("goose down: %w", err)
	}
	return nil
}

// ApplyDownTo rolls migrations down to (and including) the given
// version. A version of 0 rolls every migration back. The caller owns
// db and is responsible for opening and closing the connection.
// ApplyDownTo does NOT call Provider.Close.
func ApplyDownTo(ctx context.Context, db *sql.DB, version int64) error {
	provider, err := NewProvider(db)
	if err != nil {
		return err
	}
	if _, err := provider.DownTo(ctx, version); err != nil {
		return fmt.Errorf("goose down-to %d: %w", version, err)
	}
	return nil
}

// CurrentVersion returns the highest applied migration version in the
// default Goose version table. Zero means no migration has been
// applied yet. The caller owns db.
func CurrentVersion(ctx context.Context, db *sql.DB) (int64, error) {
	provider, err := NewProvider(db)
	if err != nil {
		return 0, err
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("goose current version: %w", err)
	}
	return version, nil
}
