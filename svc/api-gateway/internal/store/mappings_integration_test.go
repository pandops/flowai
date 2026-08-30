package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/flowai/platform/svc/api-gateway/internal/migrations"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestMappingStoreAtomicRegistration(t *testing.T) {
	connectionString := os.Getenv("FLOWAI_TEST_POSTGRES_URL")
	if connectionString == "" {
		t.Skip("FLOWAI_TEST_POSTGRES_URL is not set")
	}
	db, err := sql.Open("pgx", connectionString)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := migrations.ApplyUp(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `TRUNCATE api_gateway.oidc_team_mappings CASCADE`); err != nil {
		t.Fatal(err)
	}
	store, err := NewMappingStore(db)
	if err != nil {
		t.Fatal(err)
	}

	const contenders = 8
	results := make(chan bool, contenders)
	errorsCh := make(chan error, contenders)
	var wait sync.WaitGroup
	for range contenders {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, created, err := store.Register(ctx, Mapping{Issuer: "https://issuer.example", OIDCTeamID: "alpha", TeamID: "team-alpha"})
			results <- created
			errorsCh <- err
		}()
	}
	wait.Wait()
	close(results)
	close(errorsCh)
	createdCount := 0
	for created := range results {
		if created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count = %d, want 1", createdCount)
	}
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("identical retry failed: %v", err)
		}
	}

	_, _, err = store.Register(ctx, Mapping{Issuer: "https://issuer.example", OIDCTeamID: "alpha", TeamID: "team-beta"})
	if !errors.Is(err, ErrMappingConflict) {
		t.Fatalf("external conflict = %v", err)
	}
	_, _, err = store.Register(ctx, Mapping{Issuer: "https://issuer.example", OIDCTeamID: "beta", TeamID: "team-alpha"})
	if !errors.Is(err, ErrMappingConflict) {
		t.Fatalf("canonical conflict = %v", err)
	}

	resolved, err := store.Resolve(ctx, "https://issuer.example", []string{"unknown", "alpha", "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 || resolved[0].TeamID != "team-alpha" {
		t.Fatalf("resolved = %+v", resolved)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM api_gateway.oidc_team_mappings`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("mapping row count = %d, want 1", count)
	}
}
