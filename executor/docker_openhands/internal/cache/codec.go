// Package cache codec helpers. Kept separate so the cache surface
// stays a small, well-documented API. The encoders are stable for
// one schema version; version bumps are detected on open and the
// cache fails closed.
package cache

import (
	"encoding/json"
	"errors"
)

func encodeAssignment(rec AssignmentRecord) ([]byte, error) {
	return json.Marshal(rec)
}

func decodeAssignment(raw []byte, out *AssignmentRecord) error {
	if len(raw) == 0 {
		return errors.New("cache: empty assignment record")
	}
	return json.Unmarshal(raw, out)
}

func encodeOutbox(rec EventOutboxRecord) ([]byte, error) {
	return json.Marshal(rec)
}

func decodeOutbox(raw []byte, out *EventOutboxRecord) error {
	if len(raw) == 0 {
		return errors.New("cache: empty outbox record")
	}
	return json.Unmarshal(raw, out)
}
