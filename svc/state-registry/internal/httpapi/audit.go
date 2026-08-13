package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/svc/state-registry/internal/cursor"
	"github.com/flowai/platform/svc/state-registry/internal/platform"
	"github.com/flowai/platform/svc/state-registry/internal/store"
)

type auditHandlers struct {
	logger  *slog.Logger
	repo    store.AuditRepository
	keyring cursorKeyring
}

func RegisterAudit(r chi.Router, logger *slog.Logger, repo store.AuditRepository, keyring cursorKeyring) {
	h := &auditHandlers{logger: logger, repo: repo, keyring: keyring}
	r.With(h.requireTrustedGateway).Get("/v1/audit", h.list)
	r.With(h.requireTrustedGateway).Get("/v1/audit/{audit_id}", h.get)
}

func (h *auditHandlers) requireTrustedGateway(next http.Handler) http.Handler {
	return requireTrustedGatewayRequestID(next)
}

func (h *auditHandlers) list(w http.ResponseWriter, r *http.Request) {
	values, err := collectionQuery(r, "resource_type", "resource_id", "cursor", "limit")
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "audit query is invalid")
		return
	}
	resourceType := strings.TrimSpace(values.Get("resource_type"))
	resourceID := strings.TrimSpace(values.Get("resource_id"))
	if (resourceType != "" && (len(resourceType) > 64 || !validListingIdentifier(resourceType))) ||
		(resourceID != "" && !validListingIdentifier(resourceID)) {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "audit query is invalid")
		return
	}
	limit, err := cursor.ParseLimit(values)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_pagination", "pagination is invalid")
		return
	}
	gateway := gatewayContext(r)
	teamID := gateway.TeamID
	operatorID := gateway.OperatorID
	identity := cursor.Identity{Kind: "gateway", TeamID: teamID, OperatorID: operatorID}
	filters := map[string]string{"team_id": teamID, "resource_type": resourceType, "resource_id": resourceID}
	var after *store.AuditAfter
	token, present, err := cursorParam(values)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_pagination", "pagination is invalid")
		return
	}
	if present && token != "" {
		decoded, err := cursor.Decode(adminKeyringAdapter(h.keyring), cursor.DecodeRequest{
			Token: token, Endpoint: cursor.EndpointGatewayAudit, Identity: identity,
			Ordering: cursor.OrderingAuditDesc, Filters: filters,
		})
		if err != nil {
			h.writeError(w, r, http.StatusBadRequest, "invalid_pagination", "pagination is invalid")
			return
		}
		if pos := cursor.AuditPosition(decoded); pos != nil {
			after = &store.AuditAfter{OccurredAtUnixNano: pos.OccurredAtNano, AuditID: pos.AuditID}
		}
	}
	entries, next, err := h.repo.ListAuditEntries(r.Context(), teamID, resourceType, resourceID, limit.Value, after)
	if err != nil {
		h.logger.Error("list audit entries failed", "request_id", requestID(r), "error", err)
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "request could not be completed")
		return
	}
	page := platform.AdminPageInfo{Count: len(entries)}
	if next != nil {
		token, err := cursor.Encode(adminKeyringAdapter(h.keyring), cursor.EncodeIssue{
			Endpoint: cursor.EndpointGatewayAudit, Identity: identity,
			Ordering: cursor.OrderingAuditDesc, Filters: filters,
			AuditPosition: cursor.AuditPositionTuple(next.OccurredAtUnixNano, next.AuditID),
		})
		if err != nil {
			h.logger.Error("encode audit cursor failed", "request_id", requestID(r), "error", err)
			h.writeError(w, r, http.StatusInternalServerError, "internal_error", "request could not be completed")
			return
		}
		page.NextCursor = &token
	}
	JSON(w, http.StatusOK, platform.AuditPage{Items: entries, Page: page})
}

func (h *auditHandlers) get(w http.ResponseWriter, r *http.Request) {
	auditID := strings.TrimSpace(chi.URLParam(r, "audit_id"))
	if !validListingIdentifier(auditID) {
		h.writeError(w, r, http.StatusNotFound, "resource_not_found", "resource is unknown or unavailable")
		return
	}
	entry, err := h.repo.GetAuditEntry(r.Context(), gatewayContext(r).TeamID, auditID)
	if err != nil {
		if errors.Is(err, store.ErrTaskUnknown) {
			h.writeError(w, r, http.StatusNotFound, "resource_not_found", "resource is unknown or unavailable")
			return
		}
		h.logger.Error("get audit entry failed", "request_id", requestID(r), "error", err)
		h.writeError(w, r, http.StatusInternalServerError, "internal_error", "request could not be completed")
		return
	}
	JSON(w, http.StatusOK, entry)
}

func (h *auditHandlers) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	JSON(w, status, errorResponse{Code: code, Message: message, RequestID: requestID(r)})
}
