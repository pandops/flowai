package oidc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/flowai/platform/svc/api-gateway/internal/jwtverify"
)

var ErrProviderUnavailable = errors.New("OIDC provider unavailable")

type FlowConfig struct {
	Issuer      string
	ClientID    string
	RedirectURI string
	Scopes      []string
	Timeout     time.Duration
	StateTTL    time.Duration
	ClockSkew   time.Duration
	TokenMaxAge time.Duration
	TeamClaims  TeamClaimAdapter
	ClaimSource string
}

type Discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserInfoEndpoint      string `json:"userinfo_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

type PendingAuthorization struct {
	State        string
	Nonce        string
	PKCEVerifier string
	ExpiresAt    time.Time
}

type Flow struct {
	cfg       FlowConfig
	discovery Discovery
	mu        sync.Mutex
	pending   map[string]PendingAuthorization
	now       func() time.Time
	verifier  *jwtverify.Verifier
	client    *http.Client
}

type Identity struct {
	Issuer        string
	Subject       string
	OperatorID    string
	OIDCTeamIDs   []string
	ProviderState []byte
}

func (f *Flow) Refresh(ctx context.Context, encoded []byte, expectedSubject string) ([]string, []byte, error) {
	var state struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if json.Unmarshal(encoded, &state) != nil || expectedSubject == "" {
		return nil, nil, jwtverify.ErrInvalidToken
	}
	var claims map[string]any
	if state.RefreshToken != "" {
		form := url.Values{"grant_type": {"refresh_token"}, "client_id": {f.cfg.ClientID}, "refresh_token": {state.RefreshToken}}
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, f.discovery.TokenEndpoint, strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response, err := f.client.Do(request)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %w", ErrProviderUnavailable, err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, nil, fmt.Errorf("%w: refresh status %d", ErrProviderUnavailable, response.StatusCode)
		}
		var tokens struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			IDToken      string `json:"id_token"`
			TokenType    string `json:"token_type"`
			ExpiresIn    int    `json:"expires_in"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&tokens) != nil || tokens.AccessToken == "" || tokens.IDToken == "" {
			return nil, nil, fmt.Errorf("%w: malformed refresh response", ErrProviderUnavailable)
		}
		verified, err := f.verifier.Verify(ctx, tokens.IDToken, f.now())
		if err != nil {
			return nil, nil, err
		}
		claims = map[string]any(verified)
		subject, _ := claims["sub"].(string)
		if subject != expectedSubject {
			return nil, nil, jwtverify.ErrInvalidToken
		}
		state.AccessToken = tokens.AccessToken
		if tokens.RefreshToken != "" {
			state.RefreshToken = tokens.RefreshToken
		}
	}
	if f.cfg.ClaimSource == "userinfo" {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.discovery.UserInfoEndpoint, nil)
		request.Header.Set("Authorization", "Bearer "+state.AccessToken)
		response, err := f.client.Do(request)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %w", ErrProviderUnavailable, err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, nil, fmt.Errorf("%w: userinfo status %d", ErrProviderUnavailable, response.StatusCode)
		}
		if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&claims) != nil {
			return nil, nil, jwtverify.ErrInvalidToken
		}
		subject, _ := claims["sub"].(string)
		if subject != expectedSubject {
			return nil, nil, jwtverify.ErrInvalidToken
		}
	} else if claims == nil {
		return nil, nil, fmt.Errorf("%w: refresh token is required for id_token membership", ErrProviderUnavailable)
	}
	teamIDs, err := f.cfg.TeamClaims.Normalize(claims)
	if err != nil {
		return nil, nil, jwtverify.ErrInvalidToken
	}
	updated, err := json.Marshal(map[string]any{"access_token": state.AccessToken, "refresh_token": state.RefreshToken, "token_type": "Bearer"})
	if err != nil {
		return nil, nil, err
	}
	return teamIDs, updated, nil
}

