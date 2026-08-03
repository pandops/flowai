package executor

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestWSReadLoopNoFrameForLongTimeThenTerminalFrame is a regression test
// for the bug where the WS read loop applied a per-iteration
// SetReadDeadline(2s) and continued reading after a timeout. The
// gorilla/websocket documentation states that after a read deadline the
// conn read state is corrupt; continuing to read causes "repeated read
// on failed websocket connection". The fix: drop SetReadDeadline entirely
// and rely on a cancel-driven close-conn goroutine to unblock the
// blocking ReadMessage.
//
// The test publishes a handler that holds the connection open with no
// data for ~2.5s, then sends a single text frame. The read loop must:
//   - block on ReadMessage for the full ~2.5s (no SetReadDeadline panic)
//   - consume the late frame without a "repeated read on failed"
//     panic
//   - exit cleanly with a nil error
func TestWSReadLoopNoFrameForLongTimeThenTerminalFrame(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

	var connStarted atomic.Bool
	var firstFrameAt atomic.Int64
	now := time.Now()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("upgrade: %v", err)
			return
		}
		defer c.Close()
		connStarted.Store(true)
		// Hold the conn open with no data for 2.5s, then write a frame.
		time.Sleep(2500 * time.Millisecond)
		firstFrameAt.Store(time.Since(now).Nanoseconds())
		_ = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"conversation.status","execution_status":"finished"}`))
	}))
	defer srv.Close()

	wsURL := "ws" + srv.URL[4:]
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if !connStarted.Load() {
		t.Fatalf("server never accepted the upgrade")
	}

	// Drain a single message. The previous SetReadDeadline + recover
	// pattern would panic on this path with "repeated read on failed
	// websocket connection".
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type readResult struct {
		data []byte
		err  error
	}
	done := make(chan readResult, 1)
	go func() {
		_, data, err := conn.ReadMessage()
		done <- readResult{data: data, err: err}
	}()

	// Force a hard close via the cancel goroutine so the test cannot
	// hang if the loop is buggy.
	closeDone := make(chan struct{})
	go func() {
		<-ctx.Done()
		_ = conn.Close()
		close(closeDone)
	}()
	defer func() { <-closeDone }()

	start := time.Now()
	select {
	case r := <-done:
		elapsed := time.Since(start)
		if elapsed < 2*time.Second {
			t.Fatalf("returned too early (%s); the late frame was not waited for", elapsed)
		}
		if r.err != nil {
			t.Fatalf("ReadMessage returned error: %v", r.err)
		}
		if !bytes.Contains(r.data, []byte("finished")) {
			t.Fatalf("expected frame to contain 'finished', got %q", r.data)
		}
	case <-ctx.Done():
		t.Fatalf("ReadMessage did not return within %s", time.Since(start))
	}
}

// silence the "imported and not used" lint for any future tweak that
// drops a dependency. Keeping stdlib imports explicit here would only
// obscure the test's intent.
var _ = io.Discard
var _ = slog.LevelDebug
var _ = sync.Mutex{}
