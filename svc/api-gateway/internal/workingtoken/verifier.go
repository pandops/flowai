package workingtoken

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var ErrInvalid = errors.New("invalid working token")

type Claims struct {
	Issuer     string  `json:"iss"`
	Audience   string  `json:"aud"`
	Subject    string  `json:"sub"`
	OperatorID string  `json:"operator_id"`
	TeamID     string  `json:"team_id"`
	TeamName   *string `json:"team_name,omitempty"`
	IssuedAt   int64   `json:"iat"`
	ExpiresAt  int64   `json:"exp"`
	JTI        string  `json:"jti"`
}

func (s *Signer) Verify(token string, now time.Time) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, ErrInvalid
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
		Type      string `json:"typ"`
	}
	if err := decode(parts[0], &header); err != nil || header.Algorithm != "RS256" || header.KeyID != s.keyID || header.Type != "JWT" {
		return Claims{}, ErrInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Claims{}, ErrInvalid
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&s.key.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
		return Claims{}, ErrInvalid
	}
	var claims Claims
	if err := decode(parts[1], &claims); err != nil {
		return Claims{}, ErrInvalid
	}
	nowUnix := now.Unix()
	if claims.Issuer != s.issuer || claims.Audience != s.audience || claims.Subject == "" || claims.Subject != claims.OperatorID || claims.TeamID == "" || claims.JTI == "" || claims.IssuedAt > nowUnix+30 || claims.ExpiresAt <= nowUnix || claims.ExpiresAt <= claims.IssuedAt {
		return Claims{}, ErrInvalid
	}
	return claims, nil
}

func decode(part string, target any) error {
	data, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
