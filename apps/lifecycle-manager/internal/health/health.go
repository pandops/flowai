// Package health implements a tiny placeholder health probe used by the
// lifecycle-manager entrypoint and exposed as /healthz. Real implementation
// lives in a later wave.
package health

import "encoding/json"

type Status struct {
	Status  string `json:"status"`
	Service string `json:"service"`
}

func OK(service string) Status {
	return Status{Status: "ok", Service: service}
}

func (s Status) Marshal() []byte {
	out, _ := json.Marshal(s)
	return out
}
