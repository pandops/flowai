// Package config loads API Gateway process configuration.
package config

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultPort = 8080

type Config struct {
	BindHost                  string
	BindPort                  int
	OIDCIssuer                string
	OIDCClientID              string
	OIDCRedirectURI           string
	OIDCEndpointTimeout       time.Duration
	OIDCTeamClaimAdapter      string
	OIDCTeamClaimPointer      string
	OIDCTeamClaimSource       string
	OIDCTeamClaimIDField      string
	OIDCTeamClaimNameField    string
	OIDCAdditionalScopes      []string
	MembershipMaxAge          time.Duration
	PostgresURL               string
	MigrationPostgresURL      string
	SessionKey                []byte
	SessionTTL                time.Duration
	WebUIURL                  string
	StateRegistryURL          string
	AdminIssuer               string
	AdminAudience             string
	AdminJWKSURL              string
	AdminRolePointer          string
	AdminAlgorithms           []string
	AdminJWKSTimeout          time.Duration
	AdminTokenMaxAge          time.Duration
	AdminClockSkew            time.Duration
	WorkingTokenPrivateKeyPEM []byte
	WorkingTokenKeyID         string
	WorkingTokenIssuer        string
	WorkingTokenAudience      string
	WorkingTokenTTL           time.Duration
}

