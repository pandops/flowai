package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/state-registry/internal/platform"
	"github.com/flowai/platform/state-registry/internal/store"
)

const (
	executorIDHeader     = "X-FlowAI-Executor-Id"
	executorTeamIDHeader = "X-FlowAI-Team-Id"
	executorTeamRole     = "team-executor"
	executorSysRole      = "system-executor"
)

type executorHandlers struct {
	logger *slog.Logger
	admin  store.AdminRepository
	repo   store.ExecutorRepository
}

// RegisterExecutor mounts the Executor adapter. After v0009 the
// State Registry does not authenticate the Executor connection; the
// canonical Executor record (looked up by URL executor_id) is the
// source of truth for scope/team predicates.
func RegisterExecutor(r chi.Router, logger *slog.Logger, admin store.AdminRepository, repo store.ExecutorRepository) {
	h := &executorHandlers{logger: logger, admin: admin, repo: repo}
	r.Put("/executors/{executor_id}", h.register)
	r.Get("/executors/{executor_id}", h.get)
	r.Get("/executors/{executor_id}/tasks", h.discover)
	r.Post("/executors/{executor_id}/claim", h.claim)
}

func (h *executorHandlers) register(w http.ResponseWriter, r *http.Request) {
	executorID := chi.URLParam(r, "executor_id")
	identity, ok := h.resolveExecutorIdentity(w, r, executorID)
	if !ok {
		return
	}

	var raw map[string]json.RawMessage
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAdminRequestBytes))
	if err != nil || json.Unmarshal(body, &raw) != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	var tag string
	if encoded, exists := raw["authorized_tag"]; !exists || json.Unmarshal(encoded, &tag) != nil || !validTag(tag) {
		h.writeError(w, r, http.StatusBadRequest, "invalid_tag_count", "exactly one valid authorized_tag is required")
		return
	}

	r.Body = io.NopCloser(bytes.NewReader(body))
	var req platform.ExecutorRegistrationRequest
	if err := decodeAdminJSON(w, r, &req); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	if req.Scope != platform.ExecutorScopeTeam && req.Scope != platform.ExecutorScopeSystem {
		h.writeError(w, r, http.StatusBadRequest, "invalid_scope", "scope must be team or system")
		return
	}
	if _, exists := raw["team_id"]; !exists {
		h.writeError(w, r, http.StatusBadRequest, "missing_team_id", "team_id is required and may be null only for system scope")
		return
	}
	if !validIdentifier(executorID) || !validText(req.ExecutorType, 128) || !validText(req.Identity, 512) || req.MaxCapacity < 0 || req.RunningCount < 0 || !validJSONObject(req.RuntimeMetadata) {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "registration fields are invalid")
		return
	}
	if req.Identity != identity.ExecutorID {
		h.writeError(w, r, http.StatusForbidden, "not_authorized", "Executor identity does not match registration")
		return
	}
	if req.Scope == platform.ExecutorScopeTeam {
		if req.TeamID == nil || !validIdentifier(*req.TeamID) {
			h.writeError(w, r, http.StatusBadRequest, "missing_team_id", "team-scoped registration requires team_id")
			return
		}
		if identity.TeamID != nil && *identity.TeamID != *req.TeamID {
			h.writeError(w, r, http.StatusForbidden, "team_binding_mismatch", "team_id does not match the configured Executor binding")
			return
		}
		// For first registration, derive identity team_id from
		// the body when the header is missing.
		if identity.TeamID == nil {
			teamID := *req.TeamID
			identity.TeamID = &teamID
			identity.Scope = platform.ExecutorScopeTeam
		}
		exists, err := h.admin.TeamExists(r.Context(), *req.TeamID)
		if err != nil {
			h.repositoryError(w, r, err)
			return
		}
		if !exists {
			h.writeError(w, r, http.StatusBadRequest, "team_unknown", "team_id does not reference an existing team")
			return
		}
	} else if req.TeamID != nil {
		h.writeError(w, r, http.StatusBadRequest, "system_scope_team_id_must_be_null", "system-scoped registration requires team_id null")
		return
	}

	executor, err := h.repo.RegisterExecutor(r.Context(), executorID, req, identity)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrExecutorScopeConflict):
			h.writeError(w, r, http.StatusBadRequest, "scope_change_forbidden", "Executor scope is immutable")
		case errors.Is(err, store.ErrExecutorTeamConflict):
			h.writeError(w, r, http.StatusBadRequest, "team_binding_mismatch", "Executor team_id is immutable")
		case errors.Is(err, store.ErrExecutorIdentityMismatch):
			h.writeError(w, r, http.StatusForbidden, "not_authorized", "Executor identity does not match registration")
		case errors.Is(err, store.ErrTeamNotFound):
			h.writeError(w, r, http.StatusBadRequest, "team_unknown", "team_id does not reference an existing team")
		default:
			h.repositoryError(w, r, err)
		}
		return
	}
	JSON(w, http.StatusOK, executor)
}

