package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/state-registry/internal/cursor"
	"github.com/flowai/platform/state-registry/internal/platform"
	"github.com/flowai/platform/state-registry/internal/store"
)

var environmentKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type environmentHandlers struct {
	logger  *slog.Logger
	repo    store.EnvironmentRepository
	keyring cursorKeyring
}

func RegisterEnvironments(r chi.Router, logger *slog.Logger, repo store.EnvironmentRepository, keyring cursorKeyring) {
	h := &environmentHandlers{logger: logger, repo: repo, keyring: keyring}
	r.With(requireTrustedGatewayRequestID).Post("/v1/environments", h.create)
	r.With(requireTrustedGatewayRequestID).Get("/v1/environments", h.list)
	r.With(requireTrustedGatewayRequestID).Get("/v1/environments/{environment_id}", h.get)
	r.With(requireTrustedGatewayRequestID).Put("/v1/environments/{environment_id}", h.replace)
	r.With(requireTrustedGatewayRequestID).Delete("/v1/environments/{environment_id}", h.delete)
}

func (h *environmentHandlers) create(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeEnvironmentWrite(w, r)
	if !ok {
		return
	}
	environment, err := h.repo.CreateEnvironment(r.Context(), gatewayPlatformIdentity(r), req)
	if err != nil {
		h.writeEnvironmentError(w, r, err, true)
		return
	}
	JSON(w, http.StatusCreated, environment)
}

func (h *environmentHandlers) list(w http.ResponseWriter, r *http.Request) {
	values, err := collectionQuery(r, "project_id", "task_id", "cursor", "limit")
	if err != nil {
		withdrawInvalidPagination(w, r)
		return
	}
	limit, err := cursor.ParseLimit(values)
	if err != nil {
		withdrawInvalidPagination(w, r)
		return
	}
	projectID, ok := optionalIdentifier(values.Get("project_id"))
	if !ok {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "environment query is invalid")
		return
	}
	taskID, ok := optionalIdentifier(values.Get("task_id"))
	if !ok {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "environment query is invalid")
		return
	}
	teamID := gatewayContext(r).TeamID
	operatorID := gatewayContext(r).OperatorID
	identity := cursor.Identity{Kind: "gateway", TeamID: teamID, OperatorID: operatorID}
	filterProject, filterTask := "", ""
	if projectID != nil {
		filterProject = *projectID
	}
	if taskID != nil {
		filterTask = *taskID
	}
	cursorFilters := map[string]string{
		"team_id":    teamID,
		"project_id": filterProject,
		"task_id":    filterTask,
	}
	token, present, err := cursorParam(values)
	if err != nil {
		withdrawInvalidPagination(w, r)
		return
	}
	var after *store.EnvironmentAfter
	if present && token != "" {
		decoded, err := cursor.Decode(adminKeyringAdapter(h.keyring), cursor.DecodeRequest{
			Token:    token,
			Endpoint: cursor.EndpointGatewayEnvironments,
			Identity: identity,
			Ordering: cursor.OrderingEnvironmentsAsc,
			Filters:  cursorFilters,
		})
		if err != nil {
			withdrawInvalidPagination(w, r)
			return
		}
		if pos := cursor.EnvironmentPosition(decoded); pos != nil {
			after = &store.EnvironmentAfter{
				CreatedAtUnixNano: pos.CreatedAtNano,
				EnvironmentID:     pos.EnvironmentID,
			}
		}
	}
	items, next, err := h.repo.ListEnvironmentsPaged(r.Context(), teamID, projectID, taskID, limit.Value, after)
	if err != nil {
		h.logger.Error("list environments failed", "request_id", requestID(r), "error", err)
		withdrawPaginationInternalError(w, r)
		return
	}
	page := platform.AdminPageInfo{Count: len(items)}
	if next != nil {
		tok, err := cursor.Encode(adminKeyringAdapter(h.keyring), cursor.EncodeIssue{
			Endpoint:            cursor.EndpointGatewayEnvironments,
			Identity:            identity,
			Ordering:            cursor.OrderingEnvironmentsAsc,
			Filters:             cursorFilters,
			EnvironmentPosition: cursor.EnvironmentPositionTuple(next.CreatedAtUnixNano, next.EnvironmentID),
		})
		if err != nil {
			h.logger.Error("list environments cursor encode failed", "request_id", requestID(r), "error", err)
			withdrawPaginationInternalError(w, r)
			return
		}
		page.NextCursor = &tok
	}
	JSON(w, http.StatusOK, platform.EnvironmentPage{Items: items, Page: page})
}

