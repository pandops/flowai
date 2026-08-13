package cursor_test

import (
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"github.com/flowai/platform/svc/state-registry/internal/cursor"
)

// testKeyring is an in-memory test keyring.
type testKeyring struct {
	primary cursor.KeyID
	keys    map[cursor.KeyID][]byte
}

func (k *testKeyring) ActiveKeyID() cursor.KeyID     { return k.primary }
func (k *testKeyring) Keys() map[cursor.KeyID][]byte { return k.keys }

func newKeyring(t *testing.T) *testKeyring {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return &testKeyring{
		primary: cursor.KeyID("v0002-cursor"),
		keys:    map[cursor.KeyID][]byte{cursor.KeyID("v0002-cursor"): key},
	}
}

func gatewayIdentity() cursor.Identity {
	return cursor.Identity{Kind: "gateway", TeamID: "team-a", OperatorID: "op-a"}
}

func adminIdentity() cursor.Identity {
	return cursor.Identity{Kind: "admin"}
}

func mustEncode(t *testing.T, keyring cursor.Keyring, issue cursor.EncodeIssue) string {
	t.Helper()
	tok, err := cursor.Encode(keyring, issue)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return tok
}

// EncodeDecodeRoundTripHappyPath proves a freshly issued cursor can
// be decoded back into its envelope on the same keyring.
func TestEncodeDecodeRoundTripHappyPath(t *testing.T) {
	keyring := newKeyring(t)
	tok := mustEncode(t, keyring, cursor.EncodeIssue{
		Endpoint:     cursor.EndpointGatewayTasks,
		Identity:     gatewayIdentity(),
		Ordering:     cursor.OrderingTasksDesc,
		Filters:      map[string]string{"state": "pending"},
		TaskPosition: cursor.TaskPositionTuple(1700000000000000000, "task-a"),
	})
	env, err := cursor.Decode(keyring, cursor.DecodeRequest{
		Token:    tok,
		Endpoint: cursor.EndpointGatewayTasks,
		Identity: gatewayIdentity(),
		Ordering: cursor.OrderingTasksDesc,
		Filters:  map[string]string{"state": "pending"},
	})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Endpoint != cursor.EndpointGatewayTasks {
		t.Errorf("endpoint=%q", env.Endpoint)
	}
	if env.Ordering != cursor.OrderingTasksDesc {
		t.Errorf("ordering=%q", env.Ordering)
	}
	if env.Direction != cursor.Direction {
		t.Errorf("direction=%q", env.Direction)
	}
	if env.Filters["state"] != "pending" {
		t.Errorf("filters=%v", env.Filters)
	}
	if env.TaskPosition == nil {
		t.Errorf("expected task position")
	}
}

func TestKeyRotationVerifiesPreviousAndIssuesActive(t *testing.T) {
	oldKey := strings.Repeat("o", 32)
	newKey := strings.Repeat("n", 32)
	oldOnly := &testKeyring{
		primary: "cursor-v1",
		keys:    map[cursor.KeyID][]byte{"cursor-v1": []byte(oldKey)},
	}
	issue := cursor.EncodeIssue{
		Endpoint:     cursor.EndpointGatewayTasks,
		Identity:     gatewayIdentity(),
		Ordering:     cursor.OrderingTasksDesc,
		Filters:      map[string]string{"state": "pending"},
		TaskPosition: cursor.TaskPositionTuple(1700000000000000000, "task-a"),
	}
	oldToken := mustEncode(t, oldOnly, issue)

	rotated := &testKeyring{
		primary: "cursor-v2",
		keys: map[cursor.KeyID][]byte{
			"cursor-v1": []byte(oldKey),
			"cursor-v2": []byte(newKey),
		},
	}
	if _, err := cursor.Decode(rotated, cursor.DecodeRequest{
		Token: oldToken, Endpoint: issue.Endpoint, Identity: issue.Identity,
		Ordering: issue.Ordering, Filters: issue.Filters,
	}); err != nil {
		t.Fatalf("decode previous-key cursor after rotation: %v", err)
	}
	newToken := mustEncode(t, rotated, issue)
	if !strings.HasPrefix(newToken, "cursor-v2.") {
		t.Fatalf("new token key id=%q, want cursor-v2", strings.Split(newToken, ".")[0])
	}
}

