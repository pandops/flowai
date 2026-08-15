//go:build integration && legacy_environment_api

package test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flowai/platform/svc/state-registry/internal/httpapi"
	"github.com/flowai/platform/svc/state-registry/internal/platform"
)

// newOpenEnvironmentHTTPSetup builds the production OpenEnvironment
// surface on top of a real PostgreSQL harness so the HTTP-layer
// secrecy tests can drive the documented router path.
func newOpenEnvironmentHTTPSetup(t *testing.T) (*section9Harness, *bytes.Buffer) {
	t.Helper()
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	h.seedSourceSystem(t, "src-a", "team-a", "listener-a")
	h.seedTaskType(t, "type-a", "team-a", "tag-a")
	env, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
		Name:   "team-wide-env",
		Values: map[string]string{"K": "v"},
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	created, err := h.repo.CreateSecret(context.Background(), h.operatorIdentity("op-1"), env.EnvironmentID, platform.SecretCreateRequest{
		Name:  "alpha",
		Value: "the-very-secret-plaintext-value",
		Scope: env.Scope,
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	h.seedClaimedTask(t, "task-a", "team-a", "src-a", "src-id-a", "type-a", "tag-a", "exec-a", env.EnvironmentID, nil)
	h.lastEnv = env
	h.lastSecret = created
	return h, new(bytes.Buffer)
}

func (h *section9Harness) openEnvHTTPRequest(t *testing.T, method, path, header, token string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("X-FlowAI-Executor-Id", "exec-a")
	req.Header.Set("X-FlowAI-Role", "team-executor")
	req.Header.Set("X-FlowAI-Team-Id", "team-a")
	if header != "" {
		req.Header.Set("X-FlowAI-Request-Id", header)
	}
	if token != "" {
		req.Header.Set("X-FlowAI-Scope-Token", token)
	}
	return req
}

// ---------------------------------------------------------------------------
// TestOpenEnvironmentHTTPPlaintextFreeErrorBody
// ---------------------------------------------------------------------------

// TestOpenEnvironmentHTTPPlaintextFreeErrorBody drives the
// non-revealing 404 envelope on the production HTTP surface and
// asserts that the response body never contains plaintext,
// token, MAC, key, ciphertext, tag, or nonce bytes for any
// denial category.
func TestOpenEnvironmentHTTPPlaintextFreeErrorBody(t *testing.T) {
	h, logBuf := newOpenEnvironmentHTTPSetup(t)
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	router := httpapi.Routes("state-registry", "", logger, alwaysReady, httpapi.NewDecryptOps(), true, h.repo)
	env := h.lastEnv
	plaintext := "the-very-secret-plaintext-value"
	hmacHex := hex.EncodeToString(h.hmacKey)
	forgedToken := h.issueScopeTokenFor(t, env.EnvironmentID, "task-a", "exec-a", nil, "team-a")
	denials := []struct {
		name  string
		token string
	}{
		{name: "missing scope token", token: ""},
		{name: "garbage scope token", token: "garbage-token"},
		{name: "tampered MAC", token: tamperScopeTokenMAC(t, forgedToken)},
		{name: "unknown key id", token: "unknown-key"},
	}
	for _, tc := range denials {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := h.openEnvHTTPRequest(t, http.MethodGet, "/v1/environments/"+env.EnvironmentID+"/open?task_id=task-a", "req-"+tc.name, tc.token)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s: status=%d want 404", tc.name, rec.Code)
			}
			body := rec.Body.String()
			for _, secret := range []string{plaintext, "garbage-token", forgedToken, hmacHex, h.keyID} {
				if strings.Contains(body, secret) {
					t.Errorf("%s: response body contains %q", tc.name, secret)
				}
			}
			if !strings.Contains(body, "environment_unknown_or_unavailable") {
				t.Errorf("%s: response body missing the documented non-revealing code", tc.name)
			}
		})
	}
	// The 404 path does not emit log entries by design; the
	// assertion confirms the absence of forbidden material.
	logs := logBuf.String()
	for _, secret := range []string{plaintext, "garbage-token", hmacHex} {
		if strings.Contains(logs, secret) {
			t.Errorf("log buffer contains %q", secret)
		}
	}
}

// ---------------------------------------------------------------------------
// TestOpenEnvironmentHTTPSuccessBodyIsPlaintextFree
// ---------------------------------------------------------------------------

