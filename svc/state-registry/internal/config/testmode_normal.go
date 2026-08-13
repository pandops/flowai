// Production-mode test-mode resolver. The normal (un-tagged) build
// of the State Registry MUST reject any non-empty
// STATE_REGISTRY_TEST_MODE environment variable: the test harness is
// gated by the state_registry_test_harness build tag and a normal
// binary cannot opt into header-trusting test mode via environment
// alone.
//
//go:build !state_registry_test_harness

package config

import "fmt"

// readTestModeFromEnv resolves the STATE_REGISTRY_TEST_MODE
// environment value into a (bool, error) pair. In the un-tagged
// build the only legal value is "" (unset), which corresponds to
// production mode. Any non-empty value is rejected with a clear
// message so a misconfigured deployment fails closed instead of
// silently enabling header-trusting test mode.
func readTestModeFromEnv(raw string) (bool, error) {
	if raw == "" {
		return false, nil
	}
	return false, fmt.Errorf(
		"STATE_REGISTRY_TEST_MODE=%q is rejected by this build; "+
			"the state_registry_test_harness build tag is required to enable test mode "+
			"(a normal production binary cannot enable header-trusting test mode via environment alone)",
		raw,
	)
}
