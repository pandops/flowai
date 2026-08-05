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

// RegisterExecutorRegistrationGuard mounts the precondition guard
// for Executor registration when the full repository implementation
// is not available. After v0009 the X-FlowAI-Role header is request
// data; the team_id comes from the body and the canonical team
// record authorizes the binding. The role header is no longer used
// to reject non-team-executor callers.
func RegisterExecutorRegistrationGuard(r chi.Router, logger *slog.Logger, repo store.AdminRepository) {
	r.Put("/executors/{executor_id}", func(w http.ResponseWriter, req *http.Request) {
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
