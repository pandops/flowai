package jwtverify

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestVerifyRS256AndRefreshUnknownKey(t *testing.T) {
	t.Parallel()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	kid := "admin-key-1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		e := big.NewInt(int64(key.PublicKey.E)).Bytes()
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{
			"kid": kid, "kty": "RSA", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(e),
		}}})
	}))
	t.Cleanup(server.Close)
	now := time.Now().UTC().Truncate(time.Second)
	verifier, err := New(Config{Issuer: "https://admin.example", Audience: "flowai-admin", JWKSURL: server.URL, Algorithms: []string{"RS256"}, ClockSkew: time.Second, TokenMaxAge: time.Minute, HTTPTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	token := sign(t, key, kid, map[string]any{"iss": "https://admin.example", "sub": "admin-1", "aud": "flowai-admin", "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "roles": []string{"flowai-system-admin"}})
	claims, err := verifier.Verify(context.Background(), token, now)
	if err != nil {
		t.Fatal(err)
	}
	roles, err := ValuesAtPointer(claims, "/roles")
	if err != nil || len(roles) != 1 || roles[0] != "flowai-system-admin" {
		t.Fatalf("roles = %v, err = %v", roles, err)
	}
}

func TestVerifyRejectsClaimsAndAlgorithms(t *testing.T) {
	t.Parallel()
	if _, err := New(Config{Issuer: "https://admin.example", Audience: "admin", JWKSURL: "https://jwks.example", Algorithms: []string{"HS256"}, TokenMaxAge: time.Minute, HTTPTimeout: time.Second}); err == nil {
		t.Fatal("symmetric algorithm accepted")
	}
	if _, err := ValuesAtPointer(Claims{"roles": "admin"}, "/roles"); err == nil {
		t.Fatal("string role claim accepted")
	}
}

func TestValuesAtPointerDistinguishesMissingClaim(t *testing.T) {
	t.Parallel()
	if _, err := ValuesAtPointer(Claims{}, "/realm_access/roles"); !errors.Is(err, ErrClaimMissing) {
		t.Fatalf("ValuesAtPointer() error = %v, want ErrClaimMissing", err)
	}
}

func sign(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	headerData, _ := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid})
	claimsData, _ := json.Marshal(claims)
	header := base64.RawURLEncoding.EncodeToString(headerData)
	payload := base64.RawURLEncoding.EncodeToString(claimsData)
	input := header + "." + payload
	digest := sha256.Sum256([]byte(input))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}
