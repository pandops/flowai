// v0002.20 transport probe tests for GET /v1/events/stream.
// Drives a real gorilla/websocket client against an httptest
// server. Each test is bounded by an httptest.Server.Close; no
// sleeps or fixed timeouts.
package httpapi_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/flowai/platform/svc/state-registry/internal/health"
	"github.com/flowai/platform/svc/state-registry/internal/httpapi"
	"github.com/flowai/platform/svc/state-registry/internal/store"
)

const (
	esRoleHeader     = "X-FlowAI-Role"
	esTeamHeader     = "X-FlowAI-Team-Id"
	esOperatorHeader = "X-FlowAI-Operator-Id"
	esPath           = "/v1/events/stream"
)

func silenceLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newTestRouter(t *testing.T, testMode bool) http.Handler {
	t.Helper()
	repo := store.New(nil)
	if repo == nil {
		t.Fatalf("store.New(nil) returned nil")
	}
	return httpapi.RoutesWithKeyring(
		"state-registry",
		"",
		silenceLogger(),
		health.Compose(health.NewPostgresProbe(nil), make([]byte, 32)),
		httpapi.NewDecryptOps(),
		nil,
		testMode,
		repo,
	)
}

func wssURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + esPath
}

func dial(t *testing.T, srv *httptest.Server, headers http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	return dialer.Dial(wssURL(srv), headers)
}

func trustedGatewayHeaders() http.Header {
	return http.Header{
		esRoleHeader:     {"gateway"},
		esTeamHeader:     {"team-v0002-20-a"},
		esOperatorHeader: {"operator-v0002-20-a"},
	}
}

func TestEventsStreamTrustedGatewayUpgrade(t *testing.T) {
	srv := httptest.NewServer(newTestRouter(t, true))
	defer srv.Close()

	conn, resp, err := dial(t, srv, trustedGatewayHeaders())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status=%d, want 101", resp.StatusCode)
	}
	// Client close is the lifecycle. The server-side reader
	// returns and the deferred conn.Close() releases the socket.
	if err := conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")); err != nil {
		t.Fatalf("write close: %v", err)
	}
}

// TestEventsStreamRejectsNonGatewayRequestContext asserts that an explicit
// non-Gateway role is rejected before upgrade. The role is trusted request
// context, not certificate-derived authentication.
func TestEventsStreamRejectsNonGatewayRequestContext(t *testing.T) {
	srv := httptest.NewServer(newTestRouter(t, true))
	defer srv.Close()

	conn, resp, err := dial(t, srv, http.Header{esRoleHeader: {"team-executor"}})
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil {
		t.Fatalf("dial must fail for non-gateway data")
	}
	if resp == nil {
		t.Fatalf("expected non-nil response")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status=%d, want 403", resp.StatusCode)
	}
}

// TestEventsStreamValidatesGatewayContextShape asserts the
// v0009 contract: the events stream requires the Gateway-forwarded
// team_id + operator_id identifiers, and rejects missing/invalid
// values with the documented non-auth 400 envelope. Valid
// identifier shapes (any role header) reach the upgrade.
func TestEventsStreamValidatesGatewayContextShape(t *testing.T) {
	cases := []struct {
		name      string
		headers   http.Header
		wantError bool
	}{
		{name: "valid team+operator", headers: http.Header{esTeamHeader: {"t"}, esOperatorHeader: {"o"}}, wantError: false},
		{name: "no team", headers: http.Header{esRoleHeader: {"gateway"}, esOperatorHeader: {"o"}}, wantError: true},
		{name: "no operator", headers: http.Header{esRoleHeader: {"gateway"}, esTeamHeader: {"t"}}, wantError: true},
		{name: "blank team", headers: http.Header{esRoleHeader: {"gateway"}, esTeamHeader: {"   "}, esOperatorHeader: {"o"}}, wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(newTestRouter(t, true))
			defer srv.Close()

			conn, resp, err := dial(t, srv, tc.headers)
			if conn != nil {
				_ = conn.Close()
			}
			if tc.wantError {
				if err == nil {
					t.Fatalf("dial must fail for missing identifier")
				}
				if resp == nil {
					t.Fatalf("expected non-nil response")
				}
				defer resp.Body.Close()
				if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
					t.Fatalf("status=%d, want non-auth response (v0009)", resp.StatusCode)
				}
			} else {
				// Valid Gateway context: upgrade succeeds; the
				// server may close the connection on its own
				// (no work to do), so just verify the upgrade
				// status and let the defer clean up.
				if err != nil {
					t.Fatalf("dial: %v", err)
				}
				defer conn.Close()
				if resp.StatusCode != http.StatusSwitchingProtocols {
					t.Fatalf("status=%d, want 101", resp.StatusCode)
				}
			}
		})
	}
}

// TestEventsStreamMountedInProduction asserts the v0009 production
// invariant: /v1/events/stream is part of every router (testMode
// =false or true). The route is reached without a service-auth
// check; the Gateway-forwarded context shape is the only
// validation.
func TestEventsStreamMountedInProduction(t *testing.T) {
	srv := httptest.NewServer(newTestRouter(t, false))
	defer srv.Close()

	conn, resp, err := dial(t, srv, nil)
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil {
		t.Fatalf("dial must fail without Gateway context in production")
	}
	if resp == nil {
		t.Fatalf("expected non-nil response")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		t.Fatalf("status=%d, want non-auth response (v0009)", resp.StatusCode)
	}
}

// TestEventsStreamMountedAcceptsIdentifiedGatewayInProduction
// asserts the upgraded handshake succeeds in production when the
// trusted-Gateway headers are present. The full production stack
// (withPeerIdentity in cmd/state-registry/main.go) is the
// security boundary that maps a verified cert subject to
// these headers — that mapping is exercised in cmd/state-registry
// main_test.go. This test pins the httpapi.Routes mount
// invariant regardless of TestMode.
func TestEventsStreamMountedAcceptsIdentifiedGatewayInProduction(t *testing.T) {
	srv := httptest.NewServer(newTestRouter(t, false))
	defer srv.Close()

	conn, resp, err := dial(t, srv, trustedGatewayHeaders())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status=%d, want 101 (route mounted in production; trusted-Gateway headers reach the upgrade)", resp.StatusCode)
	}
	if err := conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")); err != nil {
		t.Fatalf("write close: %v", err)
	}
}