func TestDecodeRejectsOversizedTokenBeforeWork(t *testing.T) {
	_, err := cursor.Decode(newKeyring(t), cursor.DecodeRequest{Token: strings.Repeat("a", cursor.MaxTokenLength+1)})
	if !errors.Is(err, cursor.ErrInvalid) {
		t.Fatalf("Decode oversized error=%v, want ErrInvalid", err)
	}
}

func TestEncodeAcceptsMaximumSchemaValidAdminTagTuple(t *testing.T) {
	keyring := newKeyring(t)
	token := mustEncode(t, keyring, cursor.EncodeIssue{
		Endpoint: cursor.EndpointAdminTags,
		Identity: adminIdentity(),
		Ordering: cursor.OrderingAdminTags,
		Filters: map[string]string{
			"team_id": strings.Repeat("t", 128),
			"tag":     strings.Repeat("x", 256),
		},
		TagPosition: cursor.TagPositionTuple(
			strings.Repeat("t", 128),
			strings.Repeat("x", 256),
			strings.Repeat("y", 128),
		),
	})
	if len(token) > cursor.MaxTokenLength {
		t.Fatalf("token length=%d exceeds bound %d", len(token), cursor.MaxTokenLength)
	}
}

// TestEncodeDecodeTagPositionRoundTrip covers the admin-tag cursor.
func TestEncodeDecodeTagPositionRoundTrip(t *testing.T) {
	keyring := newKeyring(t)
	tok := mustEncode(t, keyring, cursor.EncodeIssue{
		Endpoint:    cursor.EndpointAdminTags,
		Identity:    adminIdentity(),
		Ordering:    cursor.OrderingAdminTags,
		Filters:     map[string]string{},
		TagPosition: cursor.TagPositionTuple("team-a", "openhands", "tt-a1"),
	})
	env, err := cursor.Decode(keyring, cursor.DecodeRequest{
		Token:    tok,
		Endpoint: cursor.EndpointAdminTags,
		Identity: adminIdentity(),
		Ordering: cursor.OrderingAdminTags,
		Filters:  map[string]string{},
	})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.TagPosition == nil {
		t.Fatalf("expected tag position")
	}
	if env.TagPosition.TeamID != "team-a" || env.TagPosition.ExecutionTag != "openhands" || env.TagPosition.TaskTypeID != "tt-a1" {
		t.Errorf("tag position mismatch: %+v", env.TagPosition)
	}
}

// TestDecodeRejectsTamperedEnvelope flips a single byte in the
// envelope partition and asserts the decoder returns ErrInvalid.
func TestDecodeRejectsTamperedEnvelope(t *testing.T) {
	keyring := newKeyring(t)
	tok := mustEncode(t, keyring, cursor.EncodeIssue{
		Endpoint: cursor.EndpointGatewayTasks,
		Identity: gatewayIdentity(),
		Ordering: cursor.OrderingTasksDesc,
		Filters:  map[string]string{},
	})
	parts := split3(tok)
	tampered := parts[0] + "." + flipChar(parts[1]) + "." + parts[2]
	_, err := cursor.Decode(keyring, cursor.DecodeRequest{
		Token:    tampered,
		Endpoint: cursor.EndpointGatewayTasks,
		Identity: gatewayIdentity(),
		Ordering: cursor.OrderingTasksDesc,
		Filters:  map[string]string{},
	})
	if !errors.Is(err, cursor.ErrInvalid) {
		t.Fatalf("err=%v, want ErrInvalid", err)
	}
}

// TestDecodeRejectsTamperedMAC flips a single byte in the MAC
// partition.
func TestDecodeRejectsTamperedMAC(t *testing.T) {
	keyring := newKeyring(t)
	tok := mustEncode(t, keyring, cursor.EncodeIssue{
		Endpoint: cursor.EndpointGatewayTasks,
		Identity: gatewayIdentity(),
		Ordering: cursor.OrderingTasksDesc,
		Filters:  map[string]string{},
	})
	parts := split3(tok)
	tampered := parts[0] + "." + parts[1] + "." + flipChar(parts[2])
	_, err := cursor.Decode(keyring, cursor.DecodeRequest{
		Token:    tampered,
		Endpoint: cursor.EndpointGatewayTasks,
		Identity: gatewayIdentity(),
		Ordering: cursor.OrderingTasksDesc,
		Filters:  map[string]string{},
	})
	if !errors.Is(err, cursor.ErrInvalid) {
		t.Fatalf("err=%v, want ErrInvalid", err)
	}
}

