package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/svc/state-registry/internal/cursor"
	"github.com/flowai/platform/svc/state-registry/internal/platform"
	"github.com/flowai/platform/svc/state-registry/internal/store"
)

type secretHandlers struct {
	logger  *slog.Logger
	repo    store.SecretRepository
	keyring cursorKeyring
}

func RegisterSecrets(r chi.Router, logger *slog.Logger, repo store.SecretRepository, keyring cursorKeyring) {
	h := &secretHandlers{logger: logger, repo: repo, keyring: keyring}
	r.With(requireTrustedGatewayRequestID).Post("/v1/environments/{environment_id}/secrets", h.create)
	r.With(requireTrustedGatewayRequestID).Get("/v1/environments/{environment_id}/secrets", h.list)
	r.With(requireTrustedGatewayRequestID).Get("/v1/environments/{environment_id}/secrets/{secret_id}", h.get)
	r.With(requireTrustedGatewayRequestID).Post("/v1/environments/{environment_id}/secrets/{secret_id}/versions", h.replace)
	r.With(requireTrustedGatewayRequestID).Put("/v1/environments/{environment_id}/secrets/{secret_id}", h.replace)
	r.With(requireTrustedGatewayRequestID).Get("/v1/environments/{environment_id}/secrets/{secret_id}/versions", h.listVersions)
	r.With(requireTrustedGatewayRequestID).Get("/v1/environments/{environment_id}/secrets/{secret_id}/versions/{version}", h.getVersion)
	r.With(requireTrustedGatewayRequestID).Delete("/v1/environments/{environment_id}/secrets/{secret_id}", h.revoke)
}

func (h *secretHandlers) create(w http.ResponseWriter, r *http.Request) {
	environmentID, ok := environmentPathID(r)
	if !ok {
		h.notFound(w, r)
		return
	}
	var req platform.SecretCreateRequest
	if !decodeSecretJSON(w, r, &req) || strings.TrimSpace(req.Name) == "" || len(req.Name) > 200 || req.Value == "" || len(req.Value) > 1<<20 || !validEnvironmentScope(req.Scope) {
		h.invalid(w, r)
		return
	}
	result, err := h.repo.CreateSecret(r.Context(), gatewayPlatformIdentity(r), environmentID, req)
	if err != nil {
		h.handleError(w, r, err)
		return
	}
	JSON(w, http.StatusCreated, result)
}

func (h *secretHandlers) replace(w http.ResponseWriter, r *http.Request) {
	environmentID, secretID, ok := secretPathIDs(r)
	if !ok {
		h.notFound(w, r)
		return
	}
	var req platform.SecretReplaceRequest
	if !decodeSecretJSON(w, r, &req) || req.Value == "" || len(req.Value) > 1<<20 {
		h.invalid(w, r)
		return
	}
	result, err := h.repo.ReplaceSecret(r.Context(), gatewayPlatformIdentity(r), environmentID, secretID, req)
	if err != nil {
		h.handleError(w, r, err)
		return
	}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusCreated
	}
	JSON(w, status, result)
}

func (h *secretHandlers) get(w http.ResponseWriter, r *http.Request) {
	environmentID, secretID, ok := secretPathIDs(r)
	if !ok {
		h.notFound(w, r)
		return
	}
	secret, err := h.repo.GetSecret(r.Context(), gatewayContext(r).TeamID, environmentID, secretID)
	if err != nil {
		h.handleError(w, r, err)
		return
	}
	JSON(w, http.StatusOK, secret)
}

func (h *secretHandlers) list(w http.ResponseWriter, r *http.Request) {
	environmentID, ok := environmentPathID(r)
	if !ok {
		h.notFound(w, r)
		return
	}
	values, err := collectionQuery(r, "cursor", "limit")
	if err != nil {
		withdrawInvalidPagination(w, r)
		return
	}
	limit, err := cursor.ParseLimit(values)
	if err != nil {
		withdrawInvalidPagination(w, r)
		return
	}
	teamID := gatewayContext(r).TeamID
	operatorID := gatewayContext(r).OperatorID
	identity := cursor.Identity{Kind: "gateway", TeamID: teamID, OperatorID: operatorID}
	cursorFilters := map[string]string{
		"team_id":        teamID,
		"environment_id": environmentID,
	}
	token, present, err := cursorParam(values)
	if err != nil {
		withdrawInvalidPagination(w, r)
		return
	}
	var after *store.SecretAfter
	if present && token != "" {
		decoded, err := cursor.Decode(adminKeyringAdapter(h.keyring), cursor.DecodeRequest{
			Token:    token,
			Endpoint: cursor.EndpointGatewaySecrets,
			Identity: identity,
			Ordering: cursor.OrderingSecretsAsc,
			Filters:  cursorFilters,
		})
		if err != nil {
			withdrawInvalidPagination(w, r)
			return
		}
		if pos := cursor.SecretPosition(decoded); pos != nil {
			after = &store.SecretAfter{
				CreatedAtUnixNano: pos.CreatedAtNano,
				SecretID:          pos.SecretID,
			}
		}
	}
	items, next, err := h.repo.ListSecretsPaged(r.Context(), teamID, environmentID, limit.Value, after)
	if err != nil {
		if errors.Is(err, store.ErrEnvironmentUnavailable) {
			h.notFound(w, r)
			return
		}
		h.logger.Error("list secrets failed", "request_id", requestID(r), "error", err)
		withdrawPaginationInternalError(w, r)
		return
	}
	page := platform.AdminPageInfo{Count: len(items)}
	if next != nil {
		tok, err := cursor.Encode(adminKeyringAdapter(h.keyring), cursor.EncodeIssue{
			Endpoint:       cursor.EndpointGatewaySecrets,
			Identity:       identity,
			Ordering:       cursor.OrderingSecretsAsc,
			Filters:        cursorFilters,
			SecretPosition: cursor.SecretPositionTuple(next.CreatedAtUnixNano, next.SecretID),
		})
		if err != nil {
			h.logger.Error("list secrets cursor encode failed", "request_id", requestID(r), "error", err)
			withdrawPaginationInternalError(w, r)
			return
		}
		page.NextCursor = &tok
	}
	JSON(w, http.StatusOK, platform.LogicalSecretPage{Items: items, Page: page})
}

