package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/textproto"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/state-registry/internal/platform"
	"github.com/flowai/platform/state-registry/internal/store"
)

// The listener adapter reads the documented test-mode request headers.
// Production startup MUST NOT mount the listener route; the headers
// are deliberately untrusted in production and only meaningful inside
// explicit test mode.
const (
	listenerHdrTeamID       = "X-FlowAI-Team-Id"
	listenerHdrIdentity     = "X-FlowAI-Listener-Identity"
	listenerHdrSourceSystem = "X-FlowAI-Source-System-Id"
	listenerHdrRequiredTag  = "X-FlowAI-Required-Tag"
	listenerRole            = "listener"
	maxListenerRequestBytes = 64 << 10
	maxListenerSourceID     = 256
)

// listenerHandlers owns the listener ingestion handler. The handler
// rejects any presence of a listener-supplied required_tag (header or
// JSON key) BEFORE the typed decode, validates the request, and only
// then calls the repository. The adapter therefore never trusts
// listener-supplied authoritative identifiers beyond the immutable
// authenticated binding.
type listenerHandlers struct {
	logger *slog.Logger
	repo   store.ListenerRepository
}

// RegisterListener mounts POST /v1/tasks on the supplied router in
// test mode only. The route is intentionally NOT mounted in production
// because the headers used here are not authentication credentials —
// production routes require mutually authenticated service identity.
func RegisterListener(r chi.Router, logger *slog.Logger, repo store.ListenerRepository) {
	if logger == nil || repo == nil {
		// Defense in depth: if either dependency is missing, the
		// listener adapter cannot operate safely. We deliberately
		// refuse to mount rather than panic so existing tests that
		// pass nil ops keep working.
		return
	}
	h := &listenerHandlers{logger: logger, repo: repo}
	r.With(h.requireListenerIdentity).Post("/v1/tasks", h.ingestTask)
}

// requireListenerIdentity enforces the test-mode listener identity
// envelope. The boundary rejects missing identity components with 401
// and a non-empty wrong role with 403. No body parsing happens in this
// middleware so a malformed request that carries the correct headers
// still reaches the handler.
func (h *listenerHandlers) requireListenerIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := r.Header.Get(adminRoleHeader)
		if role == "" {
			h.writeError(w, r, http.StatusUnauthorized, "unauthenticated", "listener authentication is required")
			return
		}
		if role != listenerRole {
			h.writeError(w, r, http.StatusForbidden, "not_authorized", "listener authorization is required")
			return
		}
		teamID := r.Header.Get(listenerHdrTeamID)
		sourceSystemID := r.Header.Get(listenerHdrSourceSystem)
		listenerIdentity := r.Header.Get(listenerHdrIdentity)
		if teamID == "" || sourceSystemID == "" || listenerIdentity == "" {
			h.writeError(w, r, http.StatusUnauthorized, "unauthenticated", "listener identity is required")
			return
		}
		if !validIdentifier(teamID) || !validIdentifier(sourceSystemID) || !validText(listenerIdentity, 512) {
			h.writeError(w, r, http.StatusUnauthorized, "unauthenticated", "listener identity is invalid")
			return
		}
		// Reject the presence (including empty value) of the
		// X-FlowAI-Required-Tag header before any body parsing so
		// the listener cannot smuggle a required_tag through the
		// header surface. Go's net/http canonicalizes header keys
		// (X-FlowAI-Required-Tag → X-Flowai-Required-Tag); the
		// canonical form is the only key stored on the map.
		if _, present := r.Header[textproto.CanonicalMIMEHeaderKey(listenerHdrRequiredTag)]; present {
			h.writeError(w, r, http.StatusBadRequest, "listener_supplied_required_tag_forbidden", "listener_supplied_required_tag_forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ingestTask implements POST /v1/tasks. The handler follows the
// ordering mandated by OpenSpec:
//
//  1. Validate Content-Type, bounded body, and single JSON value.
//  2. Detect any `required_tag` key recursively in the body —
//     listener_supplied_required_tag_forbidden if found.
//  3. Strictly decode the body into TaskIngestionRequest with unknown
//     fields rejected.
//  4. Validate identifiers and the source_id length bound.
//  5. Compare body team_id and source_system_id to the authenticated
//     identity before any repository call.
//  6. Call IngestTask; map source-binding / task-type failures to
//     non-revealing 403 envelopes.
//
// 201 is returned on a fresh insert, 200 on an idempotent retry. The
// handler never inserts or reads task_events.
func (h *listenerHandlers) ingestTask(w http.ResponseWriter, r *http.Request) {
	ident := listenerIdentity(r)
	raw, ok := decodeListenerBody(w, r, h)
	if !ok {
		return
	}
	if containsRequiredTagKey(raw) {
		h.writeError(w, r, http.StatusBadRequest, "listener_supplied_required_tag_forbidden", "listener_supplied_required_tag_forbidden")
		return
	}

	var req platform.TaskIngestionRequest
	if err := decodeListenerStrict(raw, &req); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	if !validIdentifier(req.TeamID) || !validIdentifier(req.SourceSystemID) ||
		!validIdentifier(req.TaskTypeID) {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "team_id, source_system_id, or task_type_id is invalid")
		return
	}
	if !validOptionalIdentifier(req.ProjectID) || !validOptionalIdentifier(req.EnvironmentID) {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "project_id or environment_id is invalid")
		return
	}
	if !validText(req.SourceID, maxListenerSourceID) {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "source_id is required and must be at most 256 characters")
		return
	}
	if !validOptionalImage(req.Image) {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "image is invalid")
		return
	}
	// Payload must be a JSON object; reject scalars, arrays, and the
	// literal `null`.
	if !payloadIsObject(req.Payload) {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "payload is required and must be a JSON object")
		return
	}

	// Body team/source must match the authenticated listener binding
	// BEFORE any repository call. A mismatch is a non-revealing 403.
	if req.TeamID != ident.TeamID || req.SourceSystemID != ident.SourceSystemID {
		h.writeError(w, r, http.StatusForbidden, "not_authorized", "listener team or source-system binding mismatch")
		return
	}

	entry, created, err := h.repo.IngestTask(r.Context(), req, ident)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrListenerSourceMismatch),
			errors.Is(err, store.ErrListenerTaskTypeUnknown):
			h.writeError(w, r, http.StatusForbidden, "not_authorized", "listener binding or task type is invalid")
		default:
			h.logger.Error("listener ingest failed",
				"request_id", requestID(r),
				"error", err,
			)
			h.writeError(w, r, http.StatusInternalServerError, "internal_error", "request could not be completed")
		}
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	JSON(w, status, entry)
}