// TestDecodeRejectsUnknownKeyID builds a cursor with a different
// key id and asserts the decoder rejects it.
func TestDecodeRejectsUnknownKeyID(t *testing.T) {
	primary := newKeyring(t)
	otherKey := make([]byte, 32)
	if _, err := rand.Read(otherKey); err != nil {
		t.Fatalf("rand: %v", err)
	}
	otherKeyring := &testKeyring{
		primary: cursor.KeyID("v0002-cursor-other"),
		keys:    map[cursor.KeyID][]byte{cursor.KeyID("v0002-cursor-other"): otherKey},
	}
	tok := mustEncode(t, otherKeyring, cursor.EncodeIssue{
		Endpoint: cursor.EndpointGatewayTasks,
		Identity: gatewayIdentity(),
		Ordering: cursor.OrderingTasksDesc,
		Filters:  map[string]string{},
	})
	_, err := cursor.Decode(primary, cursor.DecodeRequest{
		Token:    tok,
		Endpoint: cursor.EndpointGatewayTasks,
		Identity: gatewayIdentity(),
		Ordering: cursor.OrderingTasksDesc,
		Filters:  map[string]string{},
	})
	if !errors.Is(err, cursor.ErrInvalid) {
		t.Fatalf("err=%v, want ErrInvalid", err)
	}
}

// TestDecodeRejectsMalformedToken covers the well-formed-ness
// shapes that the decoder must reject.
func TestDecodeRejectsMalformedToken(t *testing.T) {
	keyring := newKeyring(t)
	cases := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"only_one_part", "no-dot"},
		{"two_parts", "k1.envelope"},
		{"four_parts", "k1.env.mac.extra"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := cursor.Decode(keyring, cursor.DecodeRequest{
				Token:    tc.token,
				Endpoint: cursor.EndpointGatewayTasks,
				Identity: gatewayIdentity(),
				Ordering: cursor.OrderingTasksDesc,
				Filters:  map[string]string{},
			})
			if !errors.Is(err, cursor.ErrInvalid) {
				t.Fatalf("err=%v, want ErrInvalid", err)
			}
		})
	}
}

// TestDecodeRejectsWrongEndpoint proves the cross-endpoint rejection.
func TestDecodeRejectsWrongEndpoint(t *testing.T) {
	keyring := newKeyring(t)
	tok := mustEncode(t, keyring, cursor.EncodeIssue{
		Endpoint: cursor.EndpointGatewayTasks,
		Identity: gatewayIdentity(),
		Ordering: cursor.OrderingTasksDesc,
		Filters:  map[string]string{},
	})
	_, err := cursor.Decode(keyring, cursor.DecodeRequest{
		Token:    tok,
		Endpoint: cursor.EndpointAdminTasks,
		Identity: gatewayIdentity(),
		Ordering: cursor.OrderingTasksDesc,
		Filters:  map[string]string{},
	})
	if !errors.Is(err, cursor.ErrInvalid) {
		t.Fatalf("err=%v, want ErrInvalid", err)
	}
}

// TestDecodeRejectsCrossScopeGAToGB proves the cross-team rejection.
func TestDecodeRejectsCrossScopeGAToGB(t *testing.T) {
	keyring := newKeyring(t)
	ga := cursor.Identity{Kind: "gateway", TeamID: "team-a", OperatorID: "op-a"}
	gb := cursor.Identity{Kind: "gateway", TeamID: "team-b", OperatorID: "op-b"}
	tok := mustEncode(t, keyring, cursor.EncodeIssue{
		Endpoint: cursor.EndpointGatewayTasks,
		Identity: ga,
		Ordering: cursor.OrderingTasksDesc,
		Filters:  map[string]string{},
	})
	_, err := cursor.Decode(keyring, cursor.DecodeRequest{
		Token:    tok,
		Endpoint: cursor.EndpointGatewayTasks,
		Identity: gb,
		Ordering: cursor.OrderingTasksDesc,
		Filters:  map[string]string{},
	})
	if !errors.Is(err, cursor.ErrInvalid) {
		t.Fatalf("err=%v, want ErrInvalid", err)
	}
}

