package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/state-registry/internal/store"
)

type executorRegistrationRequest struct {
	Scope           string         `json:"scope"`
	TeamID          *string        `json:"team_id"`
	ExecutorType    string         `json:"executor_type"`
	Identity        string         `json:"identity"`
	AuthorizedTag   string         `json:"authorized_tag"`
	MaxCapacity     int            `json:"max_capacity"`
	RunningCount    int            `json:"running_count"`
	RuntimeMetadata map[string]any `json:"runtime_metadata"`
}

// RegisterExecutorRegistrationGuard mounts the test-mode precondition needed
// before full mutually authenticated Executor registration lands:
// team-scoped registrations must reference an existing team and never create
// one implicitly. Production startup must not mount this header adapter.
func RegisterExecutorRegistrationGuard(r chi.Router, logger *slog.Logger, repo store.AdminRepository) {
	r.Put("/executors/{executor_id}", func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get(adminRoleHeader) != "team-executor" {
			JSON(w, http.StatusForbidden, errorResponse{
				Code: "not_authorized", Message: "team Executor authorization is required", RequestID: requestID(req),
			})
			return
		}

		var body executorRegistrationRequest
		if err := decodeAdminJSON(w, req, &body); err != nil || body.Scope != "team" || body.TeamID == nil || !validIdentifier(*body.TeamID) {
			JSON(w, http.StatusBadRequest, errorResponse{
				Code: "invalid_request", Message: "team-scoped registration requires a valid team_id", RequestID: requestID(req),
			})
			return
		}
		exists, err := repo.TeamExists(req.Context(), *body.TeamID)
		if err != nil {
			logger.Error("executor team lookup failed", "request_id", requestID(req))
			JSON(w, http.StatusInternalServerError, errorResponse{
				Code: "internal_error", Message: "request could not be completed", RequestID: requestID(req),
			})
			return
		}
		if !exists {
			JSON(w, http.StatusBadRequest, errorResponse{
				Code: "team_unknown", Message: "team_id does not reference an existing team", RequestID: requestID(req),
			})
			return
		}

		JSON(w, http.StatusNotImplemented, errorResponse{
			Code: "registration_not_implemented", Message: "Executor registration is not implemented", RequestID: requestID(req),
		})
	})
}