func (h *environmentHandlers) get(w http.ResponseWriter, r *http.Request) {
	environmentID, ok := environmentPathID(r)
	if !ok {
		h.writeError(w, r, http.StatusNotFound, "resource_unknown", "resource is unknown or unavailable")
		return
	}
	environment, err := h.repo.GetEnvironment(r.Context(), gatewayContext(r).TeamID, environmentID)
	if err != nil {
		h.writeEnvironmentError(w, r, err, false)
		return
	}
	JSON(w, http.StatusOK, environment)
}

func (h *environmentHandlers) replace(w http.ResponseWriter, r *http.Request) {
	environmentID, ok := environmentPathID(r)
	if !ok {
		h.writeError(w, r, http.StatusNotFound, "resource_unknown", "resource is unknown or unavailable")
		return
	}
	req, ok := decodeEnvironmentWrite(w, r)
	if !ok {
		return
	}
	environment, err := h.repo.ReplaceEnvironment(r.Context(), gatewayPlatformIdentity(r), environmentID, req)
	if err != nil {
		h.writeEnvironmentError(w, r, err, false)
		return
	}
	JSON(w, http.StatusOK, environment)
}

func (h *environmentHandlers) delete(w http.ResponseWriter, r *http.Request) {
	environmentID, ok := environmentPathID(r)
	if !ok {
		h.writeError(w, r, http.StatusNotFound, "resource_unknown", "resource is unknown or unavailable")
		return
	}
	if err := h.repo.DeleteEnvironment(r.Context(), gatewayPlatformIdentity(r), environmentID); err != nil {
		h.writeEnvironmentError(w, r, err, false)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeEnvironmentWrite(w http.ResponseWriter, r *http.Request) (platform.EnvironmentWriteRequest, bool) {
	var req platform.EnvironmentWriteRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAdminRequestBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" || len(req.Name) > 200 || req.Values == nil {
		JSON(w, http.StatusBadRequest, errorResponse{Code: "invalid_request", Message: "environment request is invalid", RequestID: requestID(r)})
		return platform.EnvironmentWriteRequest{}, false
	}
	for key, value := range req.Values {
		if !environmentKeyPattern.MatchString(key) || len(value) > 65536 {
			JSON(w, http.StatusBadRequest, errorResponse{Code: "invalid_request", Message: "environment request is invalid", RequestID: requestID(r)})
			return platform.EnvironmentWriteRequest{}, false
		}
	}
	for _, value := range []*string{req.Scope.ProjectID, req.Scope.TaskID, req.Scope.ParentTaskID} {
		if value != nil && (*value == "" || !validListingIdentifier(*value)) {
			JSON(w, http.StatusBadRequest, errorResponse{Code: "invalid_request", Message: "environment request is invalid", RequestID: requestID(r)})
			return platform.EnvironmentWriteRequest{}, false
		}
	}
	return req, true
}

func gatewayPlatformIdentity(r *http.Request) platform.GatewayIdentity {
	gateway := gatewayContext(r)
	return platform.GatewayIdentity{TeamID: gateway.TeamID, OperatorID: gateway.OperatorID, RequestID: gateway.RequestID}
}

func environmentPathID(r *http.Request) (string, bool) {
	id := strings.TrimSpace(chi.URLParam(r, "environment_id"))
	return id, id != "" && validListingIdentifier(id)
}

func optionalIdentifier(raw string) (*string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, true
	}
	return &value, validListingIdentifier(value)
}

func (h *environmentHandlers) writeEnvironmentError(w http.ResponseWriter, r *http.Request, err error, parentUnavailable bool) {
	if errors.Is(err, store.ErrEnvironmentUnavailable) {
		if parentUnavailable {
			h.writeError(w, r, http.StatusNotFound, "environment_unknown_or_unavailable", "environment is unknown or unavailable")
		} else {
			h.writeError(w, r, http.StatusNotFound, "resource_unknown", "resource is unknown or unavailable")
		}
		return
	}
	h.logger.Error("environment operation failed", "request_id", requestID(r), "error", err)
	h.writeError(w, r, http.StatusInternalServerError, "internal_error", "request could not be completed")
}

func (h *environmentHandlers) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	JSON(w, status, errorResponse{Code: code, Message: message, RequestID: requestID(r)})
}
