package workingtoken

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"testing"
	"time"
)

func TestVerifyRejectsEveryUnusableCanonicalContext(t *testing.T) {
	t.Parallel()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: mustPKCS8(t, key)})
	signer, err := New(pemKey, "key-1", "https://gateway.example", "registry", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	base := map[string]any{"iss": "https://gateway.example", "aud": "registry", "sub": "operator-1", "operator_id": "operator-1", "team_id": "team-1", "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "jti": "jti-1"}
	tests := []struct {
		name   string
		header map[string]any
		mutate func(map[string]any)
		key    *rsa.PrivateKey
	}{
		{"wrong algorithm", map[string]any{"alg": "RS512", "kid": "key-1", "typ": "JWT"}, nil, key},
		{"wrong key id", map[string]any{"alg": "RS256", "kid": "other", "typ": "JWT"}, nil, key},
		{"wrong issuer", nil, func(c map[string]any) { c["iss"] = "other" }, key},
		{"wrong audience", nil, func(c map[string]any) { c["aud"] = "other" }, key},
		{"missing operator", nil, func(c map[string]any) { delete(c, "operator_id") }, key},
		{"subject mismatch", nil, func(c map[string]any) { c["sub"] = "other" }, key},
		{"missing team", nil, func(c map[string]any) { delete(c, "team_id") }, key},
		{"multi team wrong shape", nil, func(c map[string]any) { c["team_id"] = []string{"team-1", "team-2"} }, key},
		{"missing jti", nil, func(c map[string]any) { delete(c, "jti") }, key},
		{"future issued", nil, func(c map[string]any) { c["iat"] = now.Add(time.Minute).Unix() }, key},
		{"expired", nil, func(c map[string]any) { c["exp"] = now.Unix() }, key},
		{"bad signature", nil, nil, mustRSAKey(t)},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			claims := cloneClaims(base)
			if tc.mutate != nil {
				tc.mutate(claims)
			}
			header := tc.header
			if header == nil {
				header = map[string]any{"alg": "RS256", "kid": "key-1", "typ": "JWT"}
			}
			if _, err := signer.Verify(signClaims(t, tc.key, header, claims), now); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func mustPKCS8(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	b, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func mustRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}
func cloneClaims(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for k, v := range input {
		output[k] = v
	}
	return output
}
func signClaims(t *testing.T, key *rsa.PrivateKey, header, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(header)
	c, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}
