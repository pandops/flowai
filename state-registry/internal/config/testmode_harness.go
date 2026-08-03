// Test-harness-only test-mode resolver. The Playwright worker builds
// the State Registry with -tags state_registry_test_harness so the
// narrow tag gates every header-trusting test surface. This file
// is the ONLY file in the package that honours
// STATE_REGISTRY_TEST_MODE=true; the production
// testmode_normal.go rejects every non-empty value closed.
//
//go:build state_registry_test_harness

package config

import (
	"fmt"
	"strconv"
	"strings"
)

// readTestModeFromEnv in the test-harness build accepts the
// STATE_REGISTRY_TEST_MODE boolean env var exactly like
// strconv.ParseBool would. The un-tagged counterpart (testmode_normal.go)
// returns an error for any non-empty value so a production binary
// never honours this surface.
func readTestModeFromEnv(raw string) (bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false, nil
	}
	v, err := strconv.ParseBool(trimmed)
	if err != nil {
		return false, fmt.Errorf("parse STATE_REGISTRY_TEST_MODE=%q: %w", raw, err)
	}
	return v, nil
}
