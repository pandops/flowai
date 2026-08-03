// Parser test cases for peerauth.Parse. Every test pins a single
// rule from the documented certificate subject shape; failures are
// localized to the rule they exercise.
package peerauth_test

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"net/http"
	"strings"
	"testing"

	"github.com/flowai/platform/state-registry/internal/peerauth"
)

func subject(OUs []string, O []string, CN, serial string) pkix.Name {
	return pkix.Name{
		OrganizationalUnit: OUs,
		Organization:       O,
		CommonName:         CN,
		SerialNumber:       serial,
	}
}

func certWithSubject(s pkix.Name) *x509.Certificate {
	return &x509.Certificate{Subject: s}
}

func TestParseRoles(t *testing.T) {
	cases := []struct {
		name   string
		role   peerauth.Role
		org    []string
		serial string
	}{
		{name: "admin", role: peerauth.RoleAdmin, org: nil, serial: ""},
		{name: "listener", role: peerauth.RoleListener, org: []string{"team-a"}, serial: "src-a"},
		{name: "team-executor", role: peerauth.RoleTeamExecutor, org: []string{"team-a"}, serial: ""},
		{name: "system-executor", role: peerauth.RoleSystemExecutor, org: nil, serial: ""},
		{name: "gateway", role: peerauth.RoleGateway, org: []string{"team-a"}, serial: ""},
	}
	for _, tc := range cases {
		t.Run(string(tc.role), func(t *testing.T) {
			s := subject([]string{string(tc.role)}, tc.org, "cn", tc.serial)
			c := certWithSubject(s)
			got := peerauth.Parse(c)
			if !got.Valid {
				t.Fatalf("Valid=false; reason=%q", got.Reason)
			}
			if got.Identity.Role != tc.role {
				t.Fatalf("role=%q, want %q", got.Identity.Role, tc.role)
			}
		})
	}
}

func TestParseRejectsUnknownRole(t *testing.T) {
	s := subject([]string{"wrong-role"}, []string{"team-a"}, "cn", "src-a")
	c := certWithSubject(s)
	got := peerauth.Parse(c)
	if got.Valid {
		t.Fatalf("valid=true for unknown role; identity=%+v", got.Identity)
	}
	if !strings.Contains(got.Reason, "not in the known set") {
		t.Fatalf("reason=%q", got.Reason)
	}
}

func TestParseRejectsMultipleOUs(t *testing.T) {
	s := subject([]string{"listener", "admin"}, []string{"team-a"}, "cn", "src-a")
	c := certWithSubject(s)
	got := peerauth.Parse(c)
	if got.Valid {
		t.Fatalf("valid=true; identity=%+v", got.Identity)
	}
	if !strings.Contains(got.Reason, "OrganizationalUnit") {
		t.Fatalf("reason=%q", got.Reason)
	}
}

func TestParseRejectsZeroOUs(t *testing.T) {
	s := subject(nil, []string{"team-a"}, "cn", "src-a")
	got := peerauth.Parse(certWithSubject(s))
	if got.Valid {
		t.Fatalf("valid=true for zero OUs")
	}
}

func TestParseRejectsMissingCN(t *testing.T) {
	s := subject([]string{"listener"}, []string{"team-a"}, "  ", "src-a")
	got := peerauth.Parse(certWithSubject(s))
	if got.Valid {
		t.Fatalf("valid=true for blank CN")
	}
	if !strings.Contains(got.Reason, "CN") {
		t.Fatalf("reason=%q", got.Reason)
	}
}

func TestParseRejectsMultipleOrganizations(t *testing.T) {
	s := subject([]string{"listener"}, []string{"team-a", "team-b"}, "cn", "src-a")
	got := peerauth.Parse(certWithSubject(s))
	if got.Valid {
		t.Fatalf("valid=true for multiple Organizations")
	}
	if !strings.Contains(got.Reason, "Organization") {
		t.Fatalf("reason=%q", got.Reason)
	}
}

func TestParseListenerRequiresTeamAndSerial(t *testing.T) {
	cases := []struct {
		name       string
		org        []string
		serial     string
		wantReason string
	}{
		{name: "no team", org: []string{}, serial: "src-a", wantReason: "team"},
		{name: "no serial", org: []string{"team-a"}, serial: "", wantReason: "source-system"},
		{name: "happy", org: []string{"team-a"}, serial: "src-a", wantReason: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := subject([]string{"listener"}, tc.org, "cn", tc.serial)
			got := peerauth.Parse(certWithSubject(s))
			if tc.wantReason == "" {
				if !got.Valid {
					t.Fatalf("expected valid; reason=%q", got.Reason)
				}
				if got.Identity.TeamID != "team-a" || got.Identity.SourceSystemID != "src-a" {
					t.Fatalf("identity=%+v", got.Identity)
				}
				return
			}
			if got.Valid {
				t.Fatalf("valid=true; reason=%q", got.Reason)
			}
			if !strings.Contains(got.Reason, tc.wantReason) {
				t.Fatalf("reason=%q, want containing %q", got.Reason, tc.wantReason)
			}
		})
	}
}

