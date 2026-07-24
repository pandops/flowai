package config

import (
	"encoding/hex"
	"strings"
	"testing"
)

func cursorHex(fill byte) string {
	return hex.EncodeToString(bytesOf(fill, 32))
}

func bytesOf(fill byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = fill
	}
	return out
}

func TestLoadRotatingCursorKeyring(t *testing.T) {
	keyring, err := LoadRotatingCursorKeyring(
		"cursor-v2",
		cursorHex(2),
		`{"cursor-v1":"`+cursorHex(1)+`"}`,
	)
	if err != nil {
		t.Fatalf("LoadRotatingCursorKeyring: %v", err)
	}
	if got := keyring.ActiveKeyID(); got != "cursor-v2" {
		t.Fatalf("active key id=%q want cursor-v2", got)
	}
	keys := keyring.Keys()
	if len(keys) != 2 || len(keys["cursor-v1"]) != 32 || len(keys["cursor-v2"]) != 32 {
		t.Fatalf("keys=%v, want active and previous 32-byte keys", keys)
	}
	keys["cursor-v1"][0] = 99
	if keyring.Keys()["cursor-v1"][0] == 99 {
		t.Fatal("Keys exposed mutable key bytes")
	}
}

func TestLoadRotatingCursorKeyringRejectsInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name       string
		activeID   string
		activeHex  string
		previous   string
		wantErrSub string
	}{
		{name: "missing active key", activeID: "cursor-v2", wantErrSub: "required"},
		{name: "invalid active id", activeID: "cursor key", activeHex: cursorHex(2), wantErrSub: "KEY_ID"},
		{name: "bad active hex", activeID: "cursor-v2", activeHex: "zz", wantErrSub: "decode"},
		{name: "short active key", activeID: "cursor-v2", activeHex: "00", wantErrSub: "32 bytes"},
		{name: "bad previous json", activeID: "cursor-v2", activeHex: cursorHex(2), previous: "{", wantErrSub: "PREVIOUS_KEYS_JSON"},
		{name: "duplicate active id", activeID: "cursor-v2", activeHex: cursorHex(2), previous: `{"cursor-v2":"` + cursorHex(1) + `"}`, wantErrSub: "must not repeat"},
		{name: "invalid previous id", activeID: "cursor-v2", activeHex: cursorHex(2), previous: `{"bad key":"` + cursorHex(1) + `"}`, wantErrSub: "previous cursor key id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadRotatingCursorKeyring(tc.activeID, tc.activeHex, tc.previous)
			if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("error=%v want substring %q", err, tc.wantErrSub)
			}
		})
	}
}
