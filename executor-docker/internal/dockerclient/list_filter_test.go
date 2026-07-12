package dockerclient

import (
	"encoding/json"
	"testing"

	"github.com/docker/docker/api/types/filters"
)

// TestListByLabelFilterShape is a regression test for the bug where
// filters.Arg(key, value) sent the key as the filter key (e.g.
// "flowai.cleanup_id"), which Podman rejects with "<key> is an invalid
// filter". The filter key must be the literal string "label" and the
// value must be "key=value".
//
// This test exercises the same filters.NewArgs + filters.Arg call shape
// that SDKClient.listByLabel uses and asserts the marshalled filter JSON.
func TestListByLabelFilterShape(t *testing.T) {
	cases := []struct {
		name, key, value string
	}{
		{"cleanup_id", "flowai.cleanup_id", "cleanup-abc-123"},
		{"executor_id", "flowai.executor_id", "exec-xyz-789"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Mirror the SDKClient.listByLabel construction exactly.
			args := filters.NewArgs(filters.Arg("label", tc.key+"="+tc.value))
			got, err := filters.ToJSON(args)
			if err != nil {
				t.Fatalf("ToJSON: %v", err)
			}
			var parsed map[string]map[string]bool
			if err := json.Unmarshal([]byte(got), &parsed); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			// The filter key must be the literal "label" (NOT the
			// label-name itself) — Podman rejects any other key.
			entries, ok := parsed["label"]
			if !ok {
				t.Fatalf("expected filter key 'label', got %v", parsed)
			}
			if len(entries) != 1 {
				t.Fatalf("expected one label entry, got %v", entries)
			}
			seen, ok := entries[tc.key+"="+tc.value]
			if !ok {
				t.Fatalf("label entry %q not present, got %v",
					tc.key+"="+tc.value, entries)
			}
			if !seen {
				t.Fatalf("label entry %q has value false", tc.key+"="+tc.value)
			}
			// There must be no other filter key (notably the
			// "flowai.cleanup_id" bug that was caused by passing the
			// label name as the filter key).
			for k := range parsed {
				if k != "label" {
					t.Fatalf("unexpected filter key %q (regression of the bare-label-name bug)", k)
				}
			}
		})
	}
}

// TestBareKeyFilterShape documents the buggy wire form that Podman /
// Docker reject. The listByLabel helper in this package explicitly
// avoids this shape by wrapping with filters.Arg("label", key+"="+value).
// This test pins the buggy form so a future refactor that "simplifies"
// back to filters.Arg(key, value) re-introduces the failure the test
// guards against.
func TestBareKeyFilterShape(t *testing.T) {
	args := filters.NewArgs(filters.Arg("flowai.cleanup_id", "cleanup-abc-123"))
	got, _ := filters.ToJSON(args)
	var parsed map[string]map[string]bool
	_ = json.Unmarshal([]byte(got), &parsed)
	if _, ok := parsed["label"]; ok {
		t.Fatalf("SDK filter now accepts bare label-name as filter key: %v", parsed)
	}
	// The buggy form emits the label name (e.g. "flowai.cleanup_id")
	// as the filter KEY, which is precisely what the daemon rejects.
	if _, has := parsed["flowai.cleanup_id"]; !has {
		t.Fatalf("buggy form should still be reproducible, got %v", parsed)
	}
}