func (h *executorHandlers) get(w http.ResponseWriter, r *http.Request) {
	executorID := chi.URLParam(r, "executor_id")
	if strings.TrimSpace(r.Header.Get(gatewayTeamIDHeader)) != "" {
		h.getForGateway(w, r, executorID)
		return
	}
	identity, ok := h.resolveExecutorIdentity(w, r, executorID)
	if !ok {
		return
	}
	executor, err := h.repo.GetExecutor(r.Context(), executorID)
	if err != nil {
		if errors.Is(err, store.ErrExecutorNotFound) {
			h.writeError(w, r, http.StatusNotFound, "executor_unknown", "Executor is unknown")
			return
		}
		h.repositoryError(w, r, err)
		return
	}
	if identity.Scope != executor.Scope || !sameTeamIdentity(identity.TeamID, executor.TeamID) {
		h.writeError(w, r, http.StatusNotFound, "executor_unknown", "Executor is unknown")
		return
	}
	JSON(w, http.StatusOK, executor)
}

func (h *executorHandlers) getForGateway(w http.ResponseWriter, r *http.Request, executorID string) {
	teamID := strings.TrimSpace(r.Header.Get(gatewayTeamIDHeader))
	operatorID := strings.TrimSpace(r.Header.Get(gatewayOperatorIDHeader))
	if teamID == "" || operatorID == "" {
		h.writeError(w, r, http.StatusUnauthorized, "unauthenticated", "trusted Gateway context is required")
		return
	}
	executor, err := h.repo.GetExecutor(r.Context(), executorID)
	if err != nil {
		if !errors.Is(err, store.ErrExecutorNotFound) {
			h.repositoryError(w, r, err)
			return
		}
		h.writeError(w, r, http.StatusNotFound, "executor_unknown", "Executor is unknown")
		return
	}
	if executor.Scope != platform.ExecutorScopeTeam || executor.TeamID == nil || *executor.TeamID != teamID {
		h.writeError(w, r, http.StatusNotFound, "executor_unknown", "Executor is unknown")
		return
	}
	JSON(w, http.StatusOK, executor)
}

func (h *executorHandlers) discover(w http.ResponseWriter, r *http.Request) {
	executorID := chi.URLParam(r, "executor_id")
	identity, ok := h.resolveExecutorIdentity(w, r, executorID)
	if !ok {
		return
	}
	tag := r.URL.Query().Get("tag")
	if !validTag(tag) {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "tag is required")
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 200 {
			h.writeError(w, r, http.StatusBadRequest, "invalid_request", "limit is invalid")
			return
		}
		limit = parsed
	}
	executor, err := h.repo.GetExecutor(r.Context(), executorID)
	if err != nil || identity.Scope != executor.Scope || !sameTeamIdentity(identity.TeamID, executor.TeamID) {
		if err != nil && !errors.Is(err, store.ErrExecutorNotFound) {
			h.repositoryError(w, r, err)
			return
		}
		h.writeError(w, r, http.StatusNotFound, "executor_unknown", "Executor is unknown")
		return
	}
	items, err := h.repo.DiscoverExecutorTasks(r.Context(), executorID, tag, limit)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrExecutorNotFound):
			h.writeError(w, r, http.StatusNotFound, "executor_unknown", "Executor is unknown")
		case errors.Is(err, store.ErrExecutorTagMismatch):
			h.writeError(w, r, http.StatusBadRequest, "invalid_request", "tag does not match the registered tag")
		default:
			h.repositoryError(w, r, err)
		}
		return
	}
	if len(items) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	JSON(w, http.StatusOK, platform.TaskDiscoveryPage{Items: items})
}

