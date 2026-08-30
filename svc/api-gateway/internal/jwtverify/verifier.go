// Package jwtverify validates provider-neutral asymmetric JWTs against JWKS.
package jwtverify

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
	ErrInvalidToken    = errors.New("invalid token")
	ErrJWKSUnavailable = errors.New("JWKS unavailable")
	ErrClaimMissing    = errors.New("claim missing")
)

type Config struct {
	Issuer      string
	Audience    string
	JWKSURL     string
	Algorithms  []string
	ClockSkew   time.Duration
	TokenMaxAge time.Duration
	HTTPTimeout time.Duration
}

type Claims map[string]any

type Verifier struct {
	cfg    Config
	client *http.Client
	mu     sync.RWMutex
	keys   map[string]jwk
}

type jwk struct{ Kid, Kty, Alg, Use, N, E string }
type jwksDocument struct {
	Keys []jwk `json:"keys"`
}
type header struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Type      string `json:"typ"`
}

func New(cfg Config) (*Verifier, error) {
	issuer, issuerErr := url.Parse(cfg.Issuer)
	jwksURL, jwksErr := url.Parse(cfg.JWKSURL)
	if issuerErr != nil || issuer.Scheme == "" || issuer.Host == "" {
		return nil, errors.New("JWT issuer must be absolute")
	}
	if jwksErr != nil || jwksURL.Scheme == "" || jwksURL.Host == "" {
		return nil, errors.New("JWKS URL must be absolute")
	}
	if cfg.Audience == "" || cfg.HTTPTimeout <= 0 || cfg.TokenMaxAge <= 0 || cfg.ClockSkew < 0 {
		return nil, errors.New("invalid JWT trust timing or audience")
	}
	if len(cfg.Algorithms) == 0 {
		return nil, errors.New("JWT algorithm allowlist is required")
	}
	for _, algorithm := range cfg.Algorithms {
		if hashFor(algorithm) == 0 {
			return nil, fmt.Errorf("algorithm %q is not an allowed asymmetric RSA algorithm", algorithm)
		}
	}
	return &Verifier{cfg: cfg, client: &http.Client{Timeout: cfg.HTTPTimeout}, keys: make(map[string]jwk)}, nil
}

func (v *Verifier) Verify(ctx context.Context, token string, now time.Time) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}
	var tokenHeader header
	if err := decodeJSONPart(parts[0], &tokenHeader); err != nil || tokenHeader.KeyID == "" || !contains(v.cfg.Algorithms, tokenHeader.Algorithm) {
		return nil, ErrInvalidToken
	}
	key, ok := v.cachedKey(tokenHeader.KeyID)
	if !ok {
		if err := v.refresh(ctx); err != nil {
			return nil, err
		}
		key, ok = v.cachedKey(tokenHeader.KeyID)
		if !ok {
			return nil, ErrInvalidToken
		}
	}
	if key.Kty != "RSA" || (key.Alg != "" && key.Alg != tokenHeader.Algorithm) || (key.Use != "" && key.Use != "sig") {
		return nil, ErrInvalidToken
	}
	publicKey, err := rsaKey(key)
	if err != nil {
		return nil, ErrInvalidToken
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, ErrInvalidToken
	}
	hashID := hashFor(tokenHeader.Algorithm)
	hasher := hashID.New()
	_, _ = hasher.Write([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(publicKey, hashID, hasher.Sum(nil), signature); err != nil {
		return nil, ErrInvalidToken
	}
	var claims Claims
	if err := decodeJSONPart(parts[1], &claims); err != nil {
		return nil, ErrInvalidToken
	}
	if !v.validRegisteredClaims(claims, now) {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

func (v *Verifier) refresh(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, v.cfg.JWKSURL, nil)
	if err != nil {
		return ErrJWKSUnavailable
	}
	response, err := v.client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrJWKSUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: status %d", ErrJWKSUnavailable, response.StatusCode)
	}
	var document jwksDocument
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&document); err != nil {
		return fmt.Errorf("%w: malformed response", ErrJWKSUnavailable)
	}
	keys := make(map[string]jwk, len(document.Keys))
	for _, key := range document.Keys {
		if key.Kid != "" {
			keys[key.Kid] = key
		}
	}
	v.mu.Lock()
	v.keys = keys
	v.mu.Unlock()
	return nil
}

func (v *Verifier) cachedKey(kid string) (jwk, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	key, ok := v.keys[kid]
	return key, ok
}

func (v *Verifier) validRegisteredClaims(claims Claims, now time.Time) bool {
	issuer, ok := claims["iss"].(string)
	if !ok || issuer != v.cfg.Issuer {
		return false
	}
	if !audienceContains(claims["aud"], v.cfg.Audience) {
		return false
	}
	iat, iatOK := numericDate(claims["iat"])
	exp, expOK := numericDate(claims["exp"])
	if !iatOK || !expOK {
		return false
	}
	nowUnix := now.Unix()
	skew := int64(v.cfg.ClockSkew / time.Second)
	if iat > nowUnix+skew || exp < nowUnix-skew || nowUnix-iat > int64(v.cfg.TokenMaxAge/time.Second)+skew {
		return false
	}
	return true
}

func rsaKey(key jwk) (*rsa.PublicKey, error) {
	n, err := base64.RawURLEncoding.DecodeString(key.N)
	if err != nil || len(n) == 0 {
		return nil, errors.New("invalid modulus")
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(key.E)
	if err != nil || len(eBytes) == 0 || len(eBytes) > 4 {
		return nil, errors.New("invalid exponent")
	}
	e := 0
	for _, value := range eBytes {
		e = e<<8 | int(value)
	}
	if e < 3 {
		return nil, errors.New("invalid exponent")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: e}, nil
}

func decodeJSONPart(part string, target any) error {
	data, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
func hashFor(algorithm string) crypto.Hash {
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
func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
func numericDate(value any) (int64, bool) {
	number, ok := value.(float64)
	if !ok || number != float64(int64(number)) {
		return 0, false
	}
	return int64(number), true
}
func audienceContains(value any, expected string) bool {
	if audience, ok := value.(string); ok {
		return audience == expected
	}
	values, ok := value.([]any)
	if !ok {
		return false
	}
	for _, value := range values {
		if audience, ok := value.(string); ok && audience == expected {
			return true
		}
	}
	return false
}

func ValuesAtPointer(claims Claims, pointer string) ([]string, error) {
	if pointer == "" || !strings.HasPrefix(pointer, "/") {
		return nil, errors.New("role pointer must be a non-empty RFC 6901 pointer")
	}
	var current any = map[string]any(claims)
	for _, raw := range strings.Split(pointer[1:], "/") {
		token := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return nil, ErrInvalidToken
		}
		current, ok = object[token]
		if !ok {
			return nil, ErrClaimMissing
		}
	}
	values, ok := current.([]any)
	if !ok {
		return nil, ErrInvalidToken
	}
	result := make([]string, len(values))
	for i, value := range values {
		result[i], ok = value.(string)
		if !ok || result[i] == "" {
			return nil, ErrInvalidToken
		}
	}
	return result, nil
}
