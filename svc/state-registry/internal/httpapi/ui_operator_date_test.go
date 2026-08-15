package httpapi

import "testing"

func TestValidUIDate(t *testing.T) {
	for _, test := range []struct {
		value string
		want  bool
	}{
		{value: "", want: true},
		{value: "2026-08-15", want: true},
		{value: "2026-02-29", want: false},
		{value: "2024-02-29", want: true},
		{value: "2026-8-15", want: false},
		{value: "2026-08-15T00:00:00Z", want: false},
	} {
		if got := validUIDate(test.value); got != test.want {
			t.Errorf("validUIDate(%q) = %v, want %v", test.value, got, test.want)
		}
	}
}