func (h *secretHandlers) listVersions(w http.ResponseWriter, r *http.Request) {
	environmentID, secretID, ok := secretPathIDs(r)
	if !ok {
		h.notFound(w, r)
		return
	}
	values, err := collectionQuery(r, "cursor", "limit")
	if err != nil {
		withdrawInvalidPagination(w, r)
		return
	}
	limit, err := cursor.ParseLimit(values)
	if err != nil {
		withdrawInvalidPagination(w, r)
		return
	}
	teamID := gatewayContext(r).TeamID
	operatorID := gatewayContext(r).OperatorID
	identity := cursor.Identity{Kind: "gateway", TeamID: teamID, OperatorID: operatorID}
	cursorFilters := map[string]string{
		"team_id":        teamID,
		"environment_id": environmentID,
		"secret_id":      secretID,
	}
	token, present, err := cursorParam(values)
	if err != nil {
		withdrawInvalidPagination(w, r)
		return
	}
	var after *store.SecretVersionAfter
	if present && token != "" {
		decoded, err := cursor.Decode(adminKeyringAdapter(h.keyring), cursor.DecodeRequest{
			Token:    token,
			Endpoint: cursor.EndpointGatewaySecretVersions,
			Identity: identity,
			Ordering: cursor.OrderingSecretVersionsAsc,
			Filters:  cursorFilters,
		})
		if err != nil {
			withdrawInvalidPagination(w, r)
			return
		}
		if pos := cursor.SecretVersionPosition(decoded); pos != nil {
			after = &store.SecretVersionAfter{Version: pos.Version, SecretID: pos.SecretID}
		}
	}
	items, next, err := h.repo.ListSecretVersionsPaged(r.Context(), teamID, environmentID, secretID, limit.Value, after)
	if err != nil {
		if errors.Is(err, store.ErrEnvironmentUnavailable) {
			h.notFound(w, r)
			return
		}
		h.logger.Error("list secret versions failed", "request_id", requestID(r), "error", err)
		withdrawPaginationInternalError(w, r)
		return
	}
	page := platform.AdminPageInfo{Count: len(items)}
	if next != nil {
		tok, err := cursor.Encode(adminKeyringAdapter(h.keyring), cursor.EncodeIssue{
			Endpoint:              cursor.EndpointGatewaySecretVersions,
			Identity:              identity,
			Ordering:              cursor.OrderingSecretVersionsAsc,
			Filters:               cursorFilters,
			SecretVersionPosition: cursor.SecretVersionPositionTuple(next.Version, next.SecretID),
		})
		if err != nil {
			h.logger.Error("list secret versions cursor encode failed", "request_id", requestID(r), "error", err)
			withdrawPaginationInternalError(w, r)
			return
		}
		page.NextCursor = &tok
	}
	JSON(w, http.StatusOK, platform.SecretVersionPage{Items: items, Page: page})
}

func (h *secretHandlers) getVersion(w http.ResponseWriter, r *http.Request) {
	environmentID, secretID, ok := secretPathIDs(r)
	versionNumber, err := strconv.Atoi(chi.URLParam(r, "version"))
	if !ok || err != nil || versionNumber < 1 {
		h.notFound(w, r)
		return
	}
	version, err := h.repo.GetSecretVersion(r.Context(), gatewayContext(r).TeamID, environmentID, secretID, versionNumber)
	if err != nil {
		h.handleError(w, r, err)
		return
	}
	JSON(w, http.StatusOK, version)
}

func decodeSecretJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(target) == nil
}

func validEnvironmentScope(scope platform.EnvironmentScope) bool {
	for _, value := range []*string{scope.ProjectID, scope.TaskID, scope.ParentTaskID} {
		if value != nil && (*value == "" || !validListingIdentifier(*value)) {
			return false
		}
	}
	return true
}

func secretPathIDs(r *http.Request) (string, string, bool) {
	environmentID, ok := environmentPathID(r)
	secretID := strings.TrimSpace(chi.URLParam(r, "secret_id"))
	return environmentID, secretID, ok && secretID != "" && validListingIdentifier(secretID)
}

func (h *secretHandlers) revoke(w http.ResponseWriter, r *http.Request) {
	environmentID, secretID, ok := secretPathIDs(r)
	if !ok {
		h.notFound(w, r)
		return
	}
	if _, err := h.repo.RevokeSecret(r.Context(), gatewayPlatformIdentity(r), environmentID, secretID); err != nil {
		h.handleError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *secretHandlers) handleError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrEnvironmentUnavailable) {
		h.notFound(w, r)
		return
	}
	h.logger.Error("secret operation failed", "request_id", requestID(r), "error", err)
	JSON(w, http.StatusInternalServerError, errorResponse{Code: "internal_error", Message: "request could not be completed", RequestID: requestID(r)})
}

func (h *secretHandlers) notFound(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusNotFound, errorResponse{Code: "resource_unknown", Message: "resource is unknown or unavailable", RequestID: requestID(r)})
}

func (h *secretHandlers) invalid(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusBadRequest, errorResponse{Code: "invalid_request", Message: "secret request is invalid", RequestID: requestID(r)})
}
