// Package adminauth validates provider-neutral system-administrator JWTs.
package adminauth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalid      = errors.New("invalid administrator token")
	ErrRole         = errors.New("insufficient administrator role")
	ErrUnavailable  = errors.New("administrator identity provider unavailable")
	errClaimMissing = errors.New("administrator role claim missing")
)

type Config struct {
	Issuer, Audience, JWKSURL, RolePointer, RequiredRole string
	Algorithms                                           []string
	ClockSkew, TokenMaxAge, HTTPTimeout                  time.Duration
}

type Authenticator struct {
	cfg    Config
	client *http.Client
	mu     sync.RWMutex
	keys   map[string]jwk
}
type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func New(cfg Config) (*Authenticator, error) {
	issuer, issuerErr := url.Parse(cfg.Issuer)
	keys, keysErr := url.Parse(cfg.JWKSURL)
	if issuerErr != nil || issuer.Scheme == "" || issuer.Host == "" || keysErr != nil || keys.Scheme == "" || keys.Host == "" {
		return nil, errors.New("admin issuer and JWKS URL must be absolute")
	}
	if cfg.Audience == "" || cfg.RolePointer == "" || !strings.HasPrefix(cfg.RolePointer, "/") || cfg.RequiredRole == "" || cfg.HTTPTimeout <= 0 || cfg.TokenMaxAge <= 0 || cfg.ClockSkew < 0 || len(cfg.Algorithms) == 0 {
		return nil, errors.New("admin trust configuration is incomplete")
	}
	for _, algorithm := range cfg.Algorithms {
		if jwtHash(algorithm) == 0 {
			return nil, fmt.Errorf("admin algorithm %q is not allowed", algorithm)
		}
	}
	return &Authenticator{cfg: cfg, client: &http.Client{Timeout: cfg.HTTPTimeout}, keys: map[string]jwk{}}, nil
}

func (a *Authenticator) Authenticate(ctx context.Context, authorization string) (string, error) {
	const prefix = "Bearer "
	if !strings.HasPrefix(authorization, prefix) {
		return "", ErrInvalid
	}
	token := strings.TrimSpace(strings.TrimPrefix(authorization, prefix))
	parts := strings.Split(token, ".")
	if token == "" || len(parts) != 3 {
		return "", ErrInvalid
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
		Type      string `json:"typ"`
	}
	if decodePart(parts[0], &header) != nil || header.KeyID == "" || !containsAlgorithm(a.cfg.Algorithms, header.Algorithm) {
		return "", ErrInvalid
	}
	key, ok := a.key(header.KeyID)
	if !ok {
		if err := a.refresh(ctx); err != nil {
			return "", err
		}
		key, ok = a.key(header.KeyID)
	}
	if !ok || key.Kty != "RSA" || (key.Alg != "" && key.Alg != header.Algorithm) || (key.Use != "" && key.Use != "sig") {
		return "", ErrInvalid
	}
	publicKey, err := rsaJWK(key)
	if err != nil {
		return "", ErrInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", ErrInvalid
	}
	hash := jwtHash(header.Algorithm)
	digest := hash.New()
	_, _ = digest.Write([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(publicKey, hash, digest.Sum(nil), signature) != nil {
		return "", ErrInvalid
	}
	var claims map[string]any
	if decodePart(parts[1], &claims) != nil || !a.registeredClaims(claims, time.Now().UTC()) {
		return "", ErrInvalid
	}
	subject, ok := claims["sub"].(string)
	if !ok || subject == "" {
		return "", ErrInvalid
	}
	roles, err := valuesAtPointer(claims, a.cfg.RolePointer)
	if errors.Is(err, errClaimMissing) {
		return "", ErrRole
	}
	if err != nil {
		return "", ErrInvalid
	}
	for _, role := range roles {
		if role == a.cfg.RequiredRole {
			return subject, nil
		}
	}
	return "", ErrRole
}

func (a *Authenticator) registeredClaims(claims map[string]any, now time.Time) bool {
	issuer, ok := claims["iss"].(string)
	if !ok || issuer != a.cfg.Issuer || !audienceContains(claims["aud"], a.cfg.Audience) {
		return false
	}
	iat, iatOK := numericDate(claims["iat"])
	exp, expOK := numericDate(claims["exp"])
	skew := int64(a.cfg.ClockSkew / time.Second)
	current := now.Unix()
	return iatOK && expOK && iat <= current+skew && exp >= current-skew && current-iat <= int64(a.cfg.TokenMaxAge/time.Second)+skew
}
func (a *Authenticator) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.JWKSURL, nil)
	if err != nil {
		return ErrUnavailable
	}
	response, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: status %d", ErrUnavailable, response.StatusCode)
	}
	var document struct {
		Keys []jwk `json:"keys"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&document) != nil {
		return fmt.Errorf("%w: malformed JWKS", ErrUnavailable)
	}
	keys := make(map[string]jwk, len(document.Keys))
	for _, key := range document.Keys {
		if key.Kid != "" {
			keys[key.Kid] = key
		}
	}
	a.mu.Lock()
	a.keys = keys
	a.mu.Unlock()
	return nil
}
func (a *Authenticator) key(id string) (jwk, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	key, ok := a.keys[id]
	return key, ok
}
func decodePart(part string, target any) error {
	data, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
func jwtHash(algorithm string) crypto.Hash {
	switch algorithm {
	case "RS256":
		return crypto.SHA256
	case "RS384":
		return crypto.SHA384
	case "RS512":
		return crypto.SHA512
	}
	return 0
}
func containsAlgorithm(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
func numericDate(value any) (int64, bool) {
	number, ok := value.(float64)
	return int64(number), ok && number == float64(int64(number))
}
func audienceContains(value any, expected string) bool {
	if one, ok := value.(string); ok {
		return one == expected
	}
	many, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range many {
		if item == expected {
			return true
		}
	}
	return false
}
func rsaJWK(key jwk) (*rsa.PublicKey, error) {
	n, err := base64.RawURLEncoding.DecodeString(key.N)
	if err != nil || len(n) == 0 {
		return nil, ErrInvalid
	}
	exponent, err := base64.RawURLEncoding.DecodeString(key.E)
	if err != nil || len(exponent) == 0 || len(exponent) > 4 {
		return nil, ErrInvalid
	}
	e := 0
	for _, b := range exponent {
		e = e<<8 | int(b)
	}
	if e < 3 {
		return nil, ErrInvalid
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: e}, nil
}
func valuesAtPointer(claims map[string]any, pointer string) ([]string, error) {
	var current any = claims
	for _, raw := range strings.Split(pointer[1:], "/") {
		key := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return nil, ErrInvalid
		}
		current, ok = object[key]
		if !ok {
			return nil, errClaimMissing
		}
	}
	raw, ok := current.([]any)
	if !ok {
		return nil, ErrInvalid
	}
	result := make([]string, len(raw))
	for i, item := range raw {
		result[i], ok = item.(string)
		if !ok || result[i] == "" {
			return nil, ErrInvalid
		}
	}
	return result, nil
}
