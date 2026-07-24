package httpapi

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/state-registry/internal/cursor"
	"github.com/flowai/platform/state-registry/internal/platform"
	"github.com/flowai/platform/state-registry/internal/store"
)

// adminListHandlers is the test-mode admin read-only collection
// handler. The handlers bind every pagination decision to the
// cursor package and call the list repository only after the cursor
// and limit have been validated. The (zero protected repository
// calls) assertion is enforced by the rejection tests.
type adminListHandlers struct {
	logger  *slog.Logger
	repo    store.ListRepository
	keyring cursorKeyring
}

// cursorKeyring is the narrow contract the handlers depend on so the
// cursor package is not re-exported. Production code passes the
// production keyring; tests pass a test keyring.
type cursorKeyring interface {
	ActiveKeyID() string
	Keys() map[string][]byte
}

const maxListRawQueryLength = 2048

// RegisterAdminList mounts GET /admin/tags and GET /admin/tasks on
// the supplied router. The handlers are scoped under the existing
// /admin/* route so the system-administrator middleware already
// gates non-admin identities before the request reaches the handler.
func RegisterAdminList(r chi.Router, logger *slog.Logger, repo store.ListRepository, keyring cursorKeyring) {
	h := &adminListHandlers{logger: logger, repo: repo, keyring: keyring}
	auth := &adminHandlers{logger: logger, repo: repo}
	r.With(auth.requireSystemAdministrator).Get("/admin/tags", h.listTags)
	r.With(auth.requireSystemAdministrator).Get("/admin/tasks", h.listTasks)
}

// adminCursorIdentity returns the system-administrator scope. The
// spec anchors authorization on the role, not on the subject, so
// every admin cursor binds the same canonical scope value.
func adminCursorIdentity() cursor.Identity {
	return cursor.Identity{Kind: "admin"}
}

// listTags returns the documented GET /admin/tags page.
func (h *adminListHandlers) listTags(w http.ResponseWriter, r *http.Request) {
	ident := adminCursorIdentity()
	values, err := collectionQuery(r, "team_id", "tag", "cursor", "limit")
	if err != nil {
		withdrawInvalidPagination(w, r)
		return
	}
	limit, err := cursor.ParseLimit(values)
	if err != nil {
		withdrawInvalidPagination(w, r)
		return
	}
	filter := platform.AdminTagFilter{
		TeamID: strings.TrimSpace(values.Get("team_id")),
		Tag:    strings.TrimSpace(values.Get("tag")),
	}
	if !validListingIdentifier(filter.TeamID) || !validListingTag(filter.Tag) {
		withdrawInvalidPagination(w, r)
		return
	}
	token, present, err := cursorParam(values)
	if err != nil {
		withdrawInvalidPagination(w, r)
		return
	}
	var after *store.TaskTypeAfter
	if present && token != "" {
		decoded, err := cursor.Decode(adminKeyringAdapter(h.keyring), cursor.DecodeRequest{
			Token:    token,
			Endpoint: cursor.EndpointAdminTags,
			Identity: ident,
			Ordering: cursor.OrderingAdminTags,
			Filters: map[string]string{
				"team_id": filter.TeamID,
				"tag":     filter.Tag,
			},
		})
		_ = decoded
		if err != nil {
			withdrawInvalidPagination(w, r)
			return
		}
		if pos := cursor.TagPosition(decoded); pos != nil {
			after = &store.TaskTypeAfter{
				TeamID:       pos.TeamID,
				ExecutionTag: pos.ExecutionTag,
				TaskTypeID:   pos.TaskTypeID,
			}
		}
	}
	entries, next, err := h.repo.ListTaskTypes(r.Context(), filter, limit.Value, after)
	if err != nil {
		h.logger.Error("list task types failed", "request_id", requestID(r), "error", err)
		withdrawPaginationInternalError(w, r)
		return
	}
	page := platform.AdminPageInfo{Count: len(entries)}
	if next != nil {
		tok, err := cursor.Encode(adminKeyringAdapter(h.keyring), cursor.EncodeIssue{
			Endpoint: cursor.EndpointAdminTags,
			Identity: ident,
			Ordering: cursor.OrderingAdminTags,
			Filters: map[string]string{
				"team_id": filter.TeamID,
				"tag":     filter.Tag,
			},
			TagPosition: cursor.TagPositionTuple(next.TeamID, next.ExecutionTag, next.TaskTypeID),
		})
		if err != nil {
			h.logger.Error("list task types cursor encode failed", "request_id", requestID(r), "error", err)
			withdrawPaginationInternalError(w, r)
			return
		}
		page.NextCursor = &tok
	}
	JSON(w, http.StatusOK, platform.AdminTagPage{Items: entries, Page: page})
}

