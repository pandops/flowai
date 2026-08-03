package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/state-registry/internal/store"
)

type environmentOpenHandlers struct {
	logger *slog.Logger
	repo   store.OpenEnvironmentRepository
	ops    *DecryptOps
}

func RegisterEnvironmentOpen(r chi.Router, logger *slog.Logger, repo store.OpenEnvironmentRepository, ops *DecryptOps) {
	h := &environmentOpenHandlers{logger: logger, repo: repo, ops: ops}
	r.With(h.requireExecutorIdentity).Get("/v1/environments/{environment_id}/open", h.open)
}

func (h *environmentOpenHandlers) requireExecutorIdentity(next http.Handler) http.Handler {
	return (&controlHandlers{}).requireExecutorIdentity(next)
}

func (h *environmentOpenHandlers) open(w http.ResponseWriter, r *http.Request) {
	environmentID := strings.TrimSpace(chi.URLParam(r, "environment_id"))
	taskID := strings.TrimSpace(r.URL.Query().Get("task_id"))
	token := strings.TrimSpace(r.Header.Get("X-FlowAI-Scope-Token"))
	if environmentID == "" || taskID == "" || token == "" || !validListingIdentifier(environmentID) || !validListingIdentifier(taskID) {
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
		EnvironmentID: environmentID,
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
		Code: "environment_unknown_or_unavailable", Message: "environment is unknown or unavailable", RequestID: requestID(r),
	})
}
