// lifecycle-manager entrypoint placeholder. Real implementation lands later.
package main

import (
	"fmt"
	"net/http"

	"github.com/flowai/apps/lifecycle-manager/internal/health"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(health.OK("lifecycle-manager").Marshal())
	})
	addr := ":8081"
	fmt.Println("lifecycle-manager listening on", addr)
	_ = http.ListenAndServe(addr, mux)
}