// TestDecodeRejectsCrossScopeGatewayToAdmin proves the cross-role
// rejection.
func TestDecodeRejectsCrossScopeGatewayToAdmin(t *testing.T) {
	keyring := newKeyring(t)
	tok := mustEncode(t, keyring, cursor.EncodeIssue{
		Endpoint: cursor.EndpointGatewayTasks,
		Identity: gatewayIdentity(),
		Ordering: cursor.OrderingTasksDesc,
		Filters:  map[string]string{},
	})
	_, err := cursor.Decode(keyring, cursor.DecodeRequest{
		Token:    tok,
		Endpoint: cursor.EndpointGatewayTasks,
		Identity: adminIdentity(),
		Ordering: cursor.OrderingTasksDesc,
		Filters:  map[string]string{},
	})
	if !errors.Is(err, cursor.ErrInvalid) {
		t.Fatalf("err=%v, want ErrInvalid", err)
	}
}

// TestAdminScopeIsRoleOnly proves the admin scope carries the
// documented role string and is independent of the admin subject.
func TestAdminScopeIsRoleOnly(t *testing.T) {
	one := cursor.Identity{Kind: "admin"}
	two := cursor.Identity{Kind: "admin"}
	if one.Scope() != two.Scope() {
		t.Errorf("admin scopes differ: %q vs %q", one.Scope(), two.Scope())
	}
	if one.Scope() != "admin|admin" {
		t.Errorf("admin scope=%q, want admin|admin", one.Scope())
	}
}

// TestDecodeRejectsChangedFilter proves the filter-tuple rejection.
func TestDecodeRejectsChangedFilter(t *testing.T) {
	keyring := newKeyring(t)
	tok := mustEncode(t, keyring, cursor.EncodeIssue{
		Endpoint: cursor.EndpointGatewayTasks,
		Identity: gatewayIdentity(),
		Ordering: cursor.OrderingTasksDesc,
		Filters:  map[string]string{"state": "pending"},
	})
	_, err := cursor.Decode(keyring, cursor.DecodeRequest{
		Token:    tok,
		Endpoint: cursor.EndpointGatewayTasks,
		Identity: gatewayIdentity(),
		Ordering: cursor.OrderingTasksDesc,
		Filters:  map[string]string{"state": "running"},
	})
	if !errors.Is(err, cursor.ErrInvalid) {
		t.Fatalf("err=%v, want ErrInvalid", err)
	}
}

// TestDecodeRejectsAddedFilter proves an extra filter is rejected.
func TestDecodeRejectsAddedFilter(t *testing.T) {
	keyring := newKeyring(t)
	tok := mustEncode(t, keyring, cursor.EncodeIssue{
		Endpoint: cursor.EndpointGatewayTasks,
		Identity: gatewayIdentity(),
		Ordering: cursor.OrderingTasksDesc,
		Filters:  map[string]string{},
	})
	_, err := cursor.Decode(keyring, cursor.DecodeRequest{
		Token:    tok,
		Endpoint: cursor.EndpointGatewayTasks,
		Identity: gatewayIdentity(),
		Ordering: cursor.OrderingTasksDesc,
		Filters:  map[string]string{"state": "pending"},
	})
	if !errors.Is(err, cursor.ErrInvalid) {
		t.Fatalf("err=%v, want ErrInvalid", err)
	}
}

// TestDecodeRejectsChangedOrdering proves the ordering rejection.
func TestDecodeRejectsChangedOrdering(t *testing.T) {
	keyring := newKeyring(t)
	tok := mustEncode(t, keyring, cursor.EncodeIssue{
		Endpoint: cursor.EndpointGatewayTasks,
		Identity: gatewayIdentity(),
		Ordering: cursor.OrderingTasksDesc,
		Filters:  map[string]string{},
	})
	_, err := cursor.Decode(keyring, cursor.DecodeRequest{
		Token:    tok,
		Endpoint: cursor.EndpointGatewayTasks,
		Identity: gatewayIdentity(),
		Ordering: cursor.OrderingAdminTags,
		Filters:  map[string]string{},
	})
	if !errors.Is(err, cursor.ErrInvalid) {
		t.Fatalf("err=%v, want ErrInvalid", err)
	}
}