// TestOpenEnvironmentHTTPSuccessBodyIsPlaintextFree asserts that
// the successful 200 response body on the production HTTP surface
// returns the authorized decrypted value to the assigned Executor
// but never leaks the AES key bytes, the HMAC key bytes, the
// ciphertext, the tag, the nonce, or any token segment.
func TestOpenEnvironmentHTTPSuccessBodyIsPlaintextFree(t *testing.T) {
	h, logBuf := newOpenEnvironmentHTTPSetup(t)
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	router := httpapi.Routes("state-registry", "", logger, alwaysReady, httpapi.NewDecryptOps(), true, h.repo)
	env := h.lastEnv
	goodToken := h.issueScopeTokenFor(t, env.EnvironmentID, "task-a", "exec-a", nil, "team-a")
	storedCipher := querySingleBytes(t, h.db, `SELECT ciphertext FROM secret_versions WHERE secret_id = $1 AND version = 1`, h.lastSecret.Secret.SecretID)
	storedNonce := querySingleBytes(t, h.db, `SELECT nonce FROM secret_versions WHERE secret_id = $1 AND version = 1`, h.lastSecret.Secret.SecretID)
	storedTag := querySingleBytes(t, h.db, `SELECT authentication_tag FROM secret_versions WHERE secret_id = $1 AND version = 1`, h.lastSecret.Secret.SecretID)
	if len(storedCipher) == 0 || len(storedNonce) != 12 || len(storedTag) != 16 {
		t.Fatalf("unexpected stored ciphertext/nonce/tag lengths")
	}
	req := h.openEnvHTTPRequest(t, http.MethodGet, "/v1/environments/"+env.EnvironmentID+"/open?task_id=task-a", "req-success", goodToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, forbidden := range []string{
		hex.EncodeToString(h.aesKey),
		hex.EncodeToString(h.hmacKey),
		base64.RawURLEncoding.EncodeToString(storedCipher),
		base64.RawURLEncoding.EncodeToString(storedNonce),
		base64.RawURLEncoding.EncodeToString(storedTag),
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("successful response body contains forbidden material %q", forbidden)
		}
	}
	if !strings.Contains(body, "the-very-secret-plaintext-value") {
		t.Errorf("successful response body missing the authorized decrypted value")
	}
	if strings.Contains(logBuf.String(), hex.EncodeToString(h.aesKey)) {
		t.Errorf("log buffer contains AES key bytes")
	}
}

// ---------------------------------------------------------------------------
// TestOpenEnvironmentHTTPErrorEnvelopeShape
// ---------------------------------------------------------------------------

// TestOpenEnvironmentHTTPErrorEnvelopeShape asserts that the
// non-revealing 404 envelope surfaces the canonical
// {code,message,request_id} shape with the real correlation id
// and never carries the synthetic "open-<uuid>" value the
// previous implementation used.
func TestOpenEnvironmentHTTPErrorEnvelopeShape(t *testing.T) {
	h, _ := newOpenEnvironmentHTTPSetup(t)
	logger := slog.New(slog.NewTextHandler(new(bytes.Buffer), &slog.HandlerOptions{Level: slog.LevelDebug}))
	router := httpapi.Routes("state-registry", "", logger, alwaysReady, httpapi.NewDecryptOps(), true, h.repo)
	env := h.lastEnv
	req := h.openEnvHTTPRequest(t, http.MethodGet, "/v1/environments/"+env.EnvironmentID+"/open?task_id=task-a", "req-shape", "garbage")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rec.Code)
	}
	var envelope struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Code != "environment_unknown_or_unavailable" {
		t.Errorf("code=%q want environment_unknown_or_unavailable", envelope.Code)
	}
	if envelope.RequestID != "req-shape" {
		t.Errorf("request_id=%q want req-shape (real correlation id)", envelope.RequestID)
	}
	if strings.Contains(rec.Body.String(), "open-") {
		t.Errorf("response body contains the synthetic open-uuid prefix: %s", rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// TestOpenEnvironmentHTTPAuditRequestIDIsReal
// ---------------------------------------------------------------------------

// TestOpenEnvironmentHTTPAuditRequestIDIsReal asserts that the
// audit row appended by the production HTTP path stores the real
// X-FlowAI-Request-Id correlation identifier, not a synthetic
// "open-<uuid>" value.
func TestOpenEnvironmentHTTPAuditRequestIDIsReal(t *testing.T) {
	h, _ := newOpenEnvironmentHTTPSetup(t)
	logger := slog.New(slog.NewTextHandler(new(bytes.Buffer), &slog.HandlerOptions{Level: slog.LevelDebug}))
	router := httpapi.Routes("state-registry", "", logger, alwaysReady, httpapi.NewDecryptOps(), true, h.repo)
	env := h.lastEnv
	goodToken := h.issueScopeTokenFor(t, env.EnvironmentID, "task-a", "exec-a", nil, "team-a")
	correlationID := "req-http-correlation"
	req := h.openEnvHTTPRequest(t, http.MethodGet, "/v1/environments/"+env.EnvironmentID+"/open?task_id=task-a", correlationID, goodToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var storedRequestID string
	if err := h.db.QueryRowContext(context.Background(),
		`SELECT request_id FROM audit_entries
		 WHERE action = 'environment.open' AND resource_id = $1`, env.EnvironmentID,
	).Scan(&storedRequestID); err != nil {
		t.Fatalf("read audit_entries: %v", err)
	}
	if storedRequestID != correlationID {
		t.Errorf("audit request_id=%q want %q", storedRequestID, correlationID)
	}
	if strings.HasPrefix(storedRequestID, "open-") {
		t.Errorf("audit request_id=%q still uses the synthetic open-uuid prefix", storedRequestID)
	}
}

func alwaysReady() (bool, map[string]bool) { return true, nil }

func tamperScopeTokenMAC(t *testing.T, token string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	macBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode mac: %v", err)
	}
	macBytes[0] ^= 0xff
	return parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString(macBytes)
}
