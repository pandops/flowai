package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
	"github.com/flowai/platform/svc/state-registry/internal/store"
)

type uiOperatorHandlers struct {
	logger *slog.Logger
	repo   store.UIOperatorRepository
	now    func() time.Time
}

func RegisterUIOperator(r chi.Router, logger *slog.Logger, repo store.UIOperatorRepository) {
	h := &uiOperatorHandlers{logger: logger, repo: repo, now: time.Now}
	base := "/ui/v1/teams/{team_id}"
	r.Get(base+"/dashboard", h.dashboard)
	r.Get(base+"/tasks", h.tasks)
	r.Get(base+"/tasks/{task_id}", h.task)
	r.Get(base+"/tasks/{task_id}/events", h.taskEvents)
	r.Get(base+"/executors", h.executors)
	r.Get(base+"/executors/{executor_id}", h.executor)
	r.Get(base+"/executors/{executor_id}/events", h.executorEvents)
	r.Get(base+"/executors/{executor_id}/tasks", h.executorTasks)
	r.Get(base+"/audit", h.audit)
}

func (h *uiOperatorHandlers) dashboard(w http.ResponseWriter, r *http.Request) {
	teamID, ok := uiTeamID(w, r)
	if !ok {
		return
	}
	period := r.URL.Query().Get("period")
	calendarKey := r.URL.Query().Get(period)
	result, err := h.repo.GetUIDashboard(r.Context(), teamID, period, calendarKey, h.now())
	if err != nil {
		if err.Error() == "invalid period" {
			uiError(w, http.StatusBadRequest, "invalid_period", "period must be week or month")
			return
		}
		if err.Error() == "invalid calendar key" {
			uiError(w, http.StatusBadRequest, "invalid_calendar", "calendar selection is invalid")
			return
		}
		h.internal(w, r, "read dashboard", err)
		return
	}
	JSON(w, http.StatusOK, result)
}

func (h *uiOperatorHandlers) tasks(w http.ResponseWriter, r *http.Request) {
	teamID, ok := uiTeamID(w, r)
	if !ok {
		return
	}
	limit, after, ok := uiCollectionRequest(w, r)
	if !ok {
		return
	}
	filter := platform.UITaskFilter{Status: r.URL.Query().Get("status"), Period: r.URL.Query().Get("period"), Search: strings.TrimSpace(r.URL.Query().Get("search")), Date: r.URL.Query().Get("date")}
	if !validUIStatus(filter.Status) || !validOptionalPeriod(filter.Period) || !validUIDate(filter.Date) {
		uiError(w, http.StatusBadRequest, "invalid_filter", "task filters are invalid")
		return
	}
	items, next, err := h.repo.ListUITasks(r.Context(), teamID, filter, limit, after)
	if err != nil {
		h.internal(w, r, "list tasks", err)
		return
	}
	JSON(w, http.StatusOK, platform.UITaskPage{Items: items, NextCursor: encodeUIAfter(next)})
}

func validUIDate(value string) bool {
	if value == "" {
		return true
	}
	parsed, err := time.Parse(time.DateOnly, value)
	return err == nil && parsed.Format(time.DateOnly) == value
}

