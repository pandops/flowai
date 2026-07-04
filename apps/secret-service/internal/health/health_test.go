package health

import (
	"encoding/json"
	"testing"
)

func TestOKReturnsExpectedService(t *testing.T) {
	s := OK("secret-service")
	if s.Service != "secret-service" {
		t.Fatalf("unexpected service: %s", s.Service)
	}
	if s.Status != "ok" {
		t.Fatalf("unexpected status: %s", s.Status)
	}
}

func TestMarshalProducesValidJSON(t *testing.T) {
	raw := OK("secret-service").Marshal()
	var decoded map[string]string
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if decoded["service"] != "secret-service" {
		t.Fatalf("unexpected json: %v", decoded)
	}
}
