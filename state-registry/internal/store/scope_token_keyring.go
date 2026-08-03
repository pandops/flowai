package store

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"strings"
	"sync"
)

// ScopeTokenAlgorithm identifies one allow-listed HMAC algorithm from
// the documented HS256/HS384/HS512 family.
type ScopeTokenAlgorithm string

const (
	ScopeTokenAlgHS256 ScopeTokenAlgorithm = "HS256"
	ScopeTokenAlgHS384 ScopeTokenAlgorithm = "HS384"
	ScopeTokenAlgHS512 ScopeTokenAlgorithm = "HS512"
)

// allowedScopeTokenAlgs is the canonical allow-list enforced by both
// the issuer and the verifier. Any other algorithm value is rejected
// with a non-revealing 404 before any MAC computation.
var allowedScopeTokenAlgs = map[ScopeTokenAlgorithm]bool{
	ScopeTokenAlgHS256: true,
	ScopeTokenAlgHS384: true,
	ScopeTokenAlgHS512: true,
}

// DefaultScopeTokenKeyID is the deterministic key id used when the
// operator supplies exactly one HMAC key. The value is part of the
// public scope-token wire format and is matched in tests.
const DefaultScopeTokenKeyID = "state-registry-scope-v1"

// scopeTokenKey bundles the server-side key bytes with the
// algorithm the keyring is configured to use for the key. The
// algorithm is authoritative for MAC verification: a token whose
// protected-header `alg` differs from the stored algorithm is
// rejected without any MAC computation. The previous-keys JSON map
// may either inline an algorithm per key (the secure default) or
// fall back to the keyring-wide algorithm when the operator
// supplies the legacy "hex-only" shape.
type scopeTokenKey struct {
	bytes []byte
	alg   ScopeTokenAlgorithm
}

// scopeTokenKeyring is the server-controlled rotating key set used to
// sign and verify open-environment scope tokens. New tokens are always
// issued with the active key; previous keys remain verify-only until
// operators remove them after the maximum scope-token lifetime has
// elapsed (currently 5 minutes plus 30 seconds of clock skew).
//
// Each key carries its own algorithm binding. The legacy single-alg
// keyring (one global algorithm) is preserved as a convenience for
// existing operators but every new key MUST carry an explicit
// algorithm so an attacker cannot trick the verifier by sending a
// token whose protected header `alg` is a stronger allow-listed
// variant than the key was generated for.
type scopeTokenKeyring struct {
	mu        sync.RWMutex
	keys      map[string]scopeTokenKey
	primary   string
	activeAlg ScopeTokenAlgorithm
}

// ScopeTokenKeyring is the public, read-only handle to the
// server-controlled rotating key set used to sign and verify
// open-environment scope tokens. It hides the raw key bytes and
// the mutation API; callers may only consult the active key id and
// perform constant-time lookups.
type ScopeTokenKeyring struct {
	inner *scopeTokenKeyring
}

// ActiveKeyID returns the key id the issuer uses to stamp new
// tokens. The empty string indicates the keyring is not loaded.
func (k *ScopeTokenKeyring) ActiveKeyID() string {
	if k == nil || k.inner == nil {
		return ""
	}
	return k.inner.activeKeyID()
}

// NewScopeTokenKeyring loads the rotating scope-token HMAC key set
// from the supplied hex-encoded primary key and JSON map of
// previous keys. The returned handle is safe for concurrent reads.
//
// The previousKeysJSON argument accepts two equivalent shapes:
//
//  1. The simple hex-only map: `{"<keyID>": "<hex>"}`. Every key
//     inherits the supplied `alg`; this matches the legacy
//     single-algorithm keyring and is preserved for tests and
//     operational scripts that have not been updated yet.
//  2. The per-key algorithm map: `{"<keyID>": {"key": "<hex>",
//     "alg": "HS256"}}`. The per-key algorithm is the source of
//     truth for MAC verification.
func NewScopeTokenKeyring(primaryID, primaryKeyHex, previousKeysJSON string, alg ScopeTokenAlgorithm) (*ScopeTokenKeyring, error) {
	inner, err := buildScopeTokenKeyring(primaryID, primaryKeyHex, previousKeysJSON, alg)
	if err != nil {
		return nil, err
	}
	return &ScopeTokenKeyring{inner: inner}, nil
}

