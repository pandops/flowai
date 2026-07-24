package main

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestCursorKeyringForBootFailsClosed(t *testing.T) {
	t.Setenv("STATE_REGISTRY_CURSOR_KEY_ID", "")
	t.Setenv("STATE_REGISTRY_CURSOR_KEY_HEX", "")
	t.Setenv("STATE_REGISTRY_CURSOR_PREVIOUS_KEYS_JSON", "")
	if _, err := cursorKeyringForBoot(); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("missing key error=%v", err)
	}
}

func TestCursorKeyringForBootLoadsRotationSet(t *testing.T) {
	active := make([]byte, 32)
	previous := make([]byte, 32)
	for i := range active {
		active[i] = 2
		previous[i] = 1
	}
	t.Setenv("STATE_REGISTRY_CURSOR_KEY_ID", "cursor-v2")
	t.Setenv("STATE_REGISTRY_CURSOR_KEY_HEX", hex.EncodeToString(active))
	t.Setenv("STATE_REGISTRY_CURSOR_PREVIOUS_KEYS_JSON", `{"cursor-v1":"`+hex.EncodeToString(previous)+`"}`)

	keyring, err := cursorKeyringForBoot()
	if err != nil {
		t.Fatalf("cursorKeyringForBoot: %v", err)
	}
	if keyring.ActiveKeyID() != "cursor-v2" || len(keyring.Keys()) != 2 {
		t.Fatalf("keyring active=%q keys=%d", keyring.ActiveKeyID(), len(keyring.Keys()))
	}
}
