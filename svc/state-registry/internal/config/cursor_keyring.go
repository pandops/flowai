package config

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// CursorKeyring is the server-controlled rotating HMAC key set used
// by the state-registry cursor layer. The keyring is sourced from the
// STATE_REGISTRY_CURSOR_KEY_HEX env var and is intentionally narrow: it
// holds only the bytes needed to sign and verify opaque cursors.
//
// The keyring is safe for concurrent reads; production code only reads
// the keying material during decode/encode calls.
type CursorKeyring struct {
	mu      sync.RWMutex
	keys    map[string][]byte
	primary string
}

// ActiveKeyID returns the key id that the encoder should use when
// issuing a new cursor.
func (k *CursorKeyring) ActiveKeyID() string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.primary
}

// Keys returns the active keyring indexed by key id. The returned map
// is a copy so callers cannot mutate the keyring by accident.
func (k *CursorKeyring) Keys() map[string][]byte {
	k.mu.RLock()
	defer k.mu.RUnlock()
	out := make(map[string][]byte, len(k.keys))
	for id, key := range k.keys {
		out[id] = append([]byte(nil), key...)
	}
	return out
}

// CursorKeySingle is the deterministic env-var key id used when the
// operator supplies a single HMAC key via STATE_REGISTRY_CURSOR_KEY_HEX.
// The value is a stable string and is part of the public cursor wire
// format.
const CursorKeySingle = "state-registry-cursor-v1"

const (
	maxCursorKeyIDLength = 64
	maxCursorKeyCount    = 8
)

// LoadCursorKeyringFromEnv constructs a keyring from the supplied
// hex-encoded key and the documented default key id. Production code
// MUST call this exactly once at startup. When the key bytes are
// invalid the returned error is non-nil and the deployment MUST
// fail-closed.
func LoadCursorKeyringFromEnv(keyHex string) (*CursorKeyring, error) {
	return LoadRotatingCursorKeyring(CursorKeySingle, keyHex, "")
}

// LoadRotatingCursorKeyring constructs a keyring with one active signing
// key and an optional JSON object of previous verification keys. New cursors
// are always issued with activeKeyID; previous keys remain decode-only until
// operators remove them after the maximum cursor lifetime has elapsed.
func LoadRotatingCursorKeyring(activeKeyID, activeKeyHex, previousKeysJSON string) (*CursorKeyring, error) {
	if activeKeyID == "" {
		activeKeyID = CursorKeySingle
	}
	if !validCursorKeyID(activeKeyID) {
		return nil, fmt.Errorf("STATE_REGISTRY_CURSOR_KEY_ID must be 1-%d characters from [A-Za-z0-9._:-]", maxCursorKeyIDLength)
	}
	activeKey, err := decodeCursorKey("STATE_REGISTRY_CURSOR_KEY_HEX", activeKeyHex)
	if err != nil {
		return nil, err
	}

	keys := map[string][]byte{activeKeyID: activeKey}
	if previousKeysJSON != "" {
		var previous map[string]string
		if err := json.Unmarshal([]byte(previousKeysJSON), &previous); err != nil {
			return nil, fmt.Errorf("decode STATE_REGISTRY_CURSOR_PREVIOUS_KEYS_JSON: %w", err)
		}
		if len(previous)+1 > maxCursorKeyCount {
			return nil, fmt.Errorf("cursor keyring may contain at most %d keys", maxCursorKeyCount)
		}
		for keyID, keyHex := range previous {
			if !validCursorKeyID(keyID) {
				return nil, fmt.Errorf("previous cursor key id must be 1-%d characters from [A-Za-z0-9._:-]", maxCursorKeyIDLength)
			}
			if keyID == activeKeyID {
				return nil, fmt.Errorf("previous cursor keys must not repeat active key id %q", activeKeyID)
			}
			key, err := decodeCursorKey("previous cursor key", keyHex)
			if err != nil {
				return nil, err
			}
			keys[keyID] = key
		}
	}

	return &CursorKeyring{keys: keys, primary: activeKeyID}, nil
}

func decodeCursorKey(name, keyHex string) ([]byte, error) {
	if keyHex == "" {
		return nil, missingCursorKeyError{}
	}
	decoded, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", name, err)
	}
	if len(decoded) != 32 {
		return nil, fmt.Errorf("%s must decode to exactly 32 bytes, got %d", name, len(decoded))
	}
	return decoded, nil
}

func validCursorKeyID(keyID string) bool {
	if keyID == "" || len(keyID) > maxCursorKeyIDLength || strings.TrimSpace(keyID) != keyID {
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

// missingCursorKeyError is the structured failure surfaced when the
// cursor keyring is not configured. The HTTP boundary maps the
// sentinel to startup failure so business routes are not mounted.
type missingCursorKeyError struct{}

func (missingCursorKeyError) Error() string {
	return "STATE_REGISTRY_CURSOR_KEY_HEX is required (32-byte hex-encoded cursor HMAC key)"
}