func buildScopeTokenKeyring(primaryID, primaryKeyHex, previousJSON string, alg ScopeTokenAlgorithm) (*scopeTokenKeyring, error) {
	if primaryID == "" {
		primaryID = DefaultScopeTokenKeyID
	}
	if !validScopeTokenKeyID(primaryID) {
		return nil, fmt.Errorf("scope token key id must be 1..%d characters from [A-Za-z0-9._:-]", maxScopeTokenKeyIDLength)
	}
	if alg == "" {
		alg = ScopeTokenAlgHS256
	}
	if !allowedScopeTokenAlgs[alg] {
		return nil, fmt.Errorf("scope token signing algorithm %q is not in the HS256/HS384/HS512 allow-list", alg)
	}
	primary, err := decodeScopeTokenKey("scope token key", primaryKeyHex)
	if err != nil {
		return nil, err
	}
	keys := map[string]scopeTokenKey{
		primaryID: {bytes: primary, alg: alg},
	}
	if previousJSON != "" {
		entries, err := parseScopeTokenPreviousKeys(previousJSON)
		if err != nil {
			return nil, err
		}
		if len(entries)+1 > maxScopeTokenKeyCount {
			return nil, fmt.Errorf("scope token keyring may contain at most %d keys", maxScopeTokenKeyCount)
		}
		for _, entry := range entries {
			if !validScopeTokenKeyID(entry.keyID) {
				return nil, fmt.Errorf("previous scope token key id must be 1..%d characters from [A-Za-z0-9._:-]", maxScopeTokenKeyIDLength)
			}
			if entry.keyID == primaryID {
				return nil, fmt.Errorf("previous scope token keys must not repeat active key id %q", primaryID)
			}
			key, err := decodeScopeTokenKey("previous scope token key", entry.keyHex)
			if err != nil {
				return nil, err
			}
			entryAlg := entry.alg
			if entryAlg == "" {
				entryAlg = alg
			}
			if !allowedScopeTokenAlgs[entryAlg] {
				return nil, fmt.Errorf("previous scope token key %q algorithm %q is not in the HS256/HS384/HS512 allow-list", entry.keyID, entryAlg)
			}
			keys[entry.keyID] = scopeTokenKey{bytes: key, alg: entryAlg}
		}
	}
	return &scopeTokenKeyring{keys: keys, primary: primaryID, activeAlg: alg}, nil
}

// scopeTokenPreviousEntry is the parsed shape of a single entry in
// the previous-keys JSON map. KeyHex is always populated; Alg is
// populated when the operator supplied the per-key map shape and
// stays empty for the legacy hex-only shape.
type scopeTokenPreviousEntry struct {
	keyID  string
	keyHex string
	alg    ScopeTokenAlgorithm
}

// parseScopeTokenPreviousKeys accepts both the legacy
// `{"<keyID>": "<hex>"}` shape and the per-key
// `{"<keyID>": {"key": "<hex>", "alg": "HS256"}}` shape. The
// returned slice preserves the operator's ordering; the caller is
// responsible for any uniqueness / size checks.
func parseScopeTokenPreviousKeys(previousJSON string) ([]scopeTokenPreviousEntry, error) {
	var rawEntries map[string]json.RawMessage
	if err := json.Unmarshal([]byte(previousJSON), &rawEntries); err != nil {
		return nil, fmt.Errorf("decode previous scope token keys: %w", err)
	}
	out := make([]scopeTokenPreviousEntry, 0, len(rawEntries))
	var structured *bool
	for keyID, raw := range rawEntries {
		trimmed := strings.TrimSpace(string(raw))
		entryStructured := strings.HasPrefix(trimmed, "{")
		if !entryStructured && !strings.HasPrefix(trimmed, `"`) {
			return nil, errors.New("decode previous scope token keys: values must be hex strings or structured key objects")
		}
		if structured != nil && *structured != entryStructured {
			return nil, errors.New("decode previous scope token keys: legacy and structured key entries must not be mixed")
		}
		if structured == nil {
			structured = new(bool)
			*structured = entryStructured
		}
		if !entryStructured {
			var keyHex string
			if err := json.Unmarshal(raw, &keyHex); err != nil {
				return nil, fmt.Errorf("decode previous scope token keys: %w", err)
			}
			out = append(out, scopeTokenPreviousEntry{keyID: keyID, keyHex: keyHex})
			continue
		}

		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, fmt.Errorf("decode previous scope token keys: %w", err)
		}
		if len(fields) != 2 || fields["key"] == nil || fields["alg"] == nil {
			return nil, errors.New("decode previous scope token keys: structured entries require exactly key and alg")
		}
		var keyHex, alg string
		if err := json.Unmarshal(fields["key"], &keyHex); err != nil {
			return nil, fmt.Errorf("decode previous scope token keys: key must be a string: %w", err)
		}
		if err := json.Unmarshal(fields["alg"], &alg); err != nil {
			return nil, fmt.Errorf("decode previous scope token keys: alg must be a string: %w", err)
		}
		out = append(out, scopeTokenPreviousEntry{
			keyID:  keyID,
			keyHex: keyHex,
			alg:    ScopeTokenAlgorithm(strings.ToUpper(strings.TrimSpace(alg))),
		})
	}
	return out, nil
}

