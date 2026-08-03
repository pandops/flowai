package httpapi

import (
	"context"
	"net/http"
	"strings"
)

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

func trustedGatewayMiddleware(requireRequestID, missingRoleUnauthorized bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := r.Header.Get(adminRoleHeader)
		if role == "" {
			if missingRoleUnauthorized {
				writeTrustedGatewayError(w, r, http.StatusUnauthorized, "unauthenticated")
			} else {
				writeTrustedGatewayError(w, r, http.StatusForbidden, "not_authorized")
			}
			return
		}
		if role != "gateway" {
			writeTrustedGatewayError(w, r, http.StatusForbidden, "not_authorized")
			return
		}
		gateway := trustedGatewayContext{
			TeamID:     strings.TrimSpace(r.Header.Get(gatewayTeamIDHeader)),
			OperatorID: strings.TrimSpace(r.Header.Get(gatewayOperatorIDHeader)),
			RequestID:  strings.TrimSpace(r.Header.Get(flowAIRequestID)),
			TeamName:   strings.TrimSpace(r.Header.Get("X-FlowAI-Team-Name")),
		}
		if gateway.TeamID == "" || gateway.OperatorID == "" ||
			!validListingIdentifier(gateway.TeamID) || !validListingIdentifier(gateway.OperatorID) ||
			(requireRequestID && (gateway.RequestID == "" || !validListingIdentifier(gateway.RequestID))) {
			writeTrustedGatewayError(w, r, http.StatusUnauthorized, "unauthenticated")
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

func writeTrustedGatewayError(w http.ResponseWriter, r *http.Request, status int, code string) {
	JSON(w, status, errorResponse{Code: code, Message: "trusted Gateway identity is required", RequestID: requestID(r)})
}
