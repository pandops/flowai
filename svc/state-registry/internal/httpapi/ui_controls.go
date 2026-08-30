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

type uiControlHandlers struct {
	logger *slog.Logger
	repo   store.ControlRepository
}

func RegisterUIControls(r chi.Router, logger *slog.Logger, repo store.ControlRepository) {
	h := &uiControlHandlers{logger: logger, repo: repo}
	r.Post("/ui/v1/teams/{team_id}/tasks/{task_id}/controls/cancel", h.cancel)
}

func (h *uiControlHandlers) cancel(w http.ResponseWriter, r *http.Request) {
	teamID, ok := uiTeamID(w, r)
	if !ok {
		return
	}
	taskID := strings.TrimSpace(chi.URLParam(r, "task_id"))
	if taskID == "" || !validListingIdentifier(taskID) {
		uiError(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	var body struct {
		RequestID string `json:"request_id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAdminRequestBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || strings.TrimSpace(body.RequestID) == "" || !validListingIdentifier(body.RequestID) {
		uiError(w, http.StatusBadRequest, "invalid_request", "request_id is invalid")
		return
	}
	identity := uiIdentity(r, teamID)
	identity.RequestID = body.RequestID
	control, err := h.repo.CreateTaskControl(r.Context(), identity, taskID, platform.CreateTaskControlRequest{
		Action: "cancel", IdempotencyKey: body.RequestID,
	})
	if err != nil {
		if errors.Is(err, store.ErrTaskUnknown) {
			uiError(w, http.StatusNotFound, "not_found", "resource not found")
			return
		}
		h.logger.Error("UI task cancel failed", "request_id", body.RequestID, "error", err)
		uiError(w, http.StatusInternalServerError, "internal_error", "request could not be completed")
		return
	}
	JSON(w, http.StatusAccepted, platform.Control{ControlID: control.ControlID, Status: control.Status})
}

type taskControlEventHandlers struct {
	logger *slog.Logger
	repo   store.ControlEventRepository
}

func RegisterTaskControlEvents(r chi.Router, logger *slog.Logger, repo store.ControlEventRepository) {
	h := &taskControlEventHandlers{logger: logger, repo: repo}
	r.With((&controlHandlers{}).requireExecutorIdentity).Post("/v1/tasks/{task_id}/controls/{control_id}/events", h.append)
	r.Get("/ui/v1/teams/{team_id}/tasks/{task_id}/controls/{control_id}/events", h.list)
}

func (h *taskControlEventHandlers) append(w http.ResponseWriter, r *http.Request) {
	taskID, controlID, ok := controlEventPath(r)
	if !ok {
		uiError(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	var req platform.TaskControlEventAppendRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAdminRequestBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil || req.ControlEventID == "" || !validListingIdentifier(req.ControlEventID) ||
		(req.Status != "acknowledged" && req.Status != "completed" && req.Status != "failed") ||
		req.OccurredAt.IsZero() || (len(req.Payload) > 0 && !json.Valid(req.Payload)) {
		uiError(w, http.StatusBadRequest, "invalid_request", "control event is invalid")
		return
	}
	item, err := h.repo.AppendTaskControlEvent(r.Context(), taskID, controlID, req, taskEventExecutorIdentity(r))
	if err != nil {
		switch {
		case errors.Is(err, store.ErrTaskUnknown):
			uiError(w, http.StatusNotFound, "not_found", "resource not found")
		case errors.Is(err, store.ErrControlEventConflict):
			uiError(w, http.StatusConflict, "event_conflict", "control event conflicts with current state")
		default:
			h.logger.Error("append task control event failed", "request_id", requestID(r), "error", err)
			uiError(w, http.StatusInternalServerError, "internal_error", "request could not be completed")
		}
		return
	}
	JSON(w, http.StatusAccepted, item)
}

func (h *taskControlEventHandlers) list(w http.ResponseWriter, r *http.Request) {
	teamID, ok := uiTeamID(w, r)
	if !ok {
		return
	}
	taskID, controlID, ok := controlEventPath(r)
	if !ok {
		uiError(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	limit, ok := uiLimit(w, r)
	if !ok {
		return
	}
	items, err := h.repo.ListTaskControlEvents(r.Context(), teamID, taskID, controlID, limit)
	if err != nil {
		h.logger.Error("list task control events failed", "request_id", requestID(r), "error", err)
		uiError(w, http.StatusInternalServerError, "internal_error", "request could not be completed")
		return
	}
	JSON(w, http.StatusOK, platform.TaskControlEventPage{Items: items})
}

func controlEventPath(r *http.Request) (string, string, bool) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task_id"))
	controlID := strings.TrimSpace(chi.URLParam(r, "control_id"))
	return taskID, controlID, taskID != "" && controlID != "" && validListingIdentifier(taskID) && validListingIdentifier(controlID)
}