func Load() (Config, error) {
	cfg := Config{BindHost: os.Getenv("API_GATEWAY_BIND_HOST"), BindPort: defaultPort}
	if cfg.BindHost == "" {
		cfg.BindHost = "0.0.0.0"
	}
	if raw := os.Getenv("API_GATEWAY_BIND_PORT"); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port < 0 || port > 65535 {
			return Config{}, fmt.Errorf("API_GATEWAY_BIND_PORT must be an integer from 0 to 65535")
		}
		cfg.BindPort = port
	}
	cfg.OIDCIssuer = os.Getenv("API_GATEWAY_OIDC_ISSUER")
	cfg.OIDCClientID = os.Getenv("API_GATEWAY_OIDC_CLIENT_ID")
	cfg.OIDCRedirectURI = os.Getenv("API_GATEWAY_OIDC_REDIRECT_URI")
	cfg.OIDCTeamClaimAdapter = os.Getenv("API_GATEWAY_OIDC_TEAM_CLAIM_ADAPTER")
	cfg.OIDCTeamClaimPointer = os.Getenv("API_GATEWAY_OIDC_TEAM_CLAIM_POINTER")
	cfg.OIDCTeamClaimSource = os.Getenv("API_GATEWAY_OIDC_TEAM_CLAIM_SOURCE")
	cfg.OIDCTeamClaimIDField = os.Getenv("API_GATEWAY_OIDC_TEAM_CLAIM_ID_FIELD")
	cfg.OIDCTeamClaimNameField = os.Getenv("API_GATEWAY_OIDC_TEAM_CLAIM_NAME_FIELD")
	for _, scope := range strings.Split(os.Getenv("API_GATEWAY_OIDC_ADDITIONAL_SCOPES"), ",") {
		if scope = strings.TrimSpace(scope); scope != "" {
			cfg.OIDCAdditionalScopes = append(cfg.OIDCAdditionalScopes, scope)
		}
	}
	cfg.MembershipMaxAge = 5 * time.Minute
	if raw := os.Getenv("API_GATEWAY_MEMBERSHIP_MAX_AGE"); raw != "" {
		value, err := time.ParseDuration(raw)
		if err != nil || value < 0 {
			return Config{}, fmt.Errorf("API_GATEWAY_MEMBERSHIP_MAX_AGE must be a non-negative duration")
		}
		cfg.MembershipMaxAge = value
	}
	cfg.PostgresURL = os.Getenv("API_GATEWAY_POSTGRES_URL")
	cfg.MigrationPostgresURL = os.Getenv("API_GATEWAY_MIGRATION_POSTGRES_URL")
	if cfg.MigrationPostgresURL == "" {
		cfg.MigrationPostgresURL = cfg.PostgresURL
	}
	cfg.WebUIURL = os.Getenv("API_GATEWAY_WEB_UI_URL")
	cfg.StateRegistryURL = os.Getenv("API_GATEWAY_STATE_REGISTRY_URL")
	cfg.AdminIssuer = os.Getenv("API_GATEWAY_ADMIN_ISSUER")
	cfg.AdminAudience = os.Getenv("API_GATEWAY_ADMIN_AUDIENCE")
	cfg.AdminJWKSURL = os.Getenv("API_GATEWAY_ADMIN_JWKS_URL")
	cfg.AdminRolePointer = os.Getenv("API_GATEWAY_ADMIN_ROLE_CLAIM_POINTER")
	cfg.AdminAlgorithms = splitNonEmpty(os.Getenv("API_GATEWAY_ADMIN_ALGORITHMS"))
	if len(cfg.AdminAlgorithms) == 0 {
		cfg.AdminAlgorithms = []string{"RS256"}
	}
	cfg.AdminJWKSTimeout = 5 * time.Second
	cfg.AdminTokenMaxAge = 5 * time.Minute
	cfg.AdminClockSkew = 30 * time.Second
	cfg.WorkingTokenKeyID = os.Getenv("API_GATEWAY_WORKING_TOKEN_KEY_ID")
	cfg.WorkingTokenIssuer = os.Getenv("API_GATEWAY_WORKING_TOKEN_ISSUER")
	cfg.WorkingTokenAudience = os.Getenv("API_GATEWAY_WORKING_TOKEN_AUDIENCE")
	cfg.WorkingTokenTTL = 5 * time.Minute
	if encoded := os.Getenv("API_GATEWAY_WORKING_TOKEN_PRIVATE_KEY_BASE64"); encoded != "" {
		value, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return Config{}, fmt.Errorf("API_GATEWAY_WORKING_TOKEN_PRIVATE_KEY_BASE64 is invalid")
		}
		cfg.WorkingTokenPrivateKeyPEM = value
	}
	if raw := os.Getenv("API_GATEWAY_WORKING_TOKEN_TTL"); raw != "" {
		value, err := time.ParseDuration(raw)
		if err != nil || value <= 0 {
			return Config{}, fmt.Errorf("API_GATEWAY_WORKING_TOKEN_TTL must be positive duration")
		}
		cfg.WorkingTokenTTL = value
	}
	if raw := os.Getenv("API_GATEWAY_ADMIN_JWKS_TIMEOUT"); raw != "" {
		value, err := time.ParseDuration(raw)
		if err != nil || value <= 0 {
			return Config{}, fmt.Errorf("API_GATEWAY_ADMIN_JWKS_TIMEOUT must be positive duration")
		}
		cfg.AdminJWKSTimeout = value
	}
	if raw := os.Getenv("API_GATEWAY_ADMIN_TOKEN_MAX_AGE"); raw != "" {
		value, err := time.ParseDuration(raw)
		if err != nil || value <= 0 {
			return Config{}, fmt.Errorf("API_GATEWAY_ADMIN_TOKEN_MAX_AGE must be positive duration")
		}
		cfg.AdminTokenMaxAge = value
	}
	if raw := os.Getenv("API_GATEWAY_ADMIN_CLOCK_SKEW"); raw != "" {
		value, err := time.ParseDuration(raw)
		if err != nil || value < 0 {
			return Config{}, fmt.Errorf("API_GATEWAY_ADMIN_CLOCK_SKEW must be non-negative duration")
		}
		cfg.AdminClockSkew = value
	}
	cfg.SessionTTL = time.Hour
	if raw := os.Getenv("API_GATEWAY_SESSION_TTL"); raw != "" {
		duration, err := time.ParseDuration(raw)
		if err != nil || duration <= 0 {
			return Config{}, fmt.Errorf("API_GATEWAY_SESSION_TTL must be positive duration")
		}
		cfg.SessionTTL = duration
	}
	if raw := os.Getenv("API_GATEWAY_OIDC_ENDPOINT_TIMEOUT"); raw != "" {
		duration, err := time.ParseDuration(raw)
		if err != nil || duration <= 0 {
			return Config{}, fmt.Errorf("API_GATEWAY_OIDC_ENDPOINT_TIMEOUT must be positive duration")
		}
		cfg.OIDCEndpointTimeout = duration
	} else {
		cfg.OIDCEndpointTimeout = 5 * time.Second
	}
	configured := cfg.OIDCIssuer != "" || cfg.OIDCClientID != "" || cfg.OIDCRedirectURI != "" || cfg.OIDCTeamClaimAdapter != "" || cfg.OIDCTeamClaimPointer != "" || cfg.OIDCTeamClaimSource != ""
	if configured && (cfg.OIDCIssuer == "" || cfg.OIDCClientID == "" || cfg.OIDCRedirectURI == "" || cfg.OIDCTeamClaimAdapter == "" || cfg.OIDCTeamClaimPointer == "" || (cfg.OIDCTeamClaimSource != "id_token" && cfg.OIDCTeamClaimSource != "userinfo")) {
		return Config{}, fmt.Errorf("OIDC issuer, client ID, redirect URI, team adapter, claim pointer, and valid claim source must be configured together")
	}
	if configured {
		if cfg.PostgresURL == "" {
			return Config{}, fmt.Errorf("API_GATEWAY_POSTGRES_URL is required with OIDC")
		}
		key, err := hex.DecodeString(os.Getenv("API_GATEWAY_SESSION_KEY_HEX"))
		if err != nil || len(key) != 32 {
			return Config{}, fmt.Errorf("API_GATEWAY_SESSION_KEY_HEX must decode to exactly 32 bytes")
		}
		cfg.SessionKey = key
		if len(cfg.WorkingTokenPrivateKeyPEM) == 0 || cfg.WorkingTokenKeyID == "" || cfg.WorkingTokenIssuer == "" || cfg.WorkingTokenAudience == "" {
			return Config{}, fmt.Errorf("working-token private key, key ID, issuer, and audience are required with OIDC")
		}
	}
	adminConfigured := cfg.AdminIssuer != "" || cfg.AdminAudience != "" || cfg.AdminJWKSURL != "" || cfg.AdminRolePointer != "" || cfg.StateRegistryURL != ""
	if adminConfigured && (cfg.AdminIssuer == "" || cfg.AdminAudience == "" || cfg.AdminJWKSURL == "" || cfg.AdminRolePointer == "" || cfg.StateRegistryURL == "") {
		return Config{}, fmt.Errorf("admin issuer, audience, JWKS URL, role pointer, and State Registry URL must be configured together")
	}
	for _, algorithm := range cfg.AdminAlgorithms {
		if algorithm != "RS256" && algorithm != "RS384" && algorithm != "RS512" {
			return Config{}, fmt.Errorf("API_GATEWAY_ADMIN_ALGORITHMS must contain only RS256, RS384, or RS512")
		}
	}
	return cfg, nil
}

func splitNonEmpty(raw string) []string {
	var values []string
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func (c Config) BindAddress() string {
	return net.JoinHostPort(c.BindHost, strconv.Itoa(c.BindPort))
}
