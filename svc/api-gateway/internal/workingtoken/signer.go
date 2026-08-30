// Package workingtoken issues short-lived asymmetric Gateway bearer tokens.
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
	"time"
)

type Signer struct {
	key                     *rsa.PrivateKey
	keyID, issuer, audience string
	ttl                     time.Duration
	now                     func() time.Time
}
type Token struct {
	AccessToken         string
	ExpiresIn           int
	JTI                 string
	IssuedAt, ExpiresAt time.Time
}

func New(privateKeyPEM []byte, keyID, issuer, audience string, ttl time.Duration) (*Signer, error) {
	block, _ := pem.Decode(privateKeyPEM)
	if block == nil {
		return nil, errors.New("working-token private key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("working-token private key is not PKCS8")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok || key.N.BitLen() < 2048 || keyID == "" || issuer == "" || audience == "" || ttl <= 0 {
		return nil, errors.New("invalid working-token signing configuration")
	}
	return &Signer{key: key, keyID: keyID, issuer: issuer, audience: audience, ttl: ttl, now: time.Now}, nil
}

func (s *Signer) Sign(operatorID, teamID string, teamName *string) (Token, error) {
	if operatorID == "" || teamID == "" {
		return Token{}, errors.New("operator and team are required")
	}
	jtiBytes := make([]byte, 16)
	if _, err := rand.Read(jtiBytes); err != nil {
		return Token{}, err
	}
	jti := base64.RawURLEncoding.EncodeToString(jtiBytes)
	now := s.now().UTC().Truncate(time.Second)
	expires := now.Add(s.ttl)
	header, _ := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT", "kid": s.keyID})
	claims := map[string]any{"iss": s.issuer, "aud": s.audience, "sub": operatorID, "operator_id": operatorID, "team_id": teamID, "iat": now.Unix(), "exp": expires.Unix(), "jti": jti}
	if teamName != nil {
		claims["team_name"] = *teamName
	}
	payload, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(input))
	signature, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, digest[:])
	if err != nil {
		return Token{}, err
	}
	return Token{AccessToken: input + "." + base64.RawURLEncoding.EncodeToString(signature), ExpiresIn: int(s.ttl.Seconds()), JTI: jti, IssuedAt: now, ExpiresAt: expires}, nil
}