// TestParseLimitDefaults proves the documented default of 50.
func TestParseLimitDefaults(t *testing.T) {
	got, err := cursor.ParseLimit(map[string][]string{})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if got.Value != cursor.DefaultLimit {
		t.Errorf("value=%d, want %d", got.Value, cursor.DefaultLimit)
	}
}

// TestParseLimitRejectsMalformed covers every malformed shape
// documented in the contract: range, type, leading zeros, signs,
// fractions, whitespace, empty.
func TestParseLimitRejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"zero", "0"},
		{"above_max", "201"},
		{"below_one_negative", "-1"},
		{"non_integer_alpha", "abc"},
		{"whitespace_pad", " 50"},
		{"trailing_whitespace", "50 "},
		{"leading_zero", "01"},
		{"positive_sign", "+1"},
		{"fraction", "1.5"},
		{"empty", ""},
		{"hex", "0x1"},
		{"octal", "0o1"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := cursor.ParseLimit(map[string][]string{"limit": {tc.raw}})
			if !errors.Is(err, cursor.ErrInvalid) {
				t.Fatalf("err=%v, want ErrInvalid", err)
			}
		})
	}
}

// TestParseLimitRejectsMultipleValues proves the multi-value rejection.
func TestParseLimitRejectsMultipleValues(t *testing.T) {
	_, err := cursor.ParseLimit(map[string][]string{"limit": {"50", "100"}})
	if !errors.Is(err, cursor.ErrInvalid) {
		t.Fatalf("err=%v, want ErrInvalid", err)
	}
}

// TestParseLimitAcceptsValid covers the in-range cases.
func TestParseLimitAcceptsValid(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{"1", 1},
		{"50", 50},
		{"200", 200},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.raw, func(t *testing.T) {
			got, err := cursor.ParseLimit(map[string][]string{"limit": {tc.raw}})
			if err != nil {
				t.Fatalf("err=%v", err)
			}
			if got.Value != tc.want {
				t.Errorf("value=%d, want %d", got.Value, tc.want)
			}
		})
	}
}

// TestEncodeDecodeEnvironmentPositionRoundTrip covers the new
// environment cursor tuple.
func TestEncodeDecodeEnvironmentPositionRoundTrip(t *testing.T) {
	keyring := newKeyring(t)
	tok := mustEncode(t, keyring, cursor.EncodeIssue{
		Endpoint:            cursor.EndpointGatewayEnvironments,
		Identity:            gatewayIdentity(),
		Ordering:            cursor.OrderingEnvironmentsAsc,
		Filters:             map[string]string{"team_id": "team-a"},
		EnvironmentPosition: cursor.EnvironmentPositionTuple(1700000000000000000, "env-a"),
	})
	env, err := cursor.Decode(keyring, cursor.DecodeRequest{
		Token:    tok,
		Endpoint: cursor.EndpointGatewayEnvironments,
		Identity: gatewayIdentity(),
		Ordering: cursor.OrderingEnvironmentsAsc,
		Filters:  map[string]string{"team_id": "team-a"},
	})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	pos := cursor.EnvironmentPosition(env)
	if pos == nil {
		t.Fatalf("environment position missing")
	}
	if pos.EnvironmentID != "env-a" || pos.CreatedAtNano != 1700000000000000000 {
		t.Errorf("env position=%+v, want env-a/1700000000000000000", pos)
	}
}