func (k *scopeTokenKeyring) activeKeyID() string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.primary
}

func (k *scopeTokenKeyring) activeAlgorithm() ScopeTokenAlgorithm {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.activeAlg
}

// activeKey returns a defensive copy of the active key and the
// algorithm the keyring is configured to use for it. The bool is
// false when the keyring is not loaded.
func (k *scopeTokenKeyring) activeKey() (scopeTokenKey, bool) {
	if k == nil {
		return scopeTokenKey{}, false
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.primary == "" {
		return scopeTokenKey{}, false
	}
	key, ok := k.keys[k.primary]
	if !ok {
		return scopeTokenKey{}, false
	}
	return scopeTokenKey{bytes: append([]byte(nil), key.bytes...), alg: key.alg}, true
}

// lookup returns a defensive copy of the server-side key for the
// given key id together with the algorithm the keyring is
// configured to use for that key. The second return is false when
// the key id is outside the active key window.
func (k *scopeTokenKeyring) lookup(keyID string) (scopeTokenKey, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	key, ok := k.keys[keyID]
	if !ok {
		return scopeTokenKey{}, false
	}
	return scopeTokenKey{bytes: append([]byte(nil), key.bytes...), alg: key.alg}, true
}

// lookupAll returns a defensive copy of every key in the keyring
// indexed by key id. Used by the public Keys accessor; the caller
// cannot mutate the underlying keyring via the returned map.
func (k *scopeTokenKeyring) lookupAll() map[string][]byte {
	k.mu.RLock()
	defer k.mu.RUnlock()
	out := make(map[string][]byte, len(k.keys))
	for id, key := range k.keys {
		out[id] = append([]byte(nil), key.bytes...)
	}
	return out
}

func decodeScopeTokenKey(name, keyHex string) ([]byte, error) {
	if keyHex == "" {
		return nil, errors.New("scope token key is required (32-byte hex-encoded HMAC key)")
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(keyHex))
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", name, err)
	}
	if len(decoded) < 32 {
		return nil, fmt.Errorf("%s must decode to at least 32 bytes for HS256/HS384/HS512, got %d", name, len(decoded))
	}
	return decoded, nil
}

func validScopeTokenKeyID(keyID string) bool {
	if keyID == "" || len(keyID) > maxScopeTokenKeyIDLength || strings.TrimSpace(keyID) != keyID {
		return false
	}
	for _, r := range keyID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || strings.ContainsRune("._:-", r) {
			continue
		}
		return false
	}
	return true
}

const (
	maxScopeTokenKeyIDLength = 64
	maxScopeTokenKeyCount    = 8
)

var (
	errScopeTokenAlgNotAllowed = errors.New("scope token algorithm is not in the HS256/HS384/HS512 allow-list")
)

// scopeTokenMAC computes the constant-time HMAC for the given
// algorithm and signing input. The implementation dispatches to the
// documented hash for each allow-listed algorithm:
//   - HS256 -> SHA-256
//   - HS384 -> SHA-384
//   - HS512 -> SHA-512
//
// The function is exported to the same package so both the issuer and
// the verifier use the exact same dispatcher (single source of truth).
func scopeTokenMAC(alg ScopeTokenAlgorithm, key []byte, signingInput string) ([]byte, error) {
	if !allowedScopeTokenAlgs[alg] {
		return nil, errScopeTokenAlgNotAllowed
	}
	var h func() hash.Hash
	switch alg {
	case ScopeTokenAlgHS256:
		h = sha256.New
	case ScopeTokenAlgHS384:
		h = sha512.New384
	case ScopeTokenAlgHS512:
		h = sha512.New
	default:
		return nil, errScopeTokenAlgNotAllowed
	}
	mac := hmac.New(h, key)
	_, _ = mac.Write([]byte(signingInput))
	return mac.Sum(nil), nil
}

// constantTimeEqualMAC compares two MAC byte slices under constant
// time. Both inputs MUST already be the correct length; the helper
// does not pad or truncate. The 1/0 return matches
// subtle.ConstantTimeCompare for caller convenience.
func constantTimeEqualMAC(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare(a, b) == 1
}
