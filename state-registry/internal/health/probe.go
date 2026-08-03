// Package health probes PostgreSQL reachability and AES-256-GCM key
// presence. The two checks together determine whether the State
// Registry should report itself as ready to receive traffic. No
// business checks live here — those land under the protected
// behavior slices.
package health

import (
	"context"
	"database/sql"
	"sync/atomic"
	"time"
)

// ReadinessChecker reports the readiness state and per-dependency
// map for /v1/readyz responses. The returned map is logged on
// transitions and returned to callers; it MUST NOT contain secret
// material or decrypted values.
type ReadinessChecker func() (ready bool, dependencies map[string]bool)

// PostgresPinger abstracts the subset of database/sql used by the
// readiness probe so tests can supply a fake ping without spinning
// up a real PostgreSQL container.
type PostgresPinger interface {
	PingContext(ctx context.Context) error
}

// PostgresProbe builds a ReadinessChecker dependency flag driven by a
// periodic PingContext call. The flag transitions to true once the
// first successful ping completes and stays true unless the probe
// later fails — the harness can observe the dependency directly via
// Ready.
type PostgresProbe struct {
	pinger PostgresPinger
	ready  atomic.Bool
}

// NewPostgresProbe constructs a probe that has NOT yet pinged.
func NewPostgresProbe(pinger PostgresPinger) *PostgresProbe {
	return &PostgresProbe{pinger: pinger}
}

// Run blocks until ctx is canceled. It pings once immediately and
// then on each tick of the supplied interval. The loop selects
// between ctx.Done() and the ticker so cancellation is observed
// between pings without waiting for the next tick.
func (p *PostgresProbe) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	p.pingOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.pingOnce(ctx)
		}
	}
}

func (p *PostgresProbe) pingOnce(ctx context.Context) {
	pingCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	p.ready.Store(p.pinger.PingContext(pingCtx) == nil)
}

// Ready returns the current dependency flag.
func (p *PostgresProbe) Ready() bool { return p.ready.Load() }

// AESKeyPresent is true when a non-empty AES key slice is configured.
// The probe intentionally does not look at the bytes; presence is the
// only contract and bytes must never be observable to logging.
func AESKeyPresent(key []byte) bool { return len(key) == 32 }

// Compose returns a ReadinessChecker that aggregates the postgres
// probe and AES key presence check. Both MUST be true before the
// service advertises itself as ready; either dependency failing
// causes readyz to return 503 with the dependency map.
func Compose(probe *PostgresProbe, aesKey []byte) ReadinessChecker {
	return func() (bool, map[string]bool) {
		deps := map[string]bool{
			"postgres": probe.Ready(),
			"aes_key":  AESKeyPresent(aesKey),
		}
		for _, ok := range deps {
			if !ok {
				return false, deps
			}
		}
		return true, deps
	}
}

// NewPostgresPinger wraps a *sql.DB to satisfy PostgresPinger.
func NewPostgresPinger(db *sql.DB) PostgresPinger { return db }
