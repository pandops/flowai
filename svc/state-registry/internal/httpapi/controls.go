package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
	"github.com/flowai/platform/svc/state-registry/internal/store"
)

type controlHandlers struct {
	logger *slog.Logger
	repo   store.ControlRepository
}

func RegisterTaskControls(r chi.Router, logger *slog.Logger, repo store.ControlRepository) {
	h := &controlHandlers{logger: logger, repo: repo}
	r.With(h.requireTrustedGateway).Post("/v1/tasks/{task_id}/controls", h.create)
	r.With(h.requireExecutorIdentity).Get("/v1/tasks/{task_id}/controls", h.listAssigned)
	r.With(h.requireTrustedGateway).Get("/v1/tasks/{task_id}/controls/{control_id}", h.get)
}

func (h *controlHandlers) requireTrustedGateway(next http.Handler) http.Handler {
	return requireTrustedGatewayRequestID(next)
}

// requireExecutorIdentity resolves the request-time Executor
// identity from the URL path and the X-FlowAI-* headers (trusted
// request data after v0009). The X-FlowAI-Role header is no longer
// used to reject non-Executor callers; the canonical record lookup
// in the handler is the source of truth.
func (h *controlHandlers) requireExecutorIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		executorID := strings.TrimSpace(r.Header.Get(executorIDHeader))
		if !validListingIdentifier(executorID) {
			h.writeError(w, r, http.StatusUnauthorized, "unauthenticated", "Executor identity is required")
			return
		}
		teamID := strings.TrimSpace(r.Header.Get(executorTeamIDHeader))
		if teamID != "" && !validListingIdentifier(teamID) {
			h.writeError(w, r, http.StatusUnauthorized, "unauthenticated", "Executor team binding is invalid")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *controlHandlers) create(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task_id"))
	if !validListingIdentifier(taskID) {
		h.writeError(w, r, http.StatusNotFound, "task_not_found", "task is unknown or unavailable")
		return
	}
	var req platform.CreateTaskControlRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAdminRequestBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || (req.Action != "cancel" && req.Action != "interrupt") ||
		!validListingIdentifier(req.IdempotencyKey) {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "control request is invalid")
		return
	}
	gateway := gatewayContext(r)
	identity := platform.GatewayIdentity{TeamID: gateway.TeamID, OperatorID: gateway.OperatorID, RequestID: gateway.RequestID}
	control, err := h.repo.CreateTaskControl(r.Context(), identity, taskID, req)
	if err != nil {
		if errors.Is(err, store.ErrTaskUnknown) {
			h.writeError(w, r, http.StatusNotFound, "task_not_found", "task is unknown or unavailable")
			return
		}
		h.logger.Error("create task control failed", "request_id", requestID(r), "error", err)
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "request could not be completed")
		return
	}
	JSON(w, http.StatusAccepted, control)
}

func (h *controlHandlers) listAssigned(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task_id"))
	if !validListingIdentifier(taskID) {
		h.writeError(w, r, http.StatusNotFound, "task_not_found", "task is unknown or unavailable")
		return
	}
	identity := taskEventExecutorIdentity(r)
	controls, err := h.repo.ListAssignedTaskControls(r.Context(), taskID, identity)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrTaskUnknown), errors.Is(err, store.ErrExecutorNotFound):
			h.writeError(w, r, http.StatusNotFound, "task_not_found", "task is unknown or unavailable")
		case errors.Is(err, store.ErrNotAssigned):
			h.writeError(w, r, http.StatusForbidden, "not_assigned", "Executor is not assigned to the task")
		default:
			h.logger.Error("list assigned task controls failed", "request_id", requestID(r), "error", err)
			h.writeError(w, r, http.StatusInternalServerError, "internal_error", "request could not be completed")
		}
		return
	}
	JSON(w, http.StatusOK, platform.ControlPage{Items: controls})
}

func (h *controlHandlers) get(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task_id"))
	controlID := strings.TrimSpace(chi.URLParam(r, "control_id"))
	if !validListingIdentifier(taskID) || !validListingIdentifier(controlID) {
		h.writeError(w, r, http.StatusNotFound, "resource_not_found", "resource is unknown or unavailable")
		return
	}
	control, err := h.repo.GetTaskControl(r.Context(), gatewayContext(r).TeamID, taskID, controlID)
	if err != nil {
		if errors.Is(err, store.ErrTaskUnknown) {
			h.writeError(w, r, http.StatusNotFound, "resource_not_found", "resource is unknown or unavailable")
			return
		}
		h.logger.Error("get task control failed", "request_id", requestID(r), "error", err)
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "request could not be completed")
		return
	}
	JSON(w, http.StatusOK, control)
}

func (h *controlHandlers) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	JSON(w, status, errorResponse{Code: code, Message: message, RequestID: requestID(r)})
}
