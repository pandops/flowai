// Package cursor provides the State Registry HMAC-protected opaque cursor
// encoder/decoder and the matching limit-validation helpers. The contract
// under test is the integrity-protected cursor documented in
// `openspec/changes/v0002-state-registry/specs/state-registry/spec.md`:
//
//   - Each cursor is bound to the originating endpoint, the authenticated
//     authorization scope at the time of issue, the complete active filter
//     tuple, the deterministic resource-appropriate ordering, and the
//     forward-only page direction.
//   - The integrity check is HMAC-SHA-256 under a server-controlled rotating
//     key identified by a `key_id`. Unknown or revoked key IDs fail closed.
//   - MAC verification uses hmac.Equal (constant-time) before any other
//     check so byte-level tampering is rejected without revealing which
//     field mismatched.
//   - The same non-revealing `400 invalid_pagination` shape is returned
//     for every rejection and no cursor bytes, MAC bytes, key material,
//     or filter values are logged.
//
// The package deliberately uses only the Go standard library.
package cursor

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Direction is the page direction encoded in every cursor. The contract
// permits forward-only cursors; backward cursors are rejected at decode.
const Direction = "forward"

// MaxTokenLength matches the OpenAPI PageInfo.next_cursor maxLength and
// accommodates schema-valid admin tag continuation tuples at their maximum
// identifier/tag lengths. Decode rejects larger tokens before splitting,
// decoding, or computing a MAC so attacker-controlled input cannot amplify
// memory or CPU use.
const MaxTokenLength = 1536

const maxKeyIDLength = 64

// AdminRole is the only authenticated system-administrator role the
// cursor layer binds into admin cursors. Per the OpenSpec contract
// the scope carries the role, NOT the subject.
const AdminRole = "admin"

// Orderings known to the cursor layer. Each endpoint declares exactly ONE
// binding during encoder construction; the decoder rejects any cursor
// whose declared ordering differs from the binding.
const (
	OrderingAdminTags         = "team_id_asc__execution_tag_asc__task_type_id_asc"
	OrderingTasksDesc         = "ingested_at_desc__task_id_desc"
	OrderingAuditDesc         = "occurred_at_desc__audit_id_desc"
	OrderingEnvironmentsAsc   = "created_at_asc__environment_id_asc"
	OrderingSecretsAsc        = "created_at_asc__secret_id_asc"
	OrderingSecretVersionsAsc = "version_asc__secret_id_asc"
)

// Endpoints known to the cursor layer. The decoder rejects any cursor
// whose endpoint label does not match the endpoint that issued the
// request.
const (
	EndpointAdminTags             = "GET:/admin/tags"
	EndpointAdminTasks            = "GET:/admin/tasks"
	EndpointGatewayTasks          = "GET:/v1/tasks"
	EndpointGatewayAudit          = "GET:/v1/audit"
	EndpointGatewayEnvironments   = "GET:/v1/environments"
	EndpointGatewaySecrets        = "GET:/v1/environments/{environment_id}/secrets"
	EndpointGatewaySecretVersions = "GET:/v1/environments/{environment_id}/secrets/{secret_id}/versions"
)

// KeyID is the opaque identifier of the server-controlled HMAC key used
// to sign a cursor.
type KeyID string

// Identity is the opaque cursor scope value bound into every cursor.
//
// For Gateway cursors Kind="gateway" and the verified immutable team_id
// plus operator_id are bound into the envelope. For admin cursors
// Kind="admin" and the role is bound; the spec deliberately does NOT
// bind the admin subject because the contract anchors authorization
// on the role, not on the subject identity.
type Identity struct {
	Kind       string
	TeamID     string
	OperatorID string
}

// Scope renders the Identity as a single opaque string suitable for the
// cursor envelope.
func (i Identity) Scope() string {
	switch i.Kind {
	case "gateway":
		return "gateway|" + i.TeamID + "|" + i.OperatorID
	case "admin":
		return "admin|" + AdminRole
	}
	return "unknown|" + i.Kind
}

// Equal reports whether two identities are exactly the same in scope.
func (i Identity) Equal(o Identity) bool {
	if i.Kind != o.Kind {
		return false
	}
	switch i.Kind {
	case "gateway":
		return i.TeamID == o.TeamID && i.OperatorID == o.OperatorID
	case "admin":
		return true // role-only; admin subjects are not part of scope
	}
	return false
}

