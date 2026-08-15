package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
	"github.com/flowai/platform/svc/state-registry/internal/store"
)

var uiEnvironmentKeyPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

type uiLaunchParameterHandlers struct {
	logger *slog.Logger
	repo   store.LaunchParameterRepository
}

func RegisterUILaunchParameters(r chi.Router, logger *slog.Logger, repo store.LaunchParameterRepository) {
	h := &uiLaunchParameterHandlers{logger: logger, repo: repo}
	const base = "/ui/v1/teams/{team_id}/launch-parameters"
	r.Get(base, h.list)
	r.Post(base, h.create)
	r.Get(base+"/{environment_id}", h.get)
	r.Put(base+"/{environment_id}", h.replace)
	r.Delete(base+"/{environment_id}", h.delete)
	r.Get(base+"/{environment_id}/revisions", h.revisions)
}

func (h *uiLaunchParameterHandlers) create(w http.ResponseWriter, r *http.Request) {
	teamID, ok := uiTeamID(w, r)
	if !ok {
		return
	}
	req, ok := decodeUILaunchParameterWrite(w, r)
	if !ok {
		return
	}
	item, err := h.repo.CreateLaunchParameters(r.Context(), uiIdentity(r, teamID), req)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	JSON(w, http.StatusCreated, item)
}