func TestParseTeamExecutorAndGatewayRejectSerial(t *testing.T) {
	for _, role := range []peerauth.Role{peerauth.RoleTeamExecutor, peerauth.RoleGateway} {
		t.Run(string(role), func(t *testing.T) {
			s := subject([]string{string(role)}, []string{"team-a"}, "cn", "src-a")
			got := peerauth.Parse(certWithSubject(s))
			if got.Valid {
				t.Fatalf("valid=true; identity=%+v", got.Identity)
			}
			if !strings.Contains(got.Reason, "source-system serial") {
				t.Fatalf("reason=%q", got.Reason)
			}
		})
	}
}

func TestParseAdminAndSystemExecutorRejectTeamAndSerial(t *testing.T) {
	for _, role := range []peerauth.Role{peerauth.RoleAdmin, peerauth.RoleSystemExecutor} {
		t.Run("with-team-and-serial", func(t *testing.T) {
			s := subject([]string{string(role)}, []string{"team-a"}, "cn", "src-a")
			got := peerauth.Parse(certWithSubject(s))
			if got.Valid {
				t.Fatalf("valid=true; identity=%+v", got.Identity)
			}
		})
		t.Run("happy", func(t *testing.T) {
			s := subject([]string{string(role)}, nil, "cn", "")
			got := peerauth.Parse(certWithSubject(s))
			if !got.Valid {
				t.Fatalf("valid=false; reason=%q", got.Reason)
			}
			if got.Identity.TeamID != "" || got.Identity.SourceSystemID != "" {
				t.Fatalf("identity=%+v, want no team/source", got.Identity)
			}
		})
	}
}

func TestApplyPromotesCanonicalHeaders(t *testing.T) {
	cases := []struct {
		role   peerauth.Role
		team   string
		serial string
		cn     string
		// expected headers written; "" means must not be set
		want map[string]string
	}{
		{
			role: peerauth.RoleAdmin,
			cn:   "admin-cn",
			want: map[string]string{
				"X-FlowAI-Role":          "admin",
				"X-FlowAI-Admin-Subject": "admin-cn",
			},
		},
		{
			role:   peerauth.RoleListener,
			team:   "team-a",
			serial: "source-a",
			cn:     "listener-cn",
			want: map[string]string{
				"X-FlowAI-Role":              "listener",
				"X-FlowAI-Team-Id":           "team-a",
				"X-FlowAI-Listener-Identity": "listener-cn",
				"X-FlowAI-Source-System-Id":  "source-a",
			},
		},
		{
			role: peerauth.RoleTeamExecutor,
			team: "team-a",
			cn:   "exec-cn",
			want: map[string]string{
				"X-FlowAI-Role":        "team-executor",
				"X-FlowAI-Team-Id":     "team-a",
				"X-FlowAI-Executor-Id": "exec-cn",
			},
		},
		{
			role: peerauth.RoleSystemExecutor,
			cn:   "exec-cn",
			want: map[string]string{
				"X-FlowAI-Role":        "system-executor",
				"X-FlowAI-Executor-Id": "exec-cn",
			},
		},
		{
			role: peerauth.RoleGateway,
			team: "team-a",
			cn:   "gateway-cn",
			want: map[string]string{
				"X-FlowAI-Role":        "gateway",
				"X-FlowAI-Team-Id":     "team-a",
				"X-FlowAI-Operator-Id": "gateway-cn",
			},
		},
	}
	for _, tc := range cases {
		t.Run(string(tc.role), func(t *testing.T) {
			s := subject([]string{string(tc.role)}, nilIfEmpty(tc.team), tc.cn, tc.serial)
			c := certWithSubject(s)
			claims := peerauth.Parse(c)
			if !claims.Valid {
				t.Fatalf("parse failed: %q", claims.Reason)
			}
			headers := http.Header{}
			claims.Apply(headers)
			// Every wanted value must be set.
			for k, v := range tc.want {
				if got := headers.Get(k); got != v {
					t.Errorf("%s=%q, want %q", k, got, v)
				}
			}
			// Headers NOT in the want map must not be set.
			allKeys := []string{
				"X-FlowAI-Role", "X-FlowAI-Team-Id", "X-FlowAI-Executor-Id",
				"X-FlowAI-Listener-Identity", "X-FlowAI-Source-System-Id",
				"X-FlowAI-Operator-Id", "X-FlowAI-Admin-Subject",
			}
			for _, k := range allKeys {
				if _, ok := tc.want[k]; ok {
					continue
				}
				if got := headers.Get(k); got != "" {
					t.Errorf("header %s=%q, want empty (role %s)", k, got, tc.role)
				}
			}
		})
	}
}

func TestApplyIsNoOpOnInvalidClaims(t *testing.T) {
	claims := peerauth.Claims{Valid: false}
	headers := http.Header{}
	claims.Apply(headers)
	if len(headers) != 0 {
		t.Fatalf("Apply on invalid claims wrote %v; want none", headers)
	}
}

func TestParseNilCertificateIsInvalid(t *testing.T) {
	got := peerauth.Parse(nil)
	if got.Valid {
		t.Fatalf("valid=true for nil certificate")
	}
}

func nilIfEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}
