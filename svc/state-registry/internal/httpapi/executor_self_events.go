package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
	"github.com/flowai/platform/svc/state-registry/internal/store"
)

type executorSelfEventHandlers struct {
	logger *slog.Logger
	repo   store.ExecutorEventRepository
}

// RegisterExecutorSelfEvents mounts the Executor self-event write
// route. The route accepts lifecycle, health, and capacity events
// from the authenticated Executor; capacity values are mirrored onto
// the executor row in the same transaction but never gate work.
func RegisterExecutorSelfEvents(r chi.Router, logger *slog.Logger, repo store.ExecutorEventRepository) {
	h := &executorSelfEventHandlers{logger: logger, repo: repo}
	r.With(h.requireExecutorIdentity).Post("/v1/executors/{executor_id}/events", h.append)
}

// RegisterExecutorSelfEventReads mounts the trusted-Gateway executor
// self-event history route.
func RegisterExecutorSelfEventReads(r chi.Router, logger *slog.Logger, repo store.ExecutorEventRepository) {
	h := &executorSelfEventHandlers{logger: logger, repo: repo}
	r.With(h.requireTrustedGateway).Get("/v1/executors/{executor_id}/events", h.list)
}

// requireExecutorIdentity validates the request-time Executor
// identity header shape and the URL path executor_id. The X-FlowAI
// headers are trusted request data after v0009; the canonical
// record reconciles scope/team in the handler.
func (h *executorSelfEventHandlers) requireExecutorIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		executorID := r.Header.Get(executorIDHeader)
		if executorID == "" {
			executorID = chi.URLParam(r, "executor_id")
		}
		if !validListingIdentifier(executorID) {
			h.writeError(w, r, http.StatusUnauthorized, "unauthenticated", "Executor identity is invalid")
			return
		}
		if teamID := strings.TrimSpace(r.Header.Get(executorTeamIDHeader)); teamID != "" && !validListingIdentifier(teamID) {
			h.writeError(w, r, http.StatusUnauthorized, "unauthenticated", "Executor team binding is invalid")
			return
		}
		pathExecutorID := chi.URLParam(r, "executor_id")
		if !validListingIdentifier(pathExecutorID) || pathExecutorID != executorID {
			h.writeError(w, r, http.StatusNotFound, "executor_unknown", "Executor is unknown")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *executorSelfEventHandlers) requireTrustedGateway(next http.Handler) http.Handler {
	return requireTrustedGateway(next)
}

func (h *executorSelfEventHandlers) append(w http.ResponseWriter, r *http.Request) {
	executorID := chi.URLParam(r, "executor_id")
	if !validListingIdentifier(executorID) {
		h.writeError(w, r, http.StatusNotFound, "executor_unknown", "Executor is unknown")
		return
	}

	raw, ok := decodeExecutorSelfEventBody(w, r, h)
	if !ok {
		return
	}

	var req platform.ExecutorEventAppendRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	if !validListingIdentifier(req.EventID) || !validListingIdentifier(req.ExecutorID) {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "event_id and executor_id are required")
		return
	}
	if req.ExecutorID != executorID {
		h.writeError(w, r, http.StatusNotFound, "executor_unknown", "Executor is unknown")
		return
	}
	if _, err := time.Parse(time.RFC3339Nano, req.OccurredAt); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "occurred_at is required and must be RFC3339Nano")
		return
	}
	if req.TaskID != nil && !validListingIdentifier(*req.TaskID) {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "task_id is invalid")
		return
	}
	if req.TeamID != nil && !validListingIdentifier(*req.TeamID) {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "team_id is invalid")
		return
	}
	if !validTaskEventJSON(req.Payload) {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "payload is invalid")
		return
	}

	identity, ok := selfEventExecutorIdentity(w, r, h)
	if !ok {
		return
	}

	event, err := h.repo.AppendExecutorEvent(r.Context(), req, identity)
	if err != nil {
		h.mapAppendExecutorEventError(w, r, err)
		return
	}

	JSON(w, http.StatusAccepted, platform.EventAcceptance{
		EventID:    event.EventID,
		TeamID:     teamDisplay(event.TeamID),
		AcceptedAt: event.OccurredAt,
	})
}

func (h *executorSelfEventHandlers) list(w http.ResponseWriter, r *http.Request) {
	executorID := strings.TrimSpace(chi.URLParam(r, "executor_id"))
	if !validListingIdentifier(executorID) {
		h.writeError(w, r, http.StatusNotFound, "executor_unknown", "Executor is unknown")
		return
	}
	events, err := h.repo.ListExecutorEvents(r.Context(), gatewayContext(r).TeamID, executorID)
	if err != nil {
		if errors.Is(err, store.ErrExecutorNotFound) {
			h.writeError(w, r, http.StatusNotFound, "executor_unknown", "Executor is unknown")
			return
		}
		h.logger.Error("gateway list executor events failed", "request_id", requestID(r), "error", err)
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "request could not be completed")
		return
	}
	JSON(w, http.StatusOK, platform.ExecutorEventPage{
		Items: events,
		Page:  platform.AdminPageInfo{Count: len(events)},
	})
}

func (h *executorSelfEventHandlers) mapAppendExecutorEventError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrExecutorNotFound):
		h.writeError(w, r, http.StatusNotFound, "executor_unknown", "Executor is unknown")
	case errors.Is(err, store.ErrEventNotFound):
		h.writeError(w, r, http.StatusNotFound, "executor_unknown", "Executor is unknown")
	case errors.Is(err, store.ErrEventTeamMismatch):
		h.writeError(w, r, http.StatusForbidden, "team_mismatch", "team_id does not match the authenticated Executor")
	case errors.Is(err, store.ErrEventConflict):
		h.writeError(w, r, http.StatusConflict, "event_ordering_conflict", "executor event ordering conflict")
	default:
		h.logger.Error("executor event append failed", "request_id", requestID(r), "error", err)
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "request could not be completed")
	}
}

func selfEventExecutorIdentity(w http.ResponseWriter, r *http.Request, h *executorSelfEventHandlers) (platform.ExecutorIdentity, bool) {
	role := r.Header.Get(adminRoleHeader)
	executorID := r.Header.Get(executorIDHeader)
	if role == executorTeamRole {
		teamID := strings.TrimSpace(r.Header.Get(executorTeamIDHeader))
		return platform.ExecutorIdentity{
			ExecutorID: executorID,
			Scope:      platform.ExecutorScopeTeam,
			TeamID:     &teamID,
			RequestID:  requestID(r),
		}, true
	}
	return platform.ExecutorIdentity{
		ExecutorID: executorID,
		Scope:      platform.ExecutorScopeSystem,
		RequestID:  requestID(r),
	}, true
}

func decodeExecutorSelfEventBody(w http.ResponseWriter, r *http.Request, h *executorSelfEventHandlers) ([]byte, bool) {
	contentType := r.Header.Get("Content-Type")
	if contentType != "" && !strings.HasPrefix(contentType, "application/json") {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "content type must be application/json")
		return nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminRequestBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return nil, false
	}
	if len(raw) == 0 {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "request body is required")
		return nil, false
	}
	if !json.Valid(raw) {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return nil, false
	}
	return raw, true
}

// teamDisplay returns the EventAcceptance.team_id string for an
// appended event. The wire shape is non-null for team-owned
// Executors and stable for the duration of the accepted_at response.
func teamDisplay(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (h *executorSelfEventHandlers) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	JSON(w, status, errorResponse{Code: code, Message: message, RequestID: requestID(r)})
}
