package workingtoken

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"
)

func TestSignerIssuesOneTeamDistinctTokens(t *testing.T) {
	t.Parallel()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyData, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := New(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyData}), "key-1", "issuer", "audience", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	signer.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	name := "Alpha"
	first, err := signer.Sign("operator-1", "team-alpha", &name)
	if err != nil {
		t.Fatal(err)
	}
	second, err := signer.Sign("operator-1", "team-alpha", &name)
	if err != nil {
		t.Fatal(err)
	}
	if first.JTI == second.JTI || first.AccessToken == second.AccessToken {
		t.Fatal("renewal reused token identity")
	}
	parts := strings.Split(first.AccessToken, ".")
	if len(parts) != 3 {
		t.Fatal("not compact JWT")
	}
	claimsData, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(claimsData, &claims); err != nil {
		t.Fatal(err)
	}
	if claims["team_id"] != "team-alpha" || claims["operator_id"] != "operator-1" || claims["team_name"] != "Alpha" {
		t.Fatalf("claims = %+v", claims)
	}
	if _, exists := claims["teams"]; exists {
		t.Fatal("multi-team claim present")
	}
}
