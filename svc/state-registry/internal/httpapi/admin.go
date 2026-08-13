package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
	"github.com/flowai/platform/svc/state-registry/internal/store"
)

const (
	adminRoleHeader      = "X-FlowAI-Role"
	adminSubjectHeader   = "X-FlowAI-Admin-Subject"
	flowAIRequestID      = "X-FlowAI-Request-Id"
	maxAdminRequestBytes = 64 << 10
)

var (
	identifierPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
	imageRepoPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*(:[A-Za-z0-9._-]+)?$`)
	imageDigestPattern = regexp.MustCompile(`^sha256:[A-Fa-f0-9]{64}$`)
)

type adminHandlers struct {
	logger *slog.Logger
	repo   store.AdminRepository
}

type errorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

// RegisterAdmin mounts the admin onboarding adapter. After v0009 the
// State Registry does not authenticate the system-administrator
// identity. The X-FlowAI-Role and X-FlowAI-Admin-Subject headers are
// trusted request context, not authentication proof; the deployment
// network policy owns the /admin/* caller boundary. Requiring their
// documented shape still prevents listener, Executor, and Gateway
// request envelopes from reaching administrator repositories.
func RegisterAdmin(r chi.Router, logger *slog.Logger, repo store.AdminRepository) {
	h := &adminHandlers{logger: logger, repo: repo}
	r.Route("/admin", func(admin chi.Router) {
		admin.Use(h.requireAdminHeaders)
		admin.Post("/teams", h.createTeam)
		admin.Post("/source-systems", h.createSourceSystem)
		admin.Post("/task-types", h.createTaskType)
	})
}

func (h *adminHandlers) requireAdminHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := strings.TrimSpace(r.Header.Get(adminRoleHeader))
		if role == "" {
			h.writeError(w, r, http.StatusUnauthorized, "unauthenticated", "system administrator context is required")
			return
		}
		if role != "admin" {
			h.writeError(w, r, http.StatusForbidden, "not_authorized", "system administrator context is required")
			return
		}
		if strings.TrimSpace(r.Header.Get(adminSubjectHeader)) == "" {
			h.writeError(w, r, http.StatusUnauthorized, "unauthenticated", "system administrator subject is required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *adminHandlers) createTeam(w http.ResponseWriter, r *http.Request) {
	var req platform.CreateTeamRequest
	if err := decodeAdminJSON(w, r, &req); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	if !validText(req.TeamName, 200) || !validImage(req.DefaultImage) {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "team_name and default_image are required and must be valid")
		return
	}

	team, err := h.repo.CreateTeam(r.Context(), req, adminIdentity(r))
	if err != nil {
		if errors.Is(err, store.ErrTeamNameConflict) {
			h.writeError(w, r, http.StatusBadRequest, "team_name_already_exists", "team_name is already registered")
			return
		}
		h.writeRepositoryError(w, r, "team", err)
		return
	}
	JSON(w, http.StatusCreated, team)
}

func (h *adminHandlers) createSourceSystem(w http.ResponseWriter, r *http.Request) {
	var req platform.CreateSourceSystemRequest
	if err := decodeAdminJSON(w, r, &req); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	if !validIdentifier(req.TeamID) || !validText(req.ListenerIdentity, 512) || !validOptionalImage(req.DefaultImage) {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "team_id, listener_identity, or default_image is invalid")
		return
	}

	source, err := h.repo.CreateSourceSystem(r.Context(), req, adminIdentity(r))
	if err != nil {
		switch {
		case errors.Is(err, store.ErrListenerIdentityConflict):
			h.writeError(w, r, http.StatusConflict, "listener_identity_already_bound", "listener_identity is already registered")
		case errors.Is(err, store.ErrTeamNotFound):
			h.writeError(w, r, http.StatusBadRequest, "team_unknown", "team_id does not reference an existing team")
		default:
			h.writeRepositoryError(w, r, "source_system", err)
		}
		return
	}
	JSON(w, http.StatusCreated, source)
}

func (h *adminHandlers) createTaskType(w http.ResponseWriter, r *http.Request) {
	var req platform.CreateTaskTypeRequest
	if err := decodeAdminJSON(w, r, &req); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	if !validIdentifier(req.TeamID) || !validTag(req.ExecutionTag) || !validOptionalImage(req.DefaultImage) {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "team_id, execution_tag, or default_image is invalid")
		return
	}

	taskType, err := h.repo.CreateTaskType(r.Context(), req, adminIdentity(r))
	if err != nil {
		if errors.Is(err, store.ErrTeamNotFound) {
			h.writeError(w, r, http.StatusBadRequest, "team_unknown", "team_id does not reference an existing team")
			return
		}
		h.writeRepositoryError(w, r, "task_type", err)
		return
	}
	JSON(w, http.StatusCreated, taskType)
}

func decodeAdminJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errors.New("content type must be application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON value")
		}
		return err
	}
	return nil
}

func adminIdentity(r *http.Request) platform.AdminIdentity {
	return platform.AdminIdentity{
		Subject:   r.Header.Get(adminSubjectHeader),
		RequestID: requestID(r),
	}
}

func requestID(r *http.Request) string {
	if id := r.Header.Get(flowAIRequestID); id != "" {
		return id
	}
	if id := middleware.GetReqID(r.Context()); id != "" {
		return id
	}
	return "request-unknown"
}

func (h *adminHandlers) writeRepositoryError(w http.ResponseWriter, r *http.Request, resource string, err error) {
	h.logger.Error("admin repository operation failed",
		"request_id", requestID(r),
		"resource", resource,
		"error", err,
	)
	h.writeError(w, r, http.StatusInternalServerError, "internal_error", "request could not be completed")
}

func (h *adminHandlers) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	JSON(w, status, errorResponse{Code: code, Message: message, RequestID: requestID(r)})
}

func validText(value string, max int) bool {
	length := utf8.RuneCountInString(value)
	return strings.TrimSpace(value) != "" && length <= max
}

func validIdentifier(value string) bool {
	return utf8.RuneCountInString(value) <= 128 && identifierPattern.MatchString(value)
}

func validTag(value string) bool {
	return utf8.RuneCountInString(value) <= 256 && identifierPattern.MatchString(value)
}

func validImage(image platform.ImageReference) bool {
	return utf8.RuneCountInString(image.Repository) <= 512 &&
		imageRepoPattern.MatchString(image.Repository) && imageDigestPattern.MatchString(image.Digest)
}

func validOptionalImage(image *platform.ImageReference) bool {
	return image == nil || validImage(*image)
}
