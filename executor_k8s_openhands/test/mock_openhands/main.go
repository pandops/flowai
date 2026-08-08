package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/api/conversations", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			InitialMessage struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"initial_message"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		conversationID := fmt.Sprintf("conv-%d", time.Now().UnixNano())
		if len(body.InitialMessage.Content) > 0 {
			prompt := body.InitialMessage.Content[0].Text
			switch {
			case strings.Contains(prompt, "restart"):
				conversationID = "restart-" + conversationID
			case strings.Contains(prompt, "hold"):
				conversationID = "hold-" + conversationID
			}
		}
		writeJSON(w, http.StatusCreated, map[string]string{"id": conversationID, "execution_status": "idle"})
	})
	mux.HandleFunc("/api/conversations/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || (!strings.HasSuffix(r.URL.Path, "/pause") && !strings.HasSuffix(r.URL.Path, "/events")) {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"success": true})
	})
	mux.HandleFunc("/sockets/events/", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if strings.Contains(r.URL.Path, "/hold-") {
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}
		if strings.Contains(r.URL.Path, "/restart-") {
			time.Sleep(8 * time.Second)
			_ = conn.WriteJSON(map[string]string{
				"type": "conversation.status", "execution_status": "finished",
			})
			return
		}
		time.Sleep(250 * time.Millisecond)
		_ = conn.WriteJSON(map[string]string{
			"type": "conversation.status", "execution_status": "finished",
		})
	})
	log.Fatal(http.ListenAndServe(":8000", mux))
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

var upgrader = websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
