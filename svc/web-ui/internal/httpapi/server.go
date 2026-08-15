// Package httpapi defines the Web UI HTTP surface.
package httpapi

import (
	"io/fs"
	"net/http"
)

func New(static fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.Handle("GET /", http.FileServerFS(static))
	return mux
}
