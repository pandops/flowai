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

type uiSecretHandlers struct {
	logger *slog.Logger
	repo   store.SecretRepository
}

func RegisterUISecrets(r chi.Router, logger *slog.Logger, repo store.SecretRepository) {
	h := &uiSecretHandlers{logger: logger, repo: repo}
	const base = "/ui/v1/teams/{team_id}/launch-parameters/{environment_id}/secrets"
	r.Post(base, h.create)
	r.Put(base+"/{secret_id}", h.replace)
	r.Delete(base+"/{secret_id}", h.revoke)
	r.Get(base+"/{secret_id}/versions", h.versions)
}

func (h *uiSecretHandlers) create(w http.ResponseWriter, r *http.Request) {
	teamID, environmentID, ok := uiEnvironmentPath(w, r)
	if !ok {
		return
	}
	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if !decodeUISecretBody(w, r, &req) || !uiEnvironmentKeyPattern.MatchString(req.Key) || req.Value == "" {
		uiError(w, http.StatusBadRequest, "invalid_request", "secret key or value is invalid")
		return
	}
	result, err := h.repo.CreateSecret(r.Context(), uiIdentity(r, teamID), environmentID, platform.SecretCreateRequest{Name: req.Key, Value: req.Value})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	JSON(w, http.StatusCreated, uiSecret(result.Secret))
}

func (h *uiSecretHandlers) replace(w http.ResponseWriter, r *http.Request) {
	teamID, environmentID, secretID, ok := uiSecretPath(w, r)
	if !ok {
		return
	}
	var req struct {
		Value string `json:"value"`
	}
	if !decodeUISecretBody(w, r, &req) || req.Value == "" {
		uiError(w, http.StatusBadRequest, "invalid_request", "secret value is invalid")
		return
	}
	result, err := h.repo.ReplaceSecret(r.Context(), uiIdentity(r, teamID), environmentID, secretID, platform.SecretReplaceRequest{Value: req.Value})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	JSON(w, http.StatusOK, uiSecret(result.Secret))
}

func (h *uiSecretHandlers) revoke(w http.ResponseWriter, r *http.Request) {
	teamID, environmentID, secretID, ok := uiSecretPath(w, r)
	if !ok {
		return
	}
	if _, err := h.repo.RevokeSecret(r.Context(), uiIdentity(r, teamID), environmentID, secretID); err != nil {
		h.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *uiSecretHandlers) versions(w http.ResponseWriter, r *http.Request) {
	teamID, environmentID, secretID, ok := uiSecretPath(w, r)
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
	versions, err := h.repo.ListSecretVersions(r.Context(), teamID, environmentID, secretID, limit)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	items := make([]platform.UISecretVersion, 0, len(versions))
	for _, version := range versions {
		items = append(items, platform.UISecretVersion{Version: version.Version, CreatedAt: version.CreatedAt})
	}
	JSON(w, http.StatusOK, platform.UISecretVersionPage{Items: items, NextCursor: nil})
}

func decodeUISecretBody(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAdminRequestBytes))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target) == nil
}

func uiSecretPath(w http.ResponseWriter, r *http.Request) (string, string, string, bool) {
	teamID, environmentID, ok := uiEnvironmentPath(w, r)
	if !ok {
		return "", "", "", false
	}
	secretID := strings.TrimSpace(chi.URLParam(r, "secret_id"))
	if secretID == "" || !validListingIdentifier(secretID) {
		uiError(w, http.StatusNotFound, "not_found", "resource not found")
		return "", "", "", false
	}
	return teamID, environmentID, secretID, true
}

func uiSecret(secret platform.LogicalSecret) platform.UISecret {
	return platform.UISecret{SecretID: secret.SecretID, Key: secret.Name, CurrentVersion: secret.LatestVersion, Revoked: secret.Revoked}
}

func (h *uiSecretHandlers) writeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrEnvironmentUnavailable) {
		uiError(w, http.StatusNotFound, "environment_unknown_or_unavailable", "resource not found")
		return
	}
	h.logger.Error("UI secret operation failed", "request_id", requestID(r), "error", err)
	uiError(w, http.StatusInternalServerError, "internal_error", "request could not be completed")
}