func NewFlow(ctx context.Context, cfg FlowConfig) (*Flow, error) {
	issuer, err := absoluteURL(cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("OIDC issuer: %w", err)
	}
	if cfg.ClientID == "" {
		return nil, errors.New("OIDC client ID is required")
	}
	if _, err := absoluteURL(cfg.RedirectURI); err != nil {
		return nil, fmt.Errorf("OIDC redirect URI: %w", err)
	}
	if cfg.Timeout <= 0 || cfg.StateTTL <= 0 {
		return nil, errors.New("OIDC timeout and state TTL must be positive")
	}
	scopes, err := NormalizeScopes(cfg.Scopes)
	if err != nil {
		return nil, err
	}
	cfg.Scopes = scopes
	discoveryURL := issuer.ResolveReference(&url.URL{Path: strings.TrimSuffix(issuer.Path, "/") + "/.well-known/openid-configuration"})
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery request: %w", err)
	}
	response, err := (&http.Client{Timeout: cfg.Timeout}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProviderUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: discovery status %d", ErrProviderUnavailable, response.StatusCode)
	}
	var discovery Discovery
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&discovery); err != nil {
		return nil, fmt.Errorf("%w: malformed discovery", ErrProviderUnavailable)
	}
	if discovery.Issuer != cfg.Issuer {
		return nil, errors.New("OIDC discovery issuer does not match configured issuer")
	}
	for name, value := range map[string]string{"authorization_endpoint": discovery.AuthorizationEndpoint, "token_endpoint": discovery.TokenEndpoint, "jwks_uri": discovery.JWKSURI} {
		if _, err := absoluteURL(value); err != nil {
			return nil, fmt.Errorf("OIDC discovery %s: %w", name, err)
		}
	}
	if cfg.ClaimSource != "id_token" && cfg.ClaimSource != "userinfo" {
		return nil, errors.New("OIDC claim source must be id_token or userinfo")
	}
	if cfg.ClaimSource == "userinfo" {
		if _, err := absoluteURL(discovery.UserInfoEndpoint); err != nil {
			return nil, fmt.Errorf("OIDC discovery userinfo_endpoint: %w", err)
		}
	}
	if err := cfg.TeamClaims.Validate(); err != nil {
		return nil, fmt.Errorf("OIDC team claims: %w", err)
	}
	if cfg.TokenMaxAge <= 0 || cfg.ClockSkew < 0 {
		return nil, errors.New("OIDC token max age must be positive and clock skew non-negative")
	}
	verifier, err := jwtverify.New(jwtverify.Config{Issuer: cfg.Issuer, Audience: cfg.ClientID, JWKSURL: discovery.JWKSURI, Algorithms: []string{"RS256", "RS384", "RS512"}, ClockSkew: cfg.ClockSkew, TokenMaxAge: cfg.TokenMaxAge, HTTPTimeout: cfg.Timeout})
	if err != nil {
		return nil, fmt.Errorf("OIDC token verifier: %w", err)
	}
	return &Flow{cfg: cfg, discovery: discovery, pending: make(map[string]PendingAuthorization), now: time.Now, verifier: verifier, client: &http.Client{Timeout: cfg.Timeout}}, nil
}

