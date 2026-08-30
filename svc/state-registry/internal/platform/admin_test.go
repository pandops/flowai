package platform

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAdminModelsUseOpenAPIFieldNames(t *testing.T) {
	image := ImageReference{
		Repository: "registry.example/agent:stable",
		Digest:     "sha256:" + strings.Repeat("a", 64),
	}

	tests := []struct {
		name string
		in   any
		keys []string
	}{
		{name: "team request", in: CreateTeamRequest{TeamName: "alpha", DefaultImage: image.Repository + "@" + image.Digest}, keys: []string{"team_name", "default_image"}},
		{name: "source request", in: CreateSourceSystemRequest{TeamID: "team-a", ListenerIdentity: "listener-a"}, keys: []string{"team_id", "listener_identity"}},
		{name: "task type request", in: CreateTaskTypeRequest{TeamID: "team-a", ExecutionTag: "openhands"}, keys: []string{"team_id", "execution_tag"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := json.Marshal(tt.in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			for _, key := range tt.keys {
				if _, ok := body[key]; !ok {
					t.Errorf("JSON field %q missing from %s", key, encoded)
				}
			}
		})
	}
}