func (h *uiLaunchParameterHandlers) list(w http.ResponseWriter, r *http.Request) {
	teamID, ok := uiTeamID(w, r)
	if !ok {
		return
	}
	if _, present := r.URL.Query()["cursor"]; present {
		uiError(w, http.StatusBadRequest, "invalid_cursor", "cursor is invalid")
		return
	}
	limit, ok := uiLimit(w, r)
	if !ok {
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if scope != "" && scope != platform.LaunchParameterScopeGlobal && scope != platform.LaunchParameterScopeTeam && scope != platform.LaunchParameterScopeTaskType {
		uiError(w, http.StatusBadRequest, "invalid_request", "scope is invalid")
		return
	}
	taskTypeID, valid := optionalIdentifier(r.URL.Query().Get("task_type_id"))
	if !valid {
		uiError(w, http.StatusBadRequest, "invalid_request", "task_type_id is invalid")
		return
	}
	items, err := h.repo.ListLaunchParameters(r.Context(), teamID, scope, taskTypeID, limit)
	if err != nil {
		h.internalError(w, r, "list launch parameters", err)
		return
	}
	JSON(w, http.StatusOK, platform.LaunchParameterPage{Items: items, NextCursor: nil})
}

func (h *uiLaunchParameterHandlers) get(w http.ResponseWriter, r *http.Request) {
	teamID, environmentID, ok := uiEnvironmentPath(w, r)
	if !ok {
		return
	}
	item, err := h.repo.GetLaunchParameters(r.Context(), teamID, environmentID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	JSON(w, http.StatusOK, item)
}

func (h *uiLaunchParameterHandlers) replace(w http.ResponseWriter, r *http.Request) {
	teamID, environmentID, ok := uiEnvironmentPath(w, r)
	if !ok {
		return
	}
	req, ok := decodeUILaunchParameterWrite(w, r)
	if !ok {
		return
	}
	item, err := h.repo.ReplaceLaunchParameters(r.Context(), uiIdentity(r, teamID), environmentID, req)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	JSON(w, http.StatusOK, item)
}

func (h *uiLaunchParameterHandlers) delete(w http.ResponseWriter, r *http.Request) {
	teamID, environmentID, ok := uiEnvironmentPath(w, r)
	if !ok {
		return
	}
	if err := h.repo.DeleteLaunchParameters(r.Context(), uiIdentity(r, teamID), environmentID); err != nil {
		h.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *uiLaunchParameterHandlers) revisions(w http.ResponseWriter, r *http.Request) {
	teamID, environmentID, ok := uiEnvironmentPath(w, r)
	if !ok {
		return
	}
	if _, present := r.URL.Query()["cursor"]; present {
		uiError(w, http.StatusBadRequest, "invalid_cursor", "cursor is invalid")
		return
	}
	limit, ok := uiLimit(w, r)
	if !ok {
		return
	}
	items, err := h.repo.ListLaunchParameterRevisions(r.Context(), teamID, environmentID, limit)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	JSON(w, http.StatusOK, platform.LaunchParameterRevisionPage{Items: items, NextCursor: nil})
}

func decodeUILaunchParameterWrite(w http.ResponseWriter, r *http.Request) (platform.LaunchParameterWrite, bool) {
	var req platform.LaunchParameterWrite
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAdminRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" || len(req.Name) > 200 || req.Env == nil {
		uiError(w, http.StatusBadRequest, "invalid_request", "launch parameters are invalid")
		return platform.LaunchParameterWrite{}, false
	}
	if req.Scope != platform.LaunchParameterScopeTeam && req.Scope != platform.LaunchParameterScopeTaskType {
		uiError(w, http.StatusBadRequest, "invalid_request", "scope must be team or task_type")
		return platform.LaunchParameterWrite{}, false
	}
	if (req.Scope == platform.LaunchParameterScopeTeam && req.TaskTypeID != nil) ||
		(req.Scope == platform.LaunchParameterScopeTaskType && (req.TaskTypeID == nil || !validListingIdentifier(*req.TaskTypeID))) {
		uiError(w, http.StatusBadRequest, "invalid_request", "task_type_id does not match scope")
		return platform.LaunchParameterWrite{}, false
	}
	for key, value := range req.Env {
		if !uiEnvironmentKeyPattern.MatchString(key) || len(value) > 65536 {
			uiError(w, http.StatusBadRequest, "invalid_request", "environment key or value is invalid")
			return platform.LaunchParameterWrite{}, false
		}
	}
	if req.Image != nil && (len(*req.Image) > 1000 || !strings.Contains(*req.Image, "@sha256:")) {
		uiError(w, http.StatusBadRequest, "invalid_request", "image must contain an immutable sha256 digest")
		return platform.LaunchParameterWrite{}, false
	}
	return req, true
}

func uiTeamID(w http.ResponseWriter, r *http.Request) (string, bool) {
	teamID := strings.TrimSpace(chi.URLParam(r, "team_id"))
	if teamID == "" || !validListingIdentifier(teamID) {
		uiError(w, http.StatusNotFound, "not_found", "resource not found")
		return "", false
	}
	return teamID, true
}

func uiEnvironmentPath(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	teamID, ok := uiTeamID(w, r)
	if !ok {
		return "", "", false
	}
	environmentID := strings.TrimSpace(chi.URLParam(r, "environment_id"))
	if environmentID == "" || !validListingIdentifier(environmentID) {
		uiError(w, http.StatusNotFound, "not_found", "resource not found")
		return "", "", false
	}
	return teamID, environmentID, true
}

func uiIdentity(r *http.Request, teamID string) platform.GatewayIdentity {
	return platform.GatewayIdentity{TeamID: teamID, OperatorID: "web-ui", RequestID: requestID(r)}
}

func uiLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 10, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || (limit != 10 && limit != 25 && limit != 50 && limit != 100) {
		uiError(w, http.StatusBadRequest, "invalid_limit", "limit must be 10, 25, 50, or 100")
		return 0, false
	}
	return limit, true
}

func (h *uiLaunchParameterHandlers) writeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrEnvironmentUnavailable) {
		uiError(w, http.StatusNotFound, "environment_unknown_or_unavailable", "resource not found")
		return
	}
	if errors.Is(err, store.ErrLaunchParameterConflict) {
		uiError(w, http.StatusConflict, "scope_conflict", "active launch parameters already exist for scope")
		return
	}
	h.internalError(w, r, "launch parameter operation", err)
}

func (h *uiLaunchParameterHandlers) internalError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	h.logger.Error(operation+" failed", "request_id", requestID(r), "error", err)
	uiError(w, http.StatusInternalServerError, "internal_error", "request could not be completed")
}

func uiError(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, map[string]any{"code": code, "message": message})
}
