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

	"github.com/flowai/platform/state-registry/internal/platform"
	"github.com/flowai/platform/state-registry/internal/store"
)

type taskEventHandlers struct {
	logger *slog.Logger
	repo   store.TaskEventRepository
}

// RegisterTaskEventReads mounts the trusted-Gateway task-event history route.
func RegisterTaskEventReads(r chi.Router, logger *slog.Logger, repo store.TaskEventRepository) {
	h := &taskEventHandlers{logger: logger, repo: repo}
	r.With(h.requireTrustedGateway).Get("/v1/tasks/{task_id}/events", h.list)
}

// RegisterTaskEventWrites mounts the Executor-emitted task lifecycle
// event route. The route accepts `running`, `finished`, or `failed`
// from the authenticated Executor currently assigned to the task.
func RegisterTaskEventWrites(r chi.Router, logger *slog.Logger, repo store.TaskEventRepository) {
	h := &taskEventHandlers{logger: logger, repo: repo}
	r.With(h.requireExecutorIdentity).Post("/v1/tasks/{task_id}/events", h.append)
}

func (h *taskEventHandlers) requireTrustedGateway(next http.Handler) http.Handler {
	return requireTrustedGateway(next)
}

func (h *taskEventHandlers) requireExecutorIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := r.Header.Get(adminRoleHeader)
		executorID := r.Header.Get(executorIDHeader)
		if role == "" || executorID == "" {
			h.writeError(w, r, http.StatusUnauthorized, "unauthenticated", "Executor authentication is required")
			return
		}
		if role != executorTeamRole && role != executorSysRole {
			h.writeError(w, r, http.StatusForbidden, "not_authorized", "Executor authorization is required")
			return
		}
		if !validListingIdentifier(executorID) {
			h.writeError(w, r, http.StatusUnauthorized, "unauthenticated", "Executor identity is invalid")
			return
		}
		if r.Header.Get(executorTeamIDHeader) != "" && role != executorTeamRole {
			h.writeError(w, r, http.StatusForbidden, "not_authorized", "system Executor must not carry a team binding")
			return
		}
		if role == executorTeamRole {
			teamID := strings.TrimSpace(r.Header.Get(executorTeamIDHeader))
			if teamID == "" || !validListingIdentifier(teamID) {
				h.writeError(w, r, http.StatusUnauthorized, "unauthenticated", "team Executor authentication is required")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (h *taskEventHandlers) list(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task_id"))
	if !validListingIdentifier(taskID) {
		h.writeError(w, r, http.StatusNotFound, "task_not_found", "task is unknown or unavailable")
		return
	}
	events, err := h.repo.ListTaskEvents(r.Context(), gatewayContext(r).TeamID, taskID)
	if err != nil {
		if errors.Is(err, store.ErrTaskNotFound) {
			h.writeError(w, r, http.StatusNotFound, "task_not_found", "task is unknown or unavailable")
			return
		}
		h.logger.Error("gateway list task events failed", "request_id", requestID(r), "error", err)
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "request could not be completed")
		return
	}
	JSON(w, http.StatusOK, platform.TaskEventPage{
		Items: events,
		Page:  platform.AdminPageInfo{Count: len(events)},
	})
}

func (h *taskEventHandlers) append(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task_id"))
	if !validListingIdentifier(taskID) {
		h.writeError(w, r, http.StatusNotFound, "task_not_found", "task is unknown or unavailable")
		return
	}

	raw, ok := decodeTaskEventBody(w, r, h)
	if !ok {
		return
	}
	if containsAcceptedSequenceKey(raw) {
		h.writeError(w, r, http.StatusConflict, "invalid_task_transition", "accepted_sequence and recovery overrides are not accepted")
		return
	}

	var req platform.TaskEventAppendRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	// Reject self-event types at the task-event boundary BEFORE the
	// task_id match check: the task_id on a self envelope is null
	// or absent by contract, and a self envelope that lands on the
	// task-event route is a stream-crossing mistake the boundary
	// MUST surface as 403 self_event_type_not_allowed.
	if !isAllowedTaskEventType(req.EventType) {
		h.writeError(w, r, http.StatusForbidden, "self_event_type_not_allowed", "self event types are not accepted on the task event surface")
		return
	}
	if !validListingIdentifier(req.EventID) || !validListingIdentifier(req.TaskID) ||
		!validListingIdentifier(req.ExecutorID) || !validListingIdentifier(req.TeamID) {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "event_id, task_id, executor_id, team_id are required and must be valid identifiers")
		return
	}
	if req.TaskID != taskID {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "task_id in body does not match the path")
		return
	}
	if _, err := time.Parse(time.RFC3339Nano, req.OccurredAt); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "occurred_at is required and must be RFC3339Nano")
		return
	}
	if !validTaskEventJSON(req.Payload) {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "payload is required and must be a JSON object")
		return
	}

	identity := taskEventExecutorIdentity(r)
	if identity.ExecutorID != req.ExecutorID {
		h.writeError(w, r, http.StatusForbidden, "not_authorized", "executor_id does not match the authenticated Executor")
		return
	}

	event, err := h.repo.AppendTaskEvent(r.Context(), req, identity)
	if err != nil {
		h.mapAppendTaskEventError(w, r, err)
		return
	}
	JSON(w, http.StatusAccepted, platform.EventAcceptance{
		EventID:    event.EventID,
		TeamID:     event.TeamID,
		AcceptedAt: event.OccurredAt,
	})
}

