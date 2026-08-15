package httpapi

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	"github.com/flowai/platform/svc/state-registry/internal/store"
)

// eventsStreamPath is the team-bound event WebSocket upgrade route
// for v0002.20. The route is mounted on every router (production
// and harness) — production security comes from the verified
// peer-identity middleware in cmd/state-registry/main.go, not from
// mount-time gating.
const eventsStreamPath = "/v1/events/stream"

// eventsStreamUpgrader accepts only absent Origin or a loopback
// hostname (127.0.0.1, ::1, localhost). The v0002.20 contract test
// dials wss://127.0.0.1 / wss://localhost; production semantics
// (origin pinning to the trusted Gateway identity) ship later.
var eventsStreamUpgrader = websocket.Upgrader{
	HandshakeTimeout: 10 * time.Second,
	CheckOrigin:      allowLoopbackOrigin,
}

func allowLoopbackOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Hostname() == "" {
		return false
	}
	switch u.Hostname() {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	return false
}

type eventsStreamHandlers struct {
	logger *slog.Logger
	repo   store.StreamRepository
	uiRepo store.UIStreamRepository
}

// RegisterEventsStream mounts GET /v1/events/stream under the
// trusted-Gateway identity middleware. Production security comes
// from the verified peer-identity middleware in
// cmd/state-registry/main.go: only a cert whose subject parses
// into role=gateway reaches this adapter. The mounting itself is
// unconditional so a production router always has the route.
func RegisterEventsStream(r chi.Router, logger *slog.Logger, repositories ...store.StreamRepository) {
	if logger == nil {
		return
	}
	var repo store.StreamRepository
	if len(repositories) > 0 {
		repo = repositories[0]
	}
	h := &eventsStreamHandlers{logger: logger, repo: repo}
	if candidate, ok := any(repo).(store.UIStreamRepository); ok {
		h.uiRepo = candidate
	}
	r.With(h.requireTrustedGateway).Get(eventsStreamPath, h.stream)
	r.Get("/ui/v1/teams/{team_id}/stream", h.uiStream)
}

func (h *eventsStreamHandlers) uiStream(w http.ResponseWriter, r *http.Request) {
	teamID := strings.TrimSpace(chi.URLParam(r, "team_id"))
	if teamID == "" || !validListingIdentifier(teamID) || h.uiRepo == nil {
		uiError(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	conn, err := eventsStreamUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	taskID := strings.TrimSpace(r.URL.Query().Get("task_id"))
	if taskID != "" && !validListingIdentifier(taskID) {
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	after := time.Now().UTC()
	if rawAfter := strings.TrimSpace(r.URL.Query().Get("after")); rawAfter != "" {
		parsed, parseErr := time.Parse(time.RFC3339Nano, rawAfter)
		if parseErr != nil {
			return
		}
		after = parsed.UTC()
	}
	afterFrameID := strings.TrimSpace(r.URL.Query().Get("after_frame_id"))
	if len(afterFrameID) > 200 {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-done:
			return
		case <-ticker.C:
			frames, err := h.uiRepo.ListUIStreamFrames(r.Context(), teamID, taskID, after, afterFrameID)
			if err != nil {
				h.logger.Error("UI stream read failed", "request_id", requestID(r), "error", err)
				return
			}
			for _, frame := range frames {
				if err := conn.WriteJSON(frame); err != nil {
					return
				}
				occurredAt, err := time.Parse(time.RFC3339Nano, frame.OccurredAt)
				if err != nil {
					return
				}
				after, afterFrameID = occurredAt, frame.FrameID
			}
		}
	}
}

func (h *eventsStreamHandlers) requireTrustedGateway(next http.Handler) http.Handler {
	return requireTrustedGatewayUpgrade(next)
}

func (h *eventsStreamHandlers) stream(w http.ResponseWriter, r *http.Request) {
	conn, err := eventsStreamUpgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Debug("events stream upgrade failed",
			"request_id", requestID(r),
			"error", err,
		)
		return
	}
	defer conn.Close()
	if h.repo == nil {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}

	taskID := strings.TrimSpace(r.URL.Query().Get("task_id"))
	if taskID != "" && !validListingIdentifier(taskID) {
		taskID = "invalid"
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	after := time.Now().UTC()
	afterEventID := ""
	for {
		select {
		case <-r.Context().Done():
			return
		case <-done:
			return
		case <-ticker.C:
			events, err := h.repo.ListStreamTaskEvents(r.Context(), gatewayContext(r).TeamID, taskID, after, afterEventID)
			if err != nil {
				h.logger.Error("events stream read failed", "request_id", requestID(r), "error", err)
				return
			}
			for _, event := range events {
				if err := conn.WriteJSON(event); err != nil {
					return
				}
				occurredAt, err := time.Parse(time.RFC3339Nano, event.OccurredAt)
				if err != nil {
					return
				}
				after = occurredAt
				afterEventID = event.EventID
			}
		}
	}
}

func (h *eventsStreamHandlers) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	JSON(w, status, errorResponse{Code: code, Message: message, RequestID: requestID(r)})
}
