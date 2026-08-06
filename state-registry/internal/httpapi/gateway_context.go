package httpapi

import (
	"context"
	"net/http"
	"strings"
)

// trustedGatewayContext is the Gateway-forwarded operator and team
// data. After v0009 the State Registry does not authenticate the
// Gateway connection; the forwarded headers are trusted request
// data and the identifier shape is the only validation. Non-Gateway
// callers bypassing this boundary are an external network-policy
// concern.
type trustedGatewayContext struct {
	TeamID     string
	OperatorID string
	RequestID  string
	TeamName   string
}

type trustedGatewayContextKey struct{}

func requireTrustedGateway(next http.Handler) http.Handler {
	return trustedGatewayMiddleware(false, false, next)
}

func requireTrustedGatewayRequestID(next http.Handler) http.Handler {
	return trustedGatewayMiddleware(true, true, next)
}

func requireTrustedGatewayUpgrade(next http.Handler) http.Handler {
	return trustedGatewayMiddleware(false, true, next)
}

func trustedGatewayMiddleware(requireRequestID, _ bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := strings.TrimSpace(r.Header.Get(adminRoleHeader))
		if role != "" && role != "gateway" {
			JSON(w, http.StatusForbidden, errorResponse{
				Code:      "not_authorized",
				Message:   "Gateway request context is required",
				RequestID: requestID(r),
			})
			return
		}
		gateway := trustedGatewayContext{
			TeamID:     strings.TrimSpace(r.Header.Get(gatewayTeamIDHeader)),
			OperatorID: strings.TrimSpace(r.Header.Get(gatewayOperatorIDHeader)),
			RequestID:  strings.TrimSpace(r.Header.Get(flowAIRequestID)),
			TeamName:   strings.TrimSpace(r.Header.Get("X-FlowAI-Team-Name")),
		}
		// v0009: the Gateway-forwarded team_id and operator_id are
		// the canonical tenant filter; the State Registry
		// validates the identifier shape but does not authenticate
		// the Gateway. Missing or malformed identifiers are a
		// data-validation error (400), not an auth error.
		if gateway.TeamID == "" || !validListingIdentifier(gateway.TeamID) ||
			gateway.OperatorID == "" || !validListingIdentifier(gateway.OperatorID) {
			JSON(w, http.StatusBadRequest, errorResponse{
				Code:      "invalid_request",
				Message:   "Gateway-forwarded team_id and operator_id are required and must be valid identifiers",
				RequestID: requestID(r),
			})
			return
		}
		if requireRequestID && (gateway.RequestID == "" || !validListingIdentifier(gateway.RequestID)) {
			JSON(w, http.StatusBadRequest, errorResponse{
				Code:      "invalid_request",
				Message:   "request_id is required for this Gateway surface",
				RequestID: requestID(r),
			})
			return
		}
		ctx := context.WithValue(r.Context(), trustedGatewayContextKey{}, gateway)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func gatewayContext(r *http.Request) trustedGatewayContext {
	if gateway, ok := r.Context().Value(trustedGatewayContextKey{}).(trustedGatewayContext); ok {
		return gateway
	}
	return trustedGatewayContext{
		TeamID:     strings.TrimSpace(r.Header.Get(gatewayTeamIDHeader)),
		OperatorID: strings.TrimSpace(r.Header.Get(gatewayOperatorIDHeader)),
		RequestID:  strings.TrimSpace(r.Header.Get(flowAIRequestID)),
		TeamName:   strings.TrimSpace(r.Header.Get("X-FlowAI-Team-Name")),
	}
}
