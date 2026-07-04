// Package health is a placeholder for the secret-service. Real implementation lands later.
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
