package store

import (
	"strings"
	"testing"
)

func TestParseScopeTokenPreviousKeysLegacyKeyIDNamedKey(t *testing.T) {
	keyHex := strings.Repeat("01", 32)
	entries, err := parseScopeTokenPreviousKeys(`{"key":"` + keyHex + `"}`)
	if err != nil {
		t.Fatalf("parseScopeTokenPreviousKeys: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entry count=%d want 1", len(entries))
	}
	if entries[0].keyID != "key" || entries[0].keyHex != keyHex || entries[0].alg != "" {
		t.Fatalf("entry=%+v want legacy key entry", entries[0])
	}
}

func TestParseScopeTokenPreviousKeysRejectsMixedShapes(t *testing.T) {
	keyHex := strings.Repeat("02", 32)
	_, err := parseScopeTokenPreviousKeys(`{"legacy":"` + keyHex + `","rich":{"key":"` + keyHex + `","alg":"HS256"}}`)
	if err == nil {
		t.Fatal("parseScopeTokenPreviousKeys accepted mixed legacy and structured entries")
	}
}
