// Package health tests verify the readiness probe composes the
// PostgreSQL flag and AES key presence into a single decision and
// never reports ready when either dependency is missing.
package health

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakePinger struct {
	mu    sync.Mutex
	err   error
	calls int64
}

func (f *fakePinger) PingContext(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.err
}

func (f *fakePinger) Calls() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestPostgresProbeReportsPingError(t *testing.T) {
	fp := &fakePinger{err: errors.New("boom")}
	p := NewPostgresProbe(fp)
	if p.Ready() {
		t.Fatalf("probe should not be ready before Run")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		p.Run(ctx, 10*time.Millisecond)
		close(done)
	}()
	for range 50 {
		if fp.Calls() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if fp.Calls() == 0 {
		t.Fatalf("expected at least one ping")
	}
	if p.Ready() {
		t.Fatalf("probe should report not-ready when ping fails")
	}
}

func TestPostgresProbeMarksReadyAfterSuccess(t *testing.T) {
	fp := &fakePinger{err: nil}
	p := NewPostgresProbe(fp)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		p.Run(ctx, 10*time.Millisecond)
		close(done)
	}()
	for range 50 {
		if p.Ready() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if !p.Ready() {
		t.Fatalf("expected ready after successful ping")
	}
}

func TestPostgresProbeCancellationIsResponsiveBetweenTicks(t *testing.T) {
	fp := &fakePinger{err: nil}
	p := NewPostgresProbe(fp)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.Run(ctx, 10*time.Second)
		close(done)
	}()
	start := time.Now()
	cancel()
	select {
	case <-done:
		if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
			t.Fatalf("Run did not honor cancellation within 500ms; elapsed=%s", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Run did not exit after ctx cancellation")
	}
}

func TestComposeReportsReadyOnlyWhenAllDeps(t *testing.T) {
	probe := NewPostgresProbe(&fakePinger{err: nil})
	probe.ready.Store(true)
	key := make([]byte, 32)
	checker := Compose(probe, key)
	ok, deps := checker()
	if !ok {
		t.Fatalf("expected ready=true with both deps satisfied; deps=%v", deps)
	}
	if !deps["postgres"] || !deps["aes_key"] {
		t.Fatalf("deps=%v", deps)
	}
}

func TestComposeRejectsMissingKey(t *testing.T) {
	probe := NewPostgresProbe(&fakePinger{err: nil})
	probe.ready.Store(true)
	checker := Compose(probe, nil)
	ok, deps := checker()
	if ok {
		t.Fatalf("expected not-ready when AES key missing")
	}
	if deps["aes_key"] {
		t.Fatalf("aes_key must be reported as false")
	}
}

func TestComposeRejectsPostgresFailure(t *testing.T) {
	probe := NewPostgresProbe(&fakePinger{err: errors.New("dial")})
	key := make([]byte, 32)
	checker := Compose(probe, key)
	ok, deps := checker()
	if ok {
		t.Fatalf("expected not-ready when postgres fails")
	}
	if deps["postgres"] {
		t.Fatalf("postgres must be reported as false")
	}
}

func TestAESKeyPresent(t *testing.T) {
	if AESKeyPresent(nil) {
		t.Fatalf("nil key must not be present")
	}
	if AESKeyPresent(make([]byte, 31)) {
		t.Fatalf("31-byte key must not be present")
	}
	if !AESKeyPresent(make([]byte, 32)) {
		t.Fatalf("32-byte key must be present")
	}
}

func TestNewPostgresPingerFromSQLDB(t *testing.T) {
	var p PostgresPinger = NewPostgresPinger(nil)
	_ = p
}