func (h *taskEventHandlers) mapAppendTaskEventError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrEventNotFound):
		h.writeError(w, r, http.StatusNotFound, "task_not_found", "task is unknown or unavailable")
	case errors.Is(err, store.ErrExecutorNotFound):
		h.writeError(w, r, http.StatusNotFound, "executor_unknown", "Executor is unknown")
	case errors.Is(err, store.ErrNotAssigned):
		h.writeError(w, r, http.StatusForbidden, "not_assigned", "Executor is not assigned to the task")
	case errors.Is(err, store.ErrEventTeamMismatch):
		h.writeError(w, r, http.StatusForbidden, "team_mismatch", "team_id does not match the authenticated Executor")
	case errors.Is(err, store.ErrEventConflict):
		h.writeError(w, r, http.StatusConflict, "invalid_task_transition", "event is not strictly newer than the latest accepted event")
	case errors.Is(err, store.ErrInvalidTaskTransition):
		h.writeError(w, r, http.StatusConflict, "invalid_task_transition", "the task cannot accept the requested event")
	default:
		msg := err.Error()
		if strings.Contains(msg, "is not accepted on the task event surface") {
			h.writeError(w, r, http.StatusForbidden, "self_event_type_not_allowed", "self event types are not accepted on the task event surface")
			return
		}
		h.logger.Error("task event append failed", "request_id", requestID(r), "error", err)
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "request could not be completed")
	}
}

func taskEventExecutorIdentity(r *http.Request) platform.ExecutorIdentity {
	role := r.Header.Get(adminRoleHeader)
	executorID := r.Header.Get(executorIDHeader)
	if role == executorTeamRole {
		teamID := strings.TrimSpace(r.Header.Get(executorTeamIDHeader))
		return platform.ExecutorIdentity{
			ExecutorID: executorID,
			Scope:      platform.ExecutorScopeTeam,
			TeamID:     &teamID,
			RequestID:  requestID(r),
		}
	}
	return platform.ExecutorIdentity{
		ExecutorID: executorID,
		Scope:      platform.ExecutorScopeSystem,
		RequestID:  requestID(r),
	}
}

func decodeTaskEventBody(w http.ResponseWriter, r *http.Request, h *taskEventHandlers) ([]byte, bool) {
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

// isAllowedTaskEventType reports whether the supplied event_type is
// one of the post-create Executor-emitted task lifecycle event
// types the route accepts. The Registry-appended `created` event
// is NOT in this set because the route never accepts it.
func isAllowedTaskEventType(eventType string) bool {
	switch eventType {
	case platform.TaskEventTypeRunning,
		platform.TaskEventTypeFinished,
		platform.TaskEventTypeFailed:
		return true
	}
	return false
}

// containsAcceptedSequenceKey walks the JSON body looking for any
// `accepted_sequence` key. The detection runs on the raw bytes
// before the typed decode so a client cannot smuggle the override
// through any nesting depth.
func containsAcceptedSequenceKey(raw []byte) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	return walkAcceptedSequence(v)
}

func walkAcceptedSequence(v any) bool {
	switch n := v.(type) {
	case map[string]any:
		for k, child := range n {
			if k == "accepted_sequence" {
				return true
			}
			if walkAcceptedSequence(child) {
				return true
			}
		}
	case []any:
		for _, child := range n {
			if walkAcceptedSequence(child) {
				return true
			}
		}
	}
	return false
}

func validTaskEventJSON(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return true
	}
	if trimmed[0] != '{' && trimmed[0] != '[' {
		return false
	}
	return json.Valid(raw)
}

func (h *taskEventHandlers) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	JSON(w, status, errorResponse{Code: code, Message: message, RequestID: requestID(r)})
}
