package executor

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func readWholeFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func unmarshalYAMLData(data []byte, v interface{}) error {
	return yaml.Unmarshal(data, v)
}

func lookupEnv(k string) (string, bool) {
	return os.LookupEnv(k)
}

// readFile is split out for tests.
func readFile(path string) ([]byte, error) { return readWholeFile(path) }

// unmarshalYAML is split out for tests.
func unmarshalYAML(data []byte, v interface{}) error { return unmarshalYAMLData(data, v) }

// envOr returns env value or fallback.
func envOr(k, fb string) string {
	if v, ok := lookupEnv(k); ok && v != "" {
		return v
	}
	return fb
}

// envInt returns env value as int or fallback.
func envInt(k string, fb int) int {
	if v, ok := lookupEnv(k); ok && v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return fb
}

// envDur returns env value as duration (interpreted as seconds if no unit) or fallback.
func envDur(k string, fb time.Duration) time.Duration {
	if v, ok := lookupEnv(k); ok && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		var s int
		if _, err := fmt.Sscanf(v, "%d", &s); err == nil {
			return time.Duration(s) * time.Second
		}
	}
	return fb
}

// envBool returns env value parsed as bool or fallback.
func envBool(k string, fb bool) bool {
	if v, ok := lookupEnv(k); ok && v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return fb
}
