package executor

import (
	"os"
	"strings"

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

// replaceAll returns s with all non-overlapping instances of old replaced by new.
func replaceAll(s, old, new string) string {
	return strings.ReplaceAll(s, old, new)
}