package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/flowai/platform/executor_k8s_openhands/internal/httpapi"
)

func newTestRouter() http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := httpapi.NewRouter("test", logger)
	httpapi.RegisterProbes(r, "test", "exec-abc", httpapi.ReadinessFunc(func() bool {
		return true
	}))
	mux := http.NewServeMux()
	mux.Handle("/v1/", http.StripPrefix("/v1", r))
	return mux
}

func TestLivezReturnsOK(t *testing.T) {
	srv := httptest.NewServer(newTestRouter())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/livez")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status field: %v", body["status"])
	}
	if body["executor_id"] != "exec-abc" {
		t.Fatalf("executor_id field: %v", body["executor_id"])
	}
}

func TestReadyzReturnsOK(t *testing.T) {
	srv := httptest.NewServer(newTestRouter())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/readyz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ready" {
		t.Fatalf("status field: %v", body["status"])
	}
}

func TestReadyzReturnsNotReady(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := httpapi.NewRouter("test", logger)
	httpapi.RegisterProbes(r, "test", "exec-abc", httpapi.ReadinessFunc(func() bool {
		return false
	}))
	mux := http.NewServeMux()
	mux.Handle("/v1/", http.StripPrefix("/v1", r))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/readyz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if got := body["status"]; got != "ready" {
		t.Fatalf("status field: %v", got)
	}
	if got, _ := body["state_registry_registered"].(bool); got {
		t.Fatalf("state_registry_registered: %v", got)
	}
}

func TestProbeConcurrency(t *testing.T) {
	probe := &httpapi.AtomicBool{}
	probe.Set(true)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := httpapi.NewRouter("test", logger)
	httpapi.RegisterProbes(r, "test", "exec-x", httpapi.ReadinessFunc(func() bool {
		return probe.Get()
	}))
	mux := http.NewServeMux()
	mux.Handle("/v1/", http.StripPrefix("/v1", r))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := 0; i < 100; i++ {
		go func() {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/readyz", nil)
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
	}
	probe.Set(false)
	resp, err := http.Get(srv.URL + "/v1/readyz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}
