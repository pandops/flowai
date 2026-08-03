package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/state-registry/internal/store"
)

// gatewayTaskPointReadHandlers serves the trusted-Gateway
// GET /v1/tasks/{task_id} point-read surface introduced by v0002.72.
// The handler enforces the documented "role=gateway" + verified
// team_id + operator_id identity check at the chi-router boundary so
// no caller reaches the repository surface without a verified
// Gateway binding.
type gatewayTaskPointReadHandlers struct {
	logger *slog.Logger
	repo   store.TaskPointReadRepository
}

// RegisterGatewayTaskPointRead mounts GET /v1/tasks/{task_id} under
// the trusted-Gateway middleware. The middleware runs before the
// handler so the repository call always carries a verified team_id.
func RegisterGatewayTaskPointRead(r chi.Router, logger *slog.Logger, repo store.TaskPointReadRepository) {
	h := &gatewayTaskPointReadHandlers{logger: logger, repo: repo}
	r.With(h.requireTrustedGateway).Get("/v1/tasks/{task_id}", h.read)
}

func (h *gatewayTaskPointReadHandlers) requireTrustedGateway(next http.Handler) http.Handler {
	return requireTrustedGateway(next)
}

func (h *gatewayTaskPointReadHandlers) read(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task_id"))
	if !validListingIdentifier(taskID) {
		h.writeError(w, r, http.StatusNotFound, "task_not_found", "task is unknown or unavailable")
		return
	}
	entry, err := h.repo.GetTask(r.Context(), gatewayContext(r).TeamID, taskID)
	if err != nil {
		if errors.Is(err, store.ErrTaskUnknown) {
			h.writeError(w, r, http.StatusNotFound, "task_not_found", "task is unknown or unavailable")
			return
		}
		h.logger.Error("gateway task point read failed", "request_id", requestID(r), "error", err)
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "request could not be completed")
		return
	}
	JSON(w, http.StatusOK, entry)
}

func (h *gatewayTaskPointReadHandlers) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	JSON(w, status, errorResponse{Code: code, Message: message, RequestID: requestID(r)})
}
