package oidc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestFlowStartProducesOneTimePKCEState(t *testing.T) {
	t.Parallel()
	var issuer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(Discovery{Issuer: issuer, AuthorizationEndpoint: issuer + "/authorize", TokenEndpoint: issuer + "/token", JWKSURI: issuer + "/jwks"})
	}))
	t.Cleanup(server.Close)
	issuer = server.URL
	flow, err := NewFlow(context.Background(), FlowConfig{Issuer: issuer, ClientID: "client", RedirectURI: "https://gateway.example/auth/v1/callback", Scopes: []string{"profile", "profile"}, Timeout: time.Second, StateTTL: time.Minute, ClockSkew: time.Second, TokenMaxAge: time.Minute, ClaimSource: "id_token", TeamClaims: TeamClaimAdapter{Kind: StringArray, ClaimPointer: "/teams"}})
	if err != nil {
		t.Fatal(err)
	}
	location, err := flow.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	for _, key := range []string{"state", "nonce", "code_challenge"} {
		if query.Get(key) == "" {
			t.Fatalf("%s is empty", key)
		}
	}
	if query.Get("code_challenge_method") != "S256" || query.Get("scope") != "openid profile" {
		t.Fatalf("query = %s", query.Encode())
	}
	state := query.Get("state")
	pending, ok := flow.Consume(state)
	if !ok || pending.Nonce != query.Get("nonce") || pending.PKCEVerifier == "" {
		t.Fatalf("pending = %+v, ok = %v", pending, ok)
	}
	if _, ok := flow.Consume(state); ok {
		t.Fatal("state was reusable")
	}
}
