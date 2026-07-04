// secret-service entrypoint placeholder. Real implementation lands later.
package main

import (
	"fmt"
	"net/http"

	"github.com/flowai/apps/secret-service/internal/health"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(health.OK("secret-service").Marshal())
	})
	addr := ":8083"
	fmt.Println("secret-service listening on", addr)
	_ = http.ListenAndServe(addr, mux)
}