// resolveExecutorIdentity builds the request-time Executor identity
// from the URL path and the caller-supplied X-FlowAI-Executor-Id /
// X-FlowAI-Team-Id / X-FlowAI-Role headers. After v0009 the
// X-FlowAI-Role header is request data, not authentication; the
// canonical Executor record reconciles any disagreement.
func (h *executorHandlers) resolveExecutorIdentity(w http.ResponseWriter, r *http.Request, pathExecutorID string) (platform.ExecutorIdentity, bool) {
	headerExecutorID := r.Header.Get(executorIDHeader)
	if headerExecutorID == "" {
		headerExecutorID = pathExecutorID
	}
	if headerExecutorID != pathExecutorID {
		h.writeError(w, r, http.StatusNotFound, "executor_unknown", "Executor is unknown")
		return platform.ExecutorIdentity{}, false
	}
	role := r.Header.Get(adminRoleHeader)
	identity := platform.ExecutorIdentity{ExecutorID: headerExecutorID, Scope: platform.ExecutorScopeSystem}
	teamID := strings.TrimSpace(r.Header.Get(executorTeamIDHeader))
	switch {
	case role == executorTeamRole && teamID != "":
		identity.Scope = platform.ExecutorScopeTeam
		identity.TeamID = &teamID
	case role == executorTeamRole:
		return identity, true
	case role == executorSysRole:
		identity.Scope = platform.ExecutorScopeSystem
	case teamID != "":
		identity.Scope = platform.ExecutorScopeTeam
		identity.TeamID = &teamID
	}
	return identity, true
}

func (h *executorHandlers) claim(w http.ResponseWriter, r *http.Request) {
	executorID := chi.URLParam(r, "executor_id")
	identity, ok := h.resolveExecutorIdentity(w, r, executorID)
	if !ok {
		return
	}
	var raw map[string]json.RawMessage
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAdminRequestBytes))
	if err != nil || json.Unmarshal(body, &raw) != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var req platform.ClaimRequest
	if err := decodeAdminJSON(w, r, &req); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	if !validIdentifier(req.TaskID) || !validIdentifier(req.CommandID) {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "task_id and command_id are required")
		return
	}
	if _, exists := raw["task_id"]; !exists {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "task_id is required")
		return
	}
	if _, exists := raw["command_id"]; !exists {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "command_id is required")
		return
	}
	resp, err := h.repo.ClaimTask(r.Context(), req, executorID, identity)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrExecutorNotFound):
			h.writeError(w, r, http.StatusNotFound, "executor_unknown", "Executor is unknown")
		case errors.Is(err, store.ErrTaskNotFound), errors.Is(err, store.ErrTaskUnknown):
			h.writeError(w, r, http.StatusNotFound, "task_not_found", "task is unknown or unavailable")
		case errors.Is(err, store.ErrTaskAlreadyClaimed):
			h.writeError(w, r, http.StatusConflict, "task_already_claimed", "the task is already claimed under a different command_id")
		case errors.Is(err, store.ErrOlderTaskMustBeClaimedFirst):
			h.writeError(w, r, http.StatusConflict, "older_task_must_be_claimed_first", "an older eligible pending task must be claimed first")
		default:
			h.repositoryError(w, r, err)
		}
		return
	}
	JSON(w, http.StatusOK, resp)
}

func (h *executorHandlers) repositoryError(w http.ResponseWriter, r *http.Request, err error) {
	h.logger.Error("executor repository operation failed", "request_id", requestID(r), "error", err)
	h.writeError(w, r, http.StatusInternalServerError, "internal_error", "request could not be completed")
}

func (h *executorHandlers) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	JSON(w, status, errorResponse{Code: code, Message: message, RequestID: requestID(r)})
}

func validJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var value map[string]any
	return json.Unmarshal(raw, &value) == nil && value != nil
}

func sameTeamIdentity(left, right *string) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
}