// listTasks returns the documented GET /admin/tasks page.
func (h *adminListHandlers) listTasks(w http.ResponseWriter, r *http.Request) {
	ident := adminCursorIdentity()
	values, err := collectionQuery(r, "team_id", "state", "tag", "task_type_id", "cursor", "limit")
	if err != nil {
		withdrawInvalidPagination(w, r)
		return
	}
	limit, err := cursor.ParseLimit(values)
	if err != nil {
		withdrawInvalidPagination(w, r)
		return
	}
	filter := platform.AdminTaskFilter{
		TeamID:     strings.TrimSpace(values.Get("team_id")),
		State:      strings.TrimSpace(values.Get("state")),
		Tag:        strings.TrimSpace(values.Get("tag")),
		TaskTypeID: strings.TrimSpace(values.Get("task_type_id")),
	}
	if !validListingIdentifier(filter.TeamID) || !validListingTag(filter.Tag) ||
		!validListingIdentifier(filter.TaskTypeID) || !validTaskState(filter.State) {
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
			Endpoint: cursor.EndpointAdminTasks,
			Identity: ident,
			Ordering: cursor.OrderingTasksDesc,
			Filters: map[string]string{
				"team_id":      filter.TeamID,
				"state":        filter.State,
				"tag":          filter.Tag,
				"task_type_id": filter.TaskTypeID,
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
	entries, next, err := h.repo.ListTasks(r.Context(), filter, limit.Value, after)
	if err != nil {
		h.logger.Error("list tasks failed", "request_id", requestID(r), "error", err)
		withdrawPaginationInternalError(w, r)
		return
	}
	// Narrow the canonical task row onto the exact 11-field
	// AdminTaskEntry allowlist at the handler boundary so the
	// Gateway and admin responses can never drift past the
	// documented /admin/tasks projection.
	items := make([]platform.AdminTaskEntry, 0, len(entries))
	for i := range entries {
		items = append(items, entries[i].ToAdminTaskEntry())
	}
	page := platform.AdminPageInfo{Count: len(items)}
	if next != nil {
		tok, err := cursor.Encode(adminKeyringAdapter(h.keyring), cursor.EncodeIssue{
			Endpoint: cursor.EndpointAdminTasks,
			Identity: ident,
			Ordering: cursor.OrderingTasksDesc,
			Filters: map[string]string{
				"team_id":      filter.TeamID,
				"state":        filter.State,
				"tag":          filter.Tag,
				"task_type_id": filter.TaskTypeID,
			},
			TaskPosition: cursor.TaskPositionTuple(next.IngestedAtUnixNano, next.TaskID),
		})
		if err != nil {
			h.logger.Error("list tasks cursor encode failed", "request_id", requestID(r), "error", err)
			withdrawPaginationInternalError(w, r)
			return
		}
		page.NextCursor = &tok
	}
	JSON(w, http.StatusOK, platform.AdminTaskPage{Items: items, Page: page})
}

// withdrawInvalidPagination writes the documented non-revealing 400
// invalid_pagination response shape and never logs the cursor or
// limit value.
func withdrawInvalidPagination(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusBadRequest, errorResponse{
		Code:      "invalid_pagination",
		Message:   "the pagination request is invalid",
		RequestID: requestID(r),
	})
}

// withdrawPaginationInternalError writes the generic 500 envelope
// for repository or cursor-encode failures. The internal cause is
// logged but never returned to the caller.
func withdrawPaginationInternalError(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusInternalServerError, errorResponse{
		Code:      "internal_error",
		Message:   "request could not be completed",
		RequestID: requestID(r),
	})
}

// validListingIdentifier returns true when the supplied value is
// either empty or matches the documented identifier pattern.
func validListingIdentifier(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 128 {
		return false
	}
	return identifierPattern.MatchString(value)
}

func validListingTag(value string) bool {
	return value == "" || validTag(value)
}

// validTaskState returns true when the supplied value is either
// empty or matches one of the documented canonical task states.
func validTaskState(value string) bool {
	if value == "" {
		return true
	}
	switch value {
	case "pending", "created", "running", "finished", "failed":
		return true
	}
	return false
}

// cursorParam returns the cursor query parameter, a presence flag,
// and an error. Three-way result:
//
//   - (value, true, nil): the cursor query parameter was supplied
//     with a single non-empty value; caller MUST decode before use.
//   - ("", false, nil): the cursor query parameter was absent;
//     caller treats the request as a first-page request.
//   - ("", _, err): the cursor query parameter was malformed
//     (multiple values or empty string); caller MUST reject with
//     400 invalid_pagination.
func cursorParam(values url.Values) (string, bool, error) {
	raw, ok := values["cursor"]
	if !ok || len(raw) == 0 {
		return "", false, nil
	}
	if len(raw) > 1 {
		return "", true, cursor.ErrInvalid
	}
	if raw[0] == "" {
		return "", true, cursor.ErrInvalid
	}
	if len(raw[0]) > cursor.MaxTokenLength {
		return "", true, cursor.ErrInvalid
	}
	return raw[0], true, nil
}

// collectionQuery rejects oversized, unknown, or repeated scalar query
// parameters before cursor decoding and before any protected repository call.
func collectionQuery(r *http.Request, allowed ...string) (url.Values, error) {
	if len(r.URL.RawQuery) > maxListRawQueryLength {
		return nil, cursor.ErrInvalid
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}
	values := r.URL.Query()
	for key, entries := range values {
		if _, ok := allowedSet[key]; !ok || len(entries) != 1 {
			return nil, cursor.ErrInvalid
		}
	}
	return values, nil
}

// adminKeyringAdapter wraps the httpapi keyring into the cursor
// layer Keyring so the encoder and decoder can use it without
// importing the config package.
func adminKeyringAdapter(k cursorKeyring) *cursorKeyringBridge {
	return &cursorKeyringBridge{inner: k}
}

// cursorKeyringBridge wraps the httpapi cursorKeyring into the
// cursor.Keyring interface.
type cursorKeyringBridge struct {
	inner cursorKeyring
}

func (b *cursorKeyringBridge) ActiveKeyID() cursor.KeyID {
	return cursor.KeyID(b.inner.ActiveKeyID())
}

func (b *cursorKeyringBridge) Keys() map[cursor.KeyID][]byte {
	raw := b.inner.Keys()
	out := make(map[cursor.KeyID][]byte, len(raw))
	for id, key := range raw {
		out[cursor.KeyID(id)] = key
	}
	return out
}
