// router entrypoint placeholder. Real implementation lands later.
package main

import (
	"fmt"
	"net/http"

	"github.com/flowai/apps/router/internal/health"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(health.OK("router").Marshal())
	})
	addr := ":8082"
	fmt.Println("router listening on", addr)
	_ = http.ListenAndServe(addr, mux)
}
