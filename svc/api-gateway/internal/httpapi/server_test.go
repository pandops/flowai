package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUnauthenticatedRoutesFailWithStableEnvelope(t *testing.T) {
	t.Parallel()
	tests := []struct {
		method string
		path   string
		code   string
	}{
		{http.MethodGet, "/auth/v1/teams", "invalid_session"},
		{http.MethodPost, "/auth/v1/token", "invalid_session"},
		{http.MethodDelete, "/auth/v1/session", "invalid_session"},
		{http.MethodPost, "/admin/v1/oidc-team-mappings", "invalid_admin_token"},
	}
	for _, tt := range tests {
		t.Run(tt.method+tt.path, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, tt.path, nil)
			response := httptest.NewRecorder()
			New().ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
			}
			var body errorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Code != tt.code || body.Message == "" || body.RequestID == "" {
				t.Fatalf("unexpected error response: %+v", body)
			}
		})
	}
}

func TestCallbackRequiresExactlyOneResult(t *testing.T) {
	t.Parallel()
	for _, query := range []string{
		"code=x&error=denied&state=s&error_description=do-not-reflect",
		"state=s",
	} {
		request := httptest.NewRequest(http.MethodGet, "/auth/v1/callback?"+query, nil)
		response := httptest.NewRecorder()
		New().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d", response.Code)
		}
		if response.Body.String() == "" || contains(response.Body.String(), "do-not-reflect") {
			t.Fatalf("unsafe response: %s", response.Body.String())
		}
	}
}

func contains(value, substring string) bool {
	for i := 0; i+len(substring) <= len(value); i++ {
		if value[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}