// envelope is the JSON payload that gets MAC-protected. The wire
// representation is deliberately compact and free of any caller-supplied
// or caller-tunable fields (no algorithm, no key_id, no MAC bytes inside
// the envelope). The key_id is a separate component on the wire.
type envelope struct {
	Endpoint              string                      `json:"ep"`
	Scope                 string                      `json:"sc"`
	Ordering              string                      `json:"or"`
	Direction             string                      `json:"dr"`
	Filters               map[string]string           `json:"ft"`
	TagPosition           *tagPositionTuple           `json:"tp,omitempty"`
	TaskPosition          *taskPositionTuple          `json:"kp,omitempty"`
	AuditPosition         *auditPositionTuple         `json:"ap,omitempty"`
	EnvironmentPosition   *environmentPositionTuple   `json:"enp,omitempty"`
	SecretPosition        *secretPositionTuple        `json:"sep,omitempty"`
	SecretVersionPosition *secretVersionPositionTuple `json:"svp,omitempty"`
}

// tagPositionTuple is the after-cursor continuation for the documented
// (team_id ASC, execution_tag ASC, task_type_id ASC) ordering.
type tagPositionTuple struct {
	TeamID       string `json:"t"`
	ExecutionTag string `json:"e"`
	TaskTypeID   string `json:"y"`
}

// taskPositionTuple is the after-cursor continuation for the documented
// (ingested_at DESC, task_id DESC) ordering. Equal `ingested_at` values
// are tie-broken by `task_id`.
type taskPositionTuple struct {
	IngestedAtNano int64  `json:"i"`
	TaskID         string `json:"t"`
}

type auditPositionTuple struct {
	OccurredAtNano int64  `json:"o"`
	AuditID        string `json:"a"`
}

// environmentPositionTuple is the after-cursor continuation for the
// documented (created_at ASC, environment_id ASC) ordering.
type environmentPositionTuple struct {
	CreatedAtNano int64  `json:"c"`
	EnvironmentID string `json:"e"`
}

// secretPositionTuple is the after-cursor continuation for the
// documented (created_at ASC, secret_id ASC) ordering of logical
// secrets under one environment.
type secretPositionTuple struct {
	CreatedAtNano int64  `json:"c"`
	SecretID      string `json:"s"`
}

// secretVersionPositionTuple is the after-cursor continuation for the
// documented (version ASC, secret_id ASC) ordering of secret_versions
// under one logical secret.
type secretVersionPositionTuple struct {
	Version  int    `json:"v"`
	SecretID string `json:"s"`
}

// Keyring is the server-controlled rotating key set consulted by the
// encoder and decoder.
type Keyring interface {
	ActiveKeyID() KeyID
	Keys() map[KeyID][]byte
}

// ErrInvalid is the sentinel returned for every malformed/tampered
// input. The HTTP boundary maps this sentinel to the documented
// non-revealing `400 invalid_pagination` response shape.
var ErrInvalid = errors.New("cursor: invalid")

type invalidError struct{ reason string }

func (e *invalidError) Error() string { return "cursor: " + e.reason }
func (e *invalidError) Unwrap() error { return ErrInvalid }

func invalid(reason string) error { return &invalidError{reason: reason} }

// EncodeIssue is the structured input to Encode.
type EncodeIssue struct {
	Endpoint              string
	Identity              Identity
	Ordering              string
	Filters               map[string]string
	TagPosition           *tagPositionTuple
	TaskPosition          *taskPositionTuple
	AuditPosition         *auditPositionTuple
	EnvironmentPosition   *environmentPositionTuple
	SecretPosition        *secretPositionTuple
	SecretVersionPosition *secretVersionPositionTuple
}

