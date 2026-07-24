package httpapi

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/state-registry/internal/cursor"
	"github.com/flowai/platform/state-registry/internal/platform"
	"github.com/flowai/platform/state-registry/internal/store"
)

// gatewayIdentityHeaders are the request headers the trusted Gateway
// supplies on every operator request. The handler reads them, never
// modifies them, and passes them to the cursor envelope so the
// scoping is bound at issue time.
const (
	gatewayTeamIDHeader     = "X-FlowAI-Team-Id"
	gatewayOperatorIDHeader = "X-FlowAI-Operator-Id"
)

// gatewayListHandlers is the test-mode trusted-Gateway read-only
// collection handler. The handler enforces the documented identity
// check (gateway role + verified team_id + verified operator_id) at
// the middleware layer, then validates the cursor and limit BEFORE
// any repo call.
type gatewayListHandlers struct {
	logger  *slog.Logger
	repo    store.ListRepository
	keyring cursorKeyring
}

// RegisterGatewayList mounts GET /v1/tasks under the trusted-Gateway
// middleware. The middleware enforces the identity check at the
// chi-router boundary so the handler never runs without a verified
// team_id + operator_id.
func RegisterGatewayList(r chi.Router, logger *slog.Logger, repo store.ListRepository, keyring cursorKeyring) {
	h := &gatewayListHandlers{logger: logger, repo: repo, keyring: keyring}
	r.With(h.requireTrustedGateway).Get("/v1/tasks", h.listTasks)
}

// requireTrustedGateway enforces the trusted-Gateway identity.
func (h *gatewayListHandlers) requireTrustedGateway(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(adminRoleHeader) != "gateway" {
			h.writeGatewayError(w, r, http.StatusForbidden, "not_authorized", "trusted Gateway identity is required")
			return
		}
		teamID := strings.TrimSpace(r.Header.Get(gatewayTeamIDHeader))
		operatorID := strings.TrimSpace(r.Header.Get(gatewayOperatorIDHeader))
		if teamID == "" || operatorID == "" {
			h.writeGatewayError(w, r, http.StatusUnauthorized, "unauthenticated", "trusted Gateway identity is required")
			return
		}
		if !validListingIdentifier(teamID) || !validListingIdentifier(operatorID) {
			h.writeGatewayError(w, r, http.StatusUnauthorized, "unauthenticated", "trusted Gateway identity is required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// listTasks returns the documented GET /v1/tasks page under the
// verified trusted-Gateway team_id predicate. The handler honors
// only the documented query surface (state, tag, cursor, limit);
// task_type_id is intentionally not accepted as a filter because
// the OpenAPI TaskPage contract does not expose it.
func (h *gatewayListHandlers) listTasks(w http.ResponseWriter, r *http.Request) {
	teamID := strings.TrimSpace(r.Header.Get(gatewayTeamIDHeader))
	operatorID := strings.TrimSpace(r.Header.Get(gatewayOperatorIDHeader))
	ident := cursor.Identity{
		Kind:       "gateway",
		TeamID:     teamID,
		OperatorID: operatorID,
	}
	values, err := collectionQuery(r, "state", "tag", "cursor", "limit")
	if err != nil {
		withdrawInvalidPagination(w, r)
		return
	}
	limit, err := cursor.ParseLimit(values)
	if err != nil {
		withdrawInvalidPagination(w, r)
		return
	}
	filter := platform.GatewayTaskFilter{
		State: strings.TrimSpace(values.Get("state")),
		Tag:   strings.TrimSpace(values.Get("tag")),
	}
	if !validListingTag(filter.Tag) || !validTaskState(filter.State) {
		withdrawInvalidPagination(w, r)
		return
	}
	token, present, err := cursorParam(values)
	if err != nil {
		withdrawInvalidPagination(w, r)
		return
	}
	var after *store.TaskAfter
	if present && token != "" {
		decoded, err := cursor.Decode(adminKeyringAdapter(h.keyring), cursor.DecodeRequest{
			Token:    token,
			Endpoint: cursor.EndpointGatewayTasks,
			Identity: ident,
			Ordering: cursor.OrderingTasksDesc,
			Filters: map[string]string{
				"team_id": teamID,
				"state":   filter.State,
				"tag":     filter.Tag,
			},
		})
		if err != nil {
			withdrawInvalidPagination(w, r)
			return
		}
		if pos := cursor.TaskPosition(decoded); pos != nil {
			after = &store.TaskAfter{
				IngestedAtUnixNano: pos.IngestedAtNano,
				TaskID:             pos.TaskID,
			}
		}
	}
	// The trusted Gateway identity supplies the team predicate; the
	// repository applies it before ordering, pagination, and counts.
	// `task_type_id` is intentionally absent from the gateway filter
	// map and the cursor filter set.
	taskFilter := platform.AdminTaskFilter{
		TeamID: teamID,
		State:  filter.State,
		Tag:    filter.Tag,
	}
	entries, next, err := h.repo.ListTasks(r.Context(), taskFilter, limit.Value, after)
	if err != nil {
		h.logger.Error("gateway list tasks failed", "request_id", requestID(r), "error", err)
		withdrawPaginationInternalError(w, r)
		return
	}
	page := platform.AdminPageInfo{Count: len(entries)}
	if next != nil {
		tok, err := cursor.Encode(adminKeyringAdapter(h.keyring), cursor.EncodeIssue{
			Endpoint: cursor.EndpointGatewayTasks,
			Identity: ident,
			Ordering: cursor.OrderingTasksDesc,
			Filters: map[string]string{
				"team_id": teamID,
				"state":   filter.State,
				"tag":     filter.Tag,
			},
			TaskPosition: cursor.TaskPositionTuple(next.IngestedAtUnixNano, next.TaskID),
		})
		if err != nil {
			h.logger.Error("gateway list tasks cursor encode failed", "request_id", requestID(r), "error", err)
			withdrawPaginationInternalError(w, r)
			return
		}
		page.NextCursor = &tok
	}
	JSON(w, http.StatusOK, platform.GatewayTaskPage{Items: entries, Page: page})
}

// writeGatewayError writes the trusted-Gateway identity error shape.
func (h *gatewayListHandlers) writeGatewayError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	JSON(w, status, errorResponse{Code: code, Message: message, RequestID: requestID(r)})
}