// listenerIdentity builds the immutable authenticated listener
// identity from the request headers. The fields here are the only
// inputs that authorize the ingestion call.
func listenerIdentity(r *http.Request) platform.ListenerIdentity {
	return platform.ListenerIdentity{
		TeamID:         r.Header.Get(listenerHdrTeamID),
		SourceSystemID: r.Header.Get(listenerHdrSourceSystem),
		Identity:       r.Header.Get(listenerHdrIdentity),
		RequestID:      requestID(r),
	}
}

// decodeListenerBody enforces Content-Type, body length, and the
// "exactly one JSON value" rule. It writes the error envelope and
// returns ok=false on any failure so the handler returns immediately.
func decodeListenerBody(w http.ResponseWriter, r *http.Request, h *listenerHandlers) ([]byte, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "content type must be application/json")
		return nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxListenerRequestBytes)
	decoder := json.NewDecoder(r.Body)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return nil, false
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			h.writeError(w, r, http.StatusBadRequest, "invalid_request", "request body must contain one JSON value")
			return nil, false
		}
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return nil, false
	}
	return raw, true
}

// containsRequiredTagKey recursively walks the JSON value looking for
// any object key named `required_tag`. The detection is performed on
// the raw body before the typed decode so the listener cannot smuggle
// the field through any nesting depth.
func containsRequiredTagKey(raw []byte) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		// A non-JSON body or a malformed value is the typed-decode's
		// problem; here we return false so the next stage emits the
		// canonical 400 invalid_request envelope.
		return false
	}
	return walkRequiredTag(v)
}

func walkRequiredTag(v any) bool {
	switch n := v.(type) {
	case map[string]any:
		for k, child := range n {
			if k == "required_tag" {
				return true
			}
			if walkRequiredTag(child) {
				return true
			}
		}
	case []any:
		for _, child := range n {
			if walkRequiredTag(child) {
				return true
			}
		}
	}
	return false
}

// decodeListenerStrict re-decodes the already-validated raw bytes into
// the typed request, rejecting unknown fields. Running the decoder
// against the validated bytes lets us catch an unrecognised top-level
// key that we already filtered on `required_tag`.
func decodeListenerStrict(raw []byte, dst *platform.TaskIngestionRequest) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

// writeError emits the documented {code,message,request_id} envelope.
func (h *listenerHandlers) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	JSON(w, status, errorResponse{Code: code, Message: message, RequestID: requestID(r)})
}

// validOptionalIdentifier returns true when the supplied value is
// either nil or matches the documented identifier pattern.
func validOptionalIdentifier(value *string) bool {
	if value == nil {
		return true
	}
	return validIdentifier(*value)
}

// payloadIsObject returns true when the supplied JSON payload is a
// non-null object. Scalars, arrays, and the literal `null` are rejected.
func payloadIsObject(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return false
	}
	if trimmed[0] != '{' {
		return false
	}
	return json.Valid(raw)
}