// Encode returns the opaque base64url cursor string for the supplied
// issue. The wire format is:
//
//	<key_id> "." <base64url(no-padding) of envelope> "." <base64url(no-padding) of HMAC>
func Encode(keyring Keyring, issue EncodeIssue) (string, error) {
	if keyring == nil {
		return "", errors.New("cursor: nil keyring")
	}
	if issue.Endpoint == "" || issue.Ordering == "" {
		return "", errors.New("cursor: missing endpoint or ordering")
	}
	if issue.Identity.Kind != "gateway" && issue.Identity.Kind != "admin" {
		return "", errors.New("cursor: identity kind must be gateway or admin")
	}
	keyID := keyring.ActiveKeyID()
	key, ok := keyring.Keys()[keyID]
	if !ok || len(key) == 0 {
		return "", fmt.Errorf("cursor: keyring missing key %q", keyID)
	}
	env := envelope{
		Endpoint:              issue.Endpoint,
		Scope:                 issue.Identity.Scope(),
		Ordering:              issue.Ordering,
		Direction:             Direction,
		Filters:               canonicalFilters(issue.Filters),
		TagPosition:           issue.TagPosition,
		TaskPosition:          issue.TaskPosition,
		AuditPosition:         issue.AuditPosition,
		EnvironmentPosition:   issue.EnvironmentPosition,
		SecretPosition:        issue.SecretPosition,
		SecretVersionPosition: issue.SecretVersionPosition,
	}
	envBytes, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("cursor: marshal envelope: %w", err)
	}
	mac := computeMAC(key, string(keyID), envBytes)
	encoded := base64.RawURLEncoding.EncodeToString(envBytes)
	macEncoded := base64.RawURLEncoding.EncodeToString(mac)
	token := string(keyID) + "." + encoded + "." + macEncoded
	if len(token) > MaxTokenLength {
		return "", errors.New("cursor: encoded token exceeds maximum length")
	}
	return token, nil
}

// DecodeRequest is the structured input to Decode.
type DecodeRequest struct {
	Token    string
	Endpoint string
	Identity Identity
	Ordering string
	Filters  map[string]string
}

// Decode verifies the supplied cursor and returns the parsed envelope.
func Decode(keyring Keyring, req DecodeRequest) (*envelope, error) {
	if keyring == nil {
		return nil, errors.New("cursor: nil keyring")
	}
	if req.Token == "" {
		return nil, invalid("empty token")
	}
	if len(req.Token) > MaxTokenLength {
		return nil, invalid("token too long")
	}
	parts := strings.Split(req.Token, ".")
	if len(parts) != 3 {
		return nil, invalid("malformed token")
	}
	keyID := KeyID(parts[0])
	if keyID == "" || len(keyID) > maxKeyIDLength {
		return nil, invalid("missing key_id")
	}
	key, ok := keyring.Keys()[keyID]
	if !ok || len(key) == 0 {
		return nil, invalid("unknown key_id")
	}
	envBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, invalid("envelope decode")
	}
	macBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, invalid("mac decode")
	}
	expected := computeMAC(key, string(keyID), envBytes)
	if !hmac.Equal(expected, macBytes) {
		return nil, invalid("mac mismatch")
	}
	var env envelope
	if err := json.Unmarshal(envBytes, &env); err != nil {
		return nil, invalid("envelope parse")
	}
	if env.Endpoint != req.Endpoint {
		return nil, invalid("endpoint mismatch")
	}
	if env.Scope != req.Identity.Scope() {
		return nil, invalid("scope mismatch")
	}
	if env.Ordering != req.Ordering {
		return nil, invalid("ordering mismatch")
	}
	if env.Direction != Direction {
		return nil, invalid("direction mismatch")
	}
	if !filtersEqual(env.Filters, req.Filters) {
		return nil, invalid("filter mismatch")
	}
	return &env, nil
}

// TagPosition returns the tag continuation tuple from the decoded
// envelope. Returns nil when the envelope carries no tag position.
func TagPosition(env *envelope) *tagPositionTuple { return env.TagPosition }

// TaskPosition returns the task continuation tuple from the decoded
// envelope. Returns nil when the envelope carries no task position.
func TaskPosition(env *envelope) *taskPositionTuple { return env.TaskPosition }

func AuditPosition(env *envelope) *auditPositionTuple { return env.AuditPosition }

// EnvironmentPosition returns the environment continuation tuple from
// the decoded envelope. Returns nil when the envelope carries no
// environment position.
func EnvironmentPosition(env *envelope) *environmentPositionTuple { return env.EnvironmentPosition }

// SecretPosition returns the secret continuation tuple from the
// decoded envelope. Returns nil when the envelope carries no secret
// position.
func SecretPosition(env *envelope) *secretPositionTuple { return env.SecretPosition }

// SecretVersionPosition returns the secret-version continuation tuple
// from the decoded envelope. Returns nil when the envelope carries no
// secret-version position.
func SecretVersionPosition(env *envelope) *secretVersionPositionTuple {
	return env.SecretVersionPosition
}

