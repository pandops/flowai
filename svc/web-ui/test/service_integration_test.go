package test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flowai/platform/svc/web-ui/internal/httpapi"
	webassets "github.com/flowai/platform/svc/web-ui/web"
)

func TestEmbeddedServiceSurface(t *testing.T) {
	server := httptest.NewServer(httpapi.New(webassets.Files))
	t.Cleanup(server.Close)

	tests := []struct {
		name        string
		path        string
		wantContent string
	}{
		{name: "health", path: "/healthz", wantContent: `{"status":"ok"}`},
		{name: "operator interface", path: "/", wantContent: "FlowAI v2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, err := http.Get(server.URL + tt.path)
			if err != nil {
				t.Fatalf("GET %s: %v", tt.path, err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
			}
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if !strings.Contains(string(body), tt.wantContent) {
				t.Fatalf("body does not contain %q", tt.wantContent)
			}
		})
	}
}
