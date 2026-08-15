package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
	"github.com/flowai/platform/svc/state-registry/internal/store"
)

type taskLogHandlers struct {
	logger *slog.Logger
	repo   store.TaskLogRepository
}

func RegisterTaskLogs(r chi.Router, logger *slog.Logger, repo store.TaskLogRepository) {
	h := &taskLogHandlers{logger: logger, repo: repo}
	r.With((&controlHandlers{}).requireExecutorIdentity).Post("/v1/tasks/{task_id}/logs", h.append)
	r.Get("/ui/v1/teams/{team_id}/tasks/{task_id}/logs", h.list)
}

func (h *taskLogHandlers) append(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task_id"))
	var req platform.TaskLogAppendRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if taskID == "" || !validListingIdentifier(taskID) || decoder.Decode(&req) != nil ||
		req.LogChunkID == "" || !validListingIdentifier(req.LogChunkID) ||
		(req.Stream != platform.TaskLogStreamWork && req.Stream != platform.TaskLogStreamReasoning) ||
		req.Content == "" || len(req.Content) > 65536 || req.OccurredAt.IsZero() {
		uiError(w, http.StatusBadRequest, "invalid_request", "task log chunk is invalid")
		return
	}
	chunk, err := h.repo.AppendTaskLog(r.Context(), taskID, req, taskEventExecutorIdentity(r))
	if err != nil {
		switch {
		case errors.Is(err, store.ErrTaskUnknown):
			uiError(w, http.StatusNotFound, "not_found", "resource not found")
		case errors.Is(err, store.ErrTaskLogConflict):
			uiError(w, http.StatusConflict, "log_chunk_conflict", "log chunk conflicts with stored data")
		default:
			h.logger.Error("append task log failed", "request_id", requestID(r), "error", err)
			uiError(w, http.StatusInternalServerError, "internal_error", "request could not be completed")
		}
		return
	}
	JSON(w, http.StatusAccepted, chunk)
}

func (h *taskLogHandlers) list(w http.ResponseWriter, r *http.Request) {
	teamID, ok := uiTeamID(w, r)
	if !ok {
		return
	}
	taskID := strings.TrimSpace(chi.URLParam(r, "task_id"))
	if taskID == "" || !validListingIdentifier(taskID) {
		uiError(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	limit, ok := uiLimit(w, r)
	if !ok {
		return
	}
	after, ok := parseTaskLogCursor(r.URL.Query().Get("cursor"))
	if !ok {
		uiError(w, http.StatusBadRequest, "invalid_cursor", "cursor is invalid")
		return
	}
	items, err := h.repo.ListTaskLogs(r.Context(), teamID, taskID, after, limit)
	if err != nil {
		if errors.Is(err, store.ErrTaskUnknown) {
			uiError(w, http.StatusNotFound, "not_found", "resource not found")
			return
		}
		h.logger.Error("list task logs failed", "request_id", requestID(r), "error", err)
		uiError(w, http.StatusInternalServerError, "internal_error", "request could not be completed")
		return
	}
	var next *string
	if len(items) == limit {
		cursor := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("log:%d", items[len(items)-1].LogOffset)))
		next = &cursor
	}
	JSON(w, http.StatusOK, platform.TaskLogPage{Items: items, NextCursor: next})
}

func parseTaskLogCursor(raw string) (int64, bool) {
	if raw == "" {
		return 0, true
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || !strings.HasPrefix(string(decoded), "log:") {
		return 0, false
	}
	offset, err := strconv.ParseInt(strings.TrimPrefix(string(decoded), "log:"), 10, 64)
	return offset, err == nil && offset >= 0
}
