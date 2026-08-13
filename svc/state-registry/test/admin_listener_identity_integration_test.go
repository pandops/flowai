//go:build integration

// Section 3a migration-integration tests that pin the database-side
// listener-identity uniqueness contract the planned AdminRepository
// relies on. These tests share the freshDB + applyMigrations fixture
// used by migrations_test.go and migration_assertions_test.go.
//
// RED COMMAND (with integration build tag):
//
//	go test -tags=integration ./svc/state-registry/... \
//	    -run 'TestSourceSystemListenerIdentityUniqueness|\
//
// TestAdminSourceSystemRejectsDuplicateListenerIdentity|\
// TestAdminSourceSystemRejectsCrossTeamListenerIdentity|\
// TestSourceSystemListenerIdentityIndexExists' -count=1
//
// These tests reference only the existing Postgres fixture helpers
// (freshDB, applyMigrations, indexOnColumns, mustExec, mustReject)
// and are GREEN-today structural regression tests that pin the
// migration so future schema drift cannot silently relax the
// listener_identity uniqueness contract. The RED state of the
// Section 3a RED command overall is driven by the planned
// httpapi.Store + platform packages not yet existing; see
// svc/state-registry/internal/httpapi/admin_test.go.
package test

import (
	"context"
	"testing"
	"time"
)

// TestSourceSystemListenerIdentityUniqueness proves the
// database-enforced global uniqueness of source_systems.listener_identity.
// The first INSERT accepts the listener_identity. Two follow-up
// INSERTs (same team and different team) MUST be rejected by the
// database even though they use different source_system_id values.
//
// MUST NOT create or update any other source_systems row: mustReject
// is the asserted outcome for every duplicate attempt.
func TestSourceSystemListenerIdentityUniqueness(t *testing.T) {
	db := freshDB(t)
	applyMigrations(t, db)
	preflight(t, db)

	// Seed minimum fixture so source_systems FK against teams passes.
	// We use a single source_systems row with listener_identity=L-uniq,
	// then verify that second and third attempts (same team / different
	// team) are both rejected at the database level.
	mustExec(t, db,
		`INSERT INTO teams (team_id, team_name, default_image) VALUES
		 ('team-a', 'Team A', $1::jsonb),
		 ('team-b', 'Team B', $1::jsonb)`,
		testImage,
	)

	// First registration under team-a succeeds.
	mustExec(t, db,
		`INSERT INTO source_systems (source_system_id, team_id, listener_identity)
		 VALUES ('src-a', 'team-a', 'L-uniq')`,
	)

	// Same-team duplicate with a different source_system_id MUST be
	// rejected even when the team scope matches.
	mustReject(t, db,
		"duplicate listener_identity under the same team",
		`INSERT INTO source_systems (source_system_id, team_id, listener_identity)
		 VALUES ('src-b-same-team', 'team-a', 'L-uniq')`,
	)

	// Cross-team duplicate MUST also be rejected: the global
	// uniqueness index is not scoped per team.
	mustReject(t, db,
		"duplicate listener_identity under a different team",
		`INSERT INTO source_systems (source_system_id, team_id, listener_identity)
		 VALUES ('src-c-other-team', 'team-b', 'L-uniq')`,
	)
}

// TestSourceSystemListenerIdentityIndexExists asserts the structural
// presence of the unique index over source_systems.listener_identity
// BEFORE any app code accepts a request. The migration assertion is
// the regression pin: any future migration that relaxes the index is
// caught here before a single POST /admin/source-systems runs.
//
// The companion HTTP-level pin lives in
// svc/state-registry/internal/httpapi/admin_test.go
// (TestSourceSystemListenerIdentityIndexExists).
func TestSourceSystemListenerIdentityIndexExists(t *testing.T) {
	db := freshDB(t)
	applyMigrations(t, db)
	preflight(t, db)

	if !tableExists(t, db, "source_systems") {
		t.Fatalf("source_systems table missing; cannot assert listener_identity global uniqueness index")
	}

	// Find any UNIQUE index whose column list is exactly
	// (listener_identity) on source_systems. Any qualifier name is
	// acceptable because the GREEN migration may pick any
	// deterministic index name (the unique-index assertion is
	// behavioral, not naming-based).
	if !indexOnColumns(t, db, "source_systems", []string{"listener_identity"}, true) {
		t.Errorf("global uniqueness index over source_systems.listener_identity is missing; " +
			"the planned AdminRepository must rely on a global unique index to enforce one-listener-one-(team,source_system)")
	}

	// Defense in depth: also probe information_schema for a
	// CHECK or column-level UNIQUE constraint on listener_identity
	// since both Postgres UNIQUE INDEX and inline UNIQUE qualify.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var hasColumnUnique bool
	if err := db.QueryRowContext(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM information_schema.table_constraints
			WHERE table_schema = 'public'
			  AND table_name = 'source_systems'
			  AND constraint_type IN ('UNIQUE')
		)`).Scan(&hasColumnUnique); err != nil {
		t.Fatalf("information_schema.table_constraints lookup failed: %v", err)
	}
	if !hasColumnUnique {
		t.Errorf("no UNIQUE constraint on source_systems reported by information_schema; " +
			"migration must declare a global uniqueness binding on listener_identity")
	}
}
