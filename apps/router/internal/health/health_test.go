package health

import (
	"encoding/json"
	"testing"
)

func TestOKReturnsExpectedService(t *testing.T) {
	s := OK("router")
	if s.Service != "router" {
		t.Fatalf("unexpected service: %s", s.Service)
	}
	if s.Status != "ok" {
		t.Fatalf("unexpected status: %s", s.Status)
	}
}

func TestMarshalProducesValidJSON(t *testing.T) {
	raw := OK("router").Marshal()
	var decoded map[string]string
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if decoded["service"] != "router" {
		t.Fatalf("unexpected json: %v", decoded)
	}
}