// TagPositionTuple constructs a tag continuation tuple from the
// caller-supplied (team_id, execution_tag, task_type_id).
func TagPositionTuple(teamID, executionTag, taskTypeID string) *tagPositionTuple {
	return &tagPositionTuple{TeamID: teamID, ExecutionTag: executionTag, TaskTypeID: taskTypeID}
}

// TaskPositionTuple constructs a task continuation tuple from the
// caller-supplied (ingested_at, task_id).
func TaskPositionTuple(ingestedAtNano int64, taskID string) *taskPositionTuple {
	return &taskPositionTuple{IngestedAtNano: ingestedAtNano, TaskID: taskID}
}

func AuditPositionTuple(occurredAtNano int64, auditID string) *auditPositionTuple {
	return &auditPositionTuple{OccurredAtNano: occurredAtNano, AuditID: auditID}
}

// EnvironmentPositionTuple constructs an environment continuation
// tuple from the caller-supplied (created_at, environment_id).
func EnvironmentPositionTuple(createdAtNano int64, environmentID string) *environmentPositionTuple {
	return &environmentPositionTuple{CreatedAtNano: createdAtNano, EnvironmentID: environmentID}
}

// SecretPositionTuple constructs a logical-secret continuation tuple
// from the caller-supplied (created_at, secret_id).
func SecretPositionTuple(createdAtNano int64, secretID string) *secretPositionTuple {
	return &secretPositionTuple{CreatedAtNano: createdAtNano, SecretID: secretID}
}

// SecretVersionPositionTuple constructs a secret-version continuation
// tuple from the caller-supplied (version, secret_id).
func SecretVersionPositionTuple(version int, secretID string) *secretVersionPositionTuple {
	return &secretVersionPositionTuple{Version: version, SecretID: secretID}
}

// ----------------------------------------------------------------------
// Limit parsing
// ----------------------------------------------------------------------

// DefaultLimit is the documented default page size.
const DefaultLimit = 50

// MaxLimit is the documented maximum page size.
const MaxLimit = 200

// LimitValue is the validated limit paired with the raw input.
type LimitValue struct {
	Raw   string
	Value int
}

// ParseLimit validates a single query-string limit value.
//
// Rules (matched to OpenAPI Limit component):
//   - absent: defaults to 50
//   - empty string: rejected
//   - non-integer (fractions, signs, hex, octal): rejected
//   - leading/trailing whitespace: rejected
//   - leading zeros (e.g. "01"): rejected
//   - < 1 or > 200: rejected
//   - multiple values for the same key: rejected
func ParseLimit(values url.Values) (LimitValue, error) {
	v, ok := values["limit"]
	if !ok || len(v) == 0 {
		return LimitValue{Raw: "", Value: DefaultLimit}, nil
	}
	if len(v) > 1 {
		return LimitValue{}, invalid("limit multiple values")
	}
	raw := v[0]
	if raw == "" {
		return LimitValue{}, invalid("limit empty")
	}
	if strings.TrimSpace(raw) != raw {
		return LimitValue{}, invalid("limit whitespace")
	}
	if !isCanonicalDecimal(raw) {
		return LimitValue{}, invalid("limit not canonical decimal")
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return LimitValue{}, invalid("limit not integer")
	}
	if n < 1 || n > MaxLimit {
		return LimitValue{}, invalid("limit out of range")
	}
	return LimitValue{Raw: raw, Value: n}, nil
}

// isCanonicalDecimal returns true when the string is a positive
// decimal integer with no leading zeros, no signs, and no fractions.
// The contract treats "+1", "01", "0", and "1.5" as malformed.
func isCanonicalDecimal(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	if s == "0" {
		return false
	}
	if s[0] == '0' {
		return false
	}
	return true
}

// filtersEqual returns whether two filter maps carry exactly the same
// keys with exactly the same values.
func filtersEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		other, ok := b[k]
		if !ok || v != other {
			return false
		}
	}
	return true
}

// canonicalFilters returns a fresh map with the same entries as the
// input.
func canonicalFilters(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// computeMAC returns HMAC-SHA-256 over the documented header
// concatenated with the key_id and the envelope.
func computeMAC(key []byte, keyID string, env []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(protocolHeader))
	h.Write([]byte{0})
	h.Write([]byte(keyID))
	h.Write([]byte{0})
	h.Write(env)
	return h.Sum(nil)
}

// protocolHeader is the constant header concatenated into every HMAC
// computation.
const protocolHeader = "v0002-cursor/v1"