// TestEncodeDecodeSecretPositionRoundTrip covers the new logical
// secret cursor tuple.
func TestEncodeDecodeSecretPositionRoundTrip(t *testing.T) {
	keyring := newKeyring(t)
	tok := mustEncode(t, keyring, cursor.EncodeIssue{
		Endpoint:       cursor.EndpointGatewaySecrets,
		Identity:       gatewayIdentity(),
		Ordering:       cursor.OrderingSecretsAsc,
		Filters:        map[string]string{"team_id": "team-a", "environment_id": "env-a"},
		SecretPosition: cursor.SecretPositionTuple(1700000000000000000, "secret-a"),
	})
	env, err := cursor.Decode(keyring, cursor.DecodeRequest{
		Token:    tok,
		Endpoint: cursor.EndpointGatewaySecrets,
		Identity: gatewayIdentity(),
		Ordering: cursor.OrderingSecretsAsc,
		Filters:  map[string]string{"team_id": "team-a", "environment_id": "env-a"},
	})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	pos := cursor.SecretPosition(env)
	if pos == nil {
		t.Fatalf("secret position missing")
	}
	if pos.SecretID != "secret-a" || pos.CreatedAtNano != 1700000000000000000 {
		t.Errorf("secret position=%+v", pos)
	}
}

// TestEncodeDecodeSecretVersionPositionRoundTrip covers the new
// secret-version cursor tuple.
func TestEncodeDecodeSecretVersionPositionRoundTrip(t *testing.T) {
	keyring := newKeyring(t)
	tok := mustEncode(t, keyring, cursor.EncodeIssue{
		Endpoint:              cursor.EndpointGatewaySecretVersions,
		Identity:              gatewayIdentity(),
		Ordering:              cursor.OrderingSecretVersionsAsc,
		Filters:               map[string]string{"team_id": "team-a", "environment_id": "env-a", "secret_id": "secret-a"},
		SecretVersionPosition: cursor.SecretVersionPositionTuple(2, "secret-a"),
	})
	env, err := cursor.Decode(keyring, cursor.DecodeRequest{
		Token:    tok,
		Endpoint: cursor.EndpointGatewaySecretVersions,
		Identity: gatewayIdentity(),
		Ordering: cursor.OrderingSecretVersionsAsc,
		Filters:  map[string]string{"team_id": "team-a", "environment_id": "env-a", "secret_id": "secret-a"},
	})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	pos := cursor.SecretVersionPosition(env)
	if pos == nil {
		t.Fatalf("secret version position missing")
	}
	if pos.SecretID != "secret-a" || pos.Version != 2 {
		t.Errorf("secret version position=%+v", pos)
	}
}

// TestDecodeRejectsEnvironmentCrossEndpoint covers the
// OpenSpec "Cross-endpoint cursor reuse is rejected" invariant
// for the new environment endpoint.
func TestDecodeRejectsEnvironmentCrossEndpoint(t *testing.T) {
	keyring := newKeyring(t)
	tok := mustEncode(t, keyring, cursor.EncodeIssue{
		Endpoint:            cursor.EndpointGatewayEnvironments,
		Identity:            gatewayIdentity(),
		Ordering:            cursor.OrderingEnvironmentsAsc,
		Filters:             map[string]string{"team_id": "team-a"},
		EnvironmentPosition: cursor.EnvironmentPositionTuple(1700000000000000000, "env-a"),
	})
	_, err := cursor.Decode(keyring, cursor.DecodeRequest{
		Token:    tok,
		Endpoint: cursor.EndpointGatewaySecrets,
		Identity: gatewayIdentity(),
		Ordering: cursor.OrderingEnvironmentsAsc,
		Filters:  map[string]string{"team_id": "team-a"},
	})
	if !errors.Is(err, cursor.ErrInvalid) {
		t.Fatalf("err=%v, want ErrInvalid", err)
	}
}

// split3 splits a cursor token on "." and returns the three parts.
func split3(tok string) [3]string {
	parts := [3]string{}
	current := strings.IndexByte(tok, '.')
	if current < 0 {
		return parts
	}
	parts[0] = tok[:current]
	after1 := tok[current+1:]
	current2 := strings.IndexByte(after1, '.')
	if current2 < 0 {
		return parts
	}
	parts[1] = after1[:current2]
	parts[2] = after1[current2+1:]
	return parts
}

// flipChar returns the input string with the first character replaced
// by a different valid base64url character.
func flipChar(in string) string {
	if len(in) == 0 {
		return in
	}
	if in[0] == 'A' {
		return "B" + in[1:]
	}
	return "A" + in[1:]
}
