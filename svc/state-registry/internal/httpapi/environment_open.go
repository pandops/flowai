package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/svc/state-registry/internal/store"
)

type environmentOpenHandlers struct {
	logger *slog.Logger
	repo   store.OpenEnvironmentRepository
	ops    *DecryptOps
}

func RegisterEnvironmentOpen(r chi.Router, logger *slog.Logger, repo store.OpenEnvironmentRepository, ops *DecryptOps) {
	h := &environmentOpenHandlers{logger: logger, repo: repo, ops: ops}
	r.With(h.requireExecutorIdentity).Get("/v1/tasks/{task_id}/launch-parameters/open", h.open)
}

func (h *environmentOpenHandlers) requireExecutorIdentity(next http.Handler) http.Handler {
	return (&controlHandlers{}).requireExecutorIdentity(next)
}

func (h *environmentOpenHandlers) open(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task_id"))
	token := strings.TrimSpace(r.Header.Get("X-FlowAI-Scope-Token"))
	if taskID == "" || token == "" || !validListingIdentifier(taskID) {
		h.notFound(w, r)
		return
	}
	// Build the typed open-environment request with the real
	// request_id so the audit row carries the call-site
	// correlation identifier. The synthetic "open-uuid" audit
	// value the previous implementation used is replaced: the
	// non-revealing 404 envelope still surfaces the operator's
	// request_id without leaking any token or payload material.
	req := store.OpenEnvironmentRequest{
		TaskID:        taskID,
		Token:         token,
		Identity:      taskEventExecutorIdentity(r),
		RequestID:     requestID(r),
		RecordDecrypt: h.ops.Record,
	}
	result, err := h.repo.OpenEnvironment(r.Context(), req)
	if err != nil {
		if !errors.Is(err, store.ErrEnvironmentUnavailable) {
			h.logger.Error("open environment failed", "request_id", requestID(r), "error", err)
		}
		h.notFound(w, r)
		return
	}
	JSON(w, http.StatusOK, result)
}

func (h *environmentOpenHandlers) notFound(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusNotFound, errorResponse{
		Code: "launch_parameters_unknown_or_unavailable", Message: "launch parameters are unknown or unavailable", RequestID: requestID(r),
	})
}
