package health

import (
	"encoding/json"
	"testing"
)

func TestOKReturnsExpectedService(t *testing.T) {
	s := OK("lifecycle-manager")
	if s.Service != "lifecycle-manager" {
		t.Fatalf("unexpected service: %s", s.Service)
	}
	if s.Status != "ok" {
		t.Fatalf("unexpected status: %s", s.Status)
	}
}

func TestMarshalProducesValidJSON(t *testing.T) {
	raw := OK("lifecycle-manager").Marshal()
	var decoded map[string]string
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if decoded["service"] != "lifecycle-manager" {
		t.Fatalf("unexpected json: %v", decoded)
	}
}
