package httpapi

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestNew(t *testing.T) {
	static := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<title>FlowAI v2</title>")},
	}
	tests := []struct {
		name        string
		path        string
		wantStatus  int
		wantContent string
	}{
		{name: "health", path: "/healthz", wantStatus: http.StatusOK, wantContent: `{"status":"ok"}`},
		{name: "index", path: "/", wantStatus: http.StatusOK, wantContent: "FlowAI v2"},
		{name: "unknown asset", path: "/missing.js", wantStatus: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			New(fs.FS(static)).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if tt.wantContent != "" && !strings.Contains(recorder.Body.String(), tt.wantContent) {
				t.Fatalf("body = %q, want content %q", recorder.Body.String(), tt.wantContent)
			}
		})
	}
}