func (h *uiOperatorHandlers) task(w http.ResponseWriter, r *http.Request) {
	teamID, taskID, ok := uiTaskPath(w, r)
	if !ok {
		return
	}
	item, err := h.repo.GetUITask(r.Context(), teamID, taskID)
	if errors.Is(err, store.ErrTaskUnknown) {
		uiError(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if err != nil {
		h.internal(w, r, "read task", err)
		return
	}
	JSON(w, http.StatusOK, item)
}

func (h *uiOperatorHandlers) taskEvents(w http.ResponseWriter, r *http.Request) {
	teamID, taskID, ok := uiTaskPath(w, r)
	if !ok {
		return
	}
	limit, after, ok := uiCollectionRequest(w, r)
	if !ok {
		return
	}
	items, next, err := h.repo.ListUITaskEvents(r.Context(), teamID, taskID, limit, after)
	if errors.Is(err, store.ErrTaskUnknown) {
		uiError(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if err != nil {
		h.internal(w, r, "list task events", err)
		return
	}
	JSON(w, http.StatusOK, platform.UIEventPage{Items: items, NextCursor: encodeUIAfter(next)})
}

func (h *uiOperatorHandlers) executors(w http.ResponseWriter, r *http.Request) {
	teamID, ok := uiTeamID(w, r)
	if !ok {
		return
	}
	limit, after, ok := uiCollectionRequest(w, r)
	if !ok {
		return
	}
	filter := platform.UIExecutorFilter{Status: r.URL.Query().Get("status"), Search: strings.TrimSpace(r.URL.Query().Get("search"))}
	if filter.Status != "" && filter.Status != "busy" && filter.Status != "idle" {
		uiError(w, http.StatusBadRequest, "invalid_filter", "executor status is invalid")
		return
	}
	items, next, err := h.repo.ListUIExecutors(r.Context(), teamID, filter, limit, after)
	if err != nil {
		h.internal(w, r, "list executors", err)
		return
	}
	JSON(w, http.StatusOK, platform.UIExecutorPage{Items: items, NextCursor: encodeUIAfter(next)})
}

func (h *uiOperatorHandlers) executor(w http.ResponseWriter, r *http.Request) {
	teamID, executorID, ok := uiExecutorPath(w, r)
	if !ok {
		return
	}
	item, err := h.repo.GetUIExecutor(r.Context(), teamID, executorID)
	if errors.Is(err, store.ErrUIResourceUnknown) {
		uiError(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if err != nil {
		h.internal(w, r, "read executor", err)
		return
	}
	JSON(w, http.StatusOK, item)
}

func (h *uiOperatorHandlers) executorEvents(w http.ResponseWriter, r *http.Request) {
	teamID, executorID, ok := uiExecutorPath(w, r)
	if !ok {
		return
	}
	limit, after, ok := uiCollectionRequest(w, r)
	if !ok {
		return
	}
	items, next, err := h.repo.ListUIExecutorEvents(r.Context(), teamID, executorID, limit, after)
	if errors.Is(err, store.ErrUIResourceUnknown) {
		uiError(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if err != nil {
		h.internal(w, r, "list executor events", err)
		return
	}
	JSON(w, http.StatusOK, platform.UIEventPage{Items: items, NextCursor: encodeUIAfter(next)})
}

func (h *uiOperatorHandlers) executorTasks(w http.ResponseWriter, r *http.Request) {
	teamID, executorID, ok := uiExecutorPath(w, r)
	if !ok {
		return
	}
	limit, after, ok := uiCollectionRequest(w, r)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if !validUIStatus(status) {
		uiError(w, http.StatusBadRequest, "invalid_filter", "task status is invalid")
		return
	}
	items, next, err := h.repo.ListUIExecutorTasks(r.Context(), teamID, executorID, status, limit, after)
	if errors.Is(err, store.ErrUIResourceUnknown) {
		uiError(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if err != nil {
		h.internal(w, r, "list executor tasks", err)
		return
	}
	JSON(w, http.StatusOK, platform.UITaskPage{Items: items, NextCursor: encodeUIAfter(next)})
}

func (h *uiOperatorHandlers) audit(w http.ResponseWriter, r *http.Request) {
	teamID, ok := uiTeamID(w, r)
	if !ok {
		return
	}
	limit, after, ok := uiCollectionRequest(w, r)
	if !ok {
		return
	}
	items, next, err := h.repo.ListUIAudit(r.Context(), teamID, platform.UIAuditFilter{Search: strings.TrimSpace(r.URL.Query().Get("search"))}, limit, after)
	if err != nil {
		h.internal(w, r, "list audit", err)
		return
	}
	JSON(w, http.StatusOK, platform.UIAuditPage{Items: items, NextCursor: encodeUIAfter(next)})
}

func uiCollectionRequest(w http.ResponseWriter, r *http.Request) (int, *store.UIAfter, bool) {
	limit, ok := uiLimit(w, r)
	if !ok {
		return 0, nil, false
	}
	after, ok := decodeUIAfter(r.URL.Query().Get("cursor"))
	if !ok {
		uiError(w, http.StatusBadRequest, "invalid_cursor", "cursor is invalid")
		return 0, nil, false
	}
	return limit, after, true
}

func encodeUIAfter(after *store.UIAfter) *string {
	if after == nil {
		return nil
	}
	raw, _ := json.Marshal(struct {
		At time.Time `json:"at"`
		ID string    `json:"id"`
	}{after.At.UTC(), after.ID})
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	return &encoded
}

func decodeUIAfter(raw string) (*store.UIAfter, bool) {
	if raw == "" {
		return nil, true
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, false
	}
	var cursor struct {
		At time.Time `json:"at"`
		ID string    `json:"id"`
	}
	if json.Unmarshal(decoded, &cursor) != nil || cursor.At.IsZero() || cursor.ID == "" || !validListingIdentifier(cursor.ID) {
		return nil, false
	}
	return &store.UIAfter{At: cursor.At.UTC(), ID: cursor.ID}, true
}

func uiTaskPath(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	teamID, ok := uiTeamID(w, r)
	if !ok {
		return "", "", false
	}
	taskID := strings.TrimSpace(chi.URLParam(r, "task_id"))
	if taskID == "" || !validListingIdentifier(taskID) {
		uiError(w, http.StatusNotFound, "not_found", "resource not found")
		return "", "", false
	}
	return teamID, taskID, true
}

func uiExecutorPath(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	teamID, ok := uiTeamID(w, r)
	if !ok {
		return "", "", false
	}
	executorID := strings.TrimSpace(chi.URLParam(r, "executor_id"))
	if executorID == "" || !validListingIdentifier(executorID) {
		uiError(w, http.StatusNotFound, "not_found", "resource not found")
		return "", "", false
	}
	return teamID, executorID, true
}

func validUIStatus(status string) bool {
	switch status {
	case "", "pending", "created", "running", "finished", "failed":
		return true
	default:
		return false
	}
}

func validOptionalPeriod(period string) bool {
	return period == "" || period == "day" || period == "week" || period == "month"
}

func (h *uiOperatorHandlers) internal(w http.ResponseWriter, r *http.Request, action string, err error) {
	h.logger.Error(action+" failed", "request_id", requestID(r), "error", err)
	uiError(w, http.StatusInternalServerError, "internal_error", "request could not be completed")
}