func (f *Flow) Complete(ctx context.Context, code, state string) (Identity, error) {
	if code == "" || state == "" {
		return Identity{}, errors.New("authorization code and state are required")
	}
	pending, ok := f.Consume(state)
	if !ok {
		return Identity{}, jwtverify.ErrInvalidToken
	}
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {f.cfg.ClientID}, "redirect_uri": {f.cfg.RedirectURI}, "code": {code}, "code_verifier": {pending.PKCEVerifier}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, f.discovery.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Identity{}, fmt.Errorf("token request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := f.client.Do(request)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %w", ErrProviderUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Identity{}, fmt.Errorf("%w: token status %d", ErrProviderUnavailable, response.StatusCode)
	}
	var tokens struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&tokens); err != nil || tokens.AccessToken == "" || tokens.IDToken == "" || !strings.EqualFold(tokens.TokenType, "Bearer") {
		return Identity{}, fmt.Errorf("%w: malformed token response", ErrProviderUnavailable)
	}
	claims, err := f.verifier.Verify(ctx, tokens.IDToken, f.now())
	if err != nil {
		return Identity{}, err
	}
	nonce, ok := claims["nonce"].(string)
	if !ok || nonce != pending.Nonce {
		return Identity{}, jwtverify.ErrInvalidToken
	}
	subject, ok := claims["sub"].(string)
	if !ok || subject == "" {
		return Identity{}, jwtverify.ErrInvalidToken
	}
	claimDocument := map[string]any(claims)
	if f.cfg.ClaimSource == "userinfo" {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, f.discovery.UserInfoEndpoint, nil)
		if err != nil {
			return Identity{}, fmt.Errorf("userinfo request: %w", err)
		}
		request.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
		response, err := f.client.Do(request)
		if err != nil {
			return Identity{}, fmt.Errorf("%w: %w", ErrProviderUnavailable, err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return Identity{}, fmt.Errorf("%w: userinfo status %d", ErrProviderUnavailable, response.StatusCode)
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&claimDocument); err != nil {
			return Identity{}, jwtverify.ErrInvalidToken
		}
		userinfoSubject, ok := claimDocument["sub"].(string)
		if !ok || userinfoSubject != subject {
			return Identity{}, jwtverify.ErrInvalidToken
		}
	}
	if f.cfg.TeamClaims.Kind != "" {
		if f.cfg.TeamClaims.ClaimPointer == "" {
			return Identity{}, jwtverify.ErrInvalidToken
		}
	}
	teamIDs, err := f.cfg.TeamClaims.Normalize(claimDocument)
	if err != nil {
		return Identity{}, jwtverify.ErrInvalidToken
	}
	providerState, err := json.Marshal(map[string]any{"access_token": tokens.AccessToken, "refresh_token": tokens.RefreshToken, "token_type": "Bearer", "expires_in": tokens.ExpiresIn})
	if err != nil {
		return Identity{}, fmt.Errorf("encode provider state: %w", err)
	}
	operatorDigest := sha256.Sum256([]byte(f.cfg.Issuer + "\x00" + subject))
	return Identity{Issuer: f.cfg.Issuer, Subject: subject, OperatorID: "oidc_" + base64.RawURLEncoding.EncodeToString(operatorDigest[:]), OIDCTeamIDs: teamIDs, ProviderState: providerState}, nil
}

func (f *Flow) Start(_ context.Context) (string, error) {
	state, err := randomToken(32)
	if err != nil {
		return "", err
	}
	nonce, err := randomToken(32)
	if err != nil {
		return "", err
	}
	verifier, err := randomToken(48)
	if err != nil {
		return "", err
	}
	challenge := sha256.Sum256([]byte(verifier))
	now := f.now()
	f.mu.Lock()
	for key, value := range f.pending {
		if !value.ExpiresAt.After(now) {
			delete(f.pending, key)
		}
	}
	f.pending[state] = PendingAuthorization{State: state, Nonce: nonce, PKCEVerifier: verifier, ExpiresAt: now.Add(f.cfg.StateTTL)}
	f.mu.Unlock()
	authorizationURL, _ := url.Parse(f.discovery.AuthorizationEndpoint)
	query := authorizationURL.Query()
	query.Set("client_id", f.cfg.ClientID)
	query.Set("redirect_uri", f.cfg.RedirectURI)
	query.Set("response_type", "code")
	query.Set("scope", strings.Join(f.cfg.Scopes, " "))
	query.Set("state", state)
	query.Set("nonce", nonce)
	query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	query.Set("code_challenge_method", "S256")
	authorizationURL.RawQuery = query.Encode()
	return authorizationURL.String(), nil
}

func (f *Flow) Consume(state string) (PendingAuthorization, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pending, ok := f.pending[state]
	delete(f.pending, state)
	if !ok || !pending.ExpiresAt.After(f.now()) {
		return PendingAuthorization{}, false
	}
	return pending, true
}

func absoluteURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("must be an absolute URL")
	}
	return parsed, nil
}
func randomToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("secure random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
