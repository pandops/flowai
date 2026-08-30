// Package httpapi defines the API Gateway HTTP surface.
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/flowai/platform/svc/api-gateway/internal/adminauth"
	"github.com/flowai/platform/svc/api-gateway/internal/jwtverify"
	"github.com/flowai/platform/svc/api-gateway/internal/oidc"
	"github.com/flowai/platform/svc/api-gateway/internal/stateregistry"
	"github.com/flowai/platform/svc/api-gateway/internal/store"
	"github.com/flowai/platform/svc/api-gateway/internal/workingtoken"
	"github.com/gorilla/websocket"
)

type errorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func New() http.Handler {
	return NewWithDependencies(Dependencies{})
}

type AdminAuthenticator interface {
	Authenticate(context.Context, string) (string, error)
}
type LoginStarter interface {
	Start(context.Context) (string, error)
}
type OIDCCompleter interface {
	Complete(context.Context, string, string) (oidc.Identity, error)
}
type SessionCreator interface {
	Create(context.Context, store.SessionInput) error
	AccessibleTeams(context.Context, string, time.Time) ([]store.AccessibleTeam, error)
	Delete(context.Context, string) error
	TokenContext(context.Context, string, string, time.Time) (store.TokenContext, error)
	RecordToken(context.Context, store.TokenContext, string, time.Time, time.Time) error
	ValidateTokenAccess(context.Context, string, string, string, time.Time) error
}
type WorkingTokenSigner interface {
	Sign(string, string, *string) (workingtoken.Token, error)
	Verify(string, time.Time) (workingtoken.Claims, error)
}
type SessionEncryptor interface {
	Encrypt([]byte, []byte) ([]byte, error)
	Decrypt([]byte, []byte) ([]byte, error)
}
type RefreshableSessionStore interface {
	MembershipState(context.Context, string, time.Time) (store.MembershipState, error)
	ReplaceMembership(context.Context, string, []byte, []string, time.Time) error
}
type MembershipRefresher interface {
	Refresh(context.Context, []byte, string) ([]string, []byte, error)
}
type MappingRegistrar interface {
	Register(context.Context, store.Mapping) (store.Mapping, bool, error)
}
type MappingPresentationUpdater interface {
	RefreshPresentation(context.Context, string, string, *time.Time) error
}
type CanonicalTeamReader interface {
	GetTeam(context.Context, string, string) (stateregistry.Team, error)
}

type Dependencies struct {
	ConfiguredIssuer string
	AdminAuth        AdminAuthenticator
	Mappings         MappingRegistrar
	Presentation     MappingPresentationUpdater
	Teams            CanonicalTeamReader
	Login            LoginStarter
	OIDC             OIDCCompleter
	Sessions         SessionCreator
	SessionEncryptor SessionEncryptor
	SessionTTL       time.Duration
	MembershipMaxAge time.Duration
	Membership       MembershipRefresher
	WebUIURL         string
	WorkingTokens    WorkingTokenSigner
	ProxyBaseURL     string
}

type authenticatedContext struct {
	Claims workingtoken.Claims
}

func NewWithDependencies(dependencies Dependencies) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /auth/v1/login", func(w http.ResponseWriter, r *http.Request) { startLogin(w, r, dependencies.Login) })
	mux.HandleFunc("GET /auth/v1/callback", func(w http.ResponseWriter, r *http.Request) { callback(w, r, dependencies) })
	mux.HandleFunc("GET /auth/v1/teams", func(w http.ResponseWriter, r *http.Request) { listTeams(w, r, dependencies) })
	mux.HandleFunc("POST /auth/v1/token", func(w http.ResponseWriter, r *http.Request) { issueToken(w, r, dependencies) })
	mux.HandleFunc("DELETE /auth/v1/session", func(w http.ResponseWriter, r *http.Request) { logout(w, r, dependencies) })
	mux.HandleFunc("POST /admin/v1/oidc-team-mappings", func(w http.ResponseWriter, r *http.Request) { registerMapping(w, r, dependencies) })
	mux.HandleFunc("/ui/", func(w http.ResponseWriter, r *http.Request) {
		if websocket.IsWebSocketUpgrade(r) {
			proxyWebSocket(w, r, dependencies)
			return
		}
		proxyREST(w, r, dependencies)
	})
	return mux
}

const browserBearerProtocolPrefix = "flowai.bearer."

func proxyWebSocket(w http.ResponseWriter, r *http.Request, dependencies Dependencies) {
	token, selectedProtocol := websocketBearer(r)
	auth, ok := authenticateWorkingToken(w, r, dependencies, token)
	if !ok {
		return
	}
	if dependencies.ProxyBaseURL == "" {
		writeError(w, http.StatusBadGateway, "state_registry_unavailable", "State Registry is unavailable")
		return
	}
	claims := auth.Claims
	target, err := url.Parse(dependencies.ProxyBaseURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, "state_registry_unavailable", "State Registry is unavailable")
		return
	}
	if target.Scheme == "https" {
		target.Scheme = "wss"
	} else {
		target.Scheme = "ws"
	}
	target.Path, target.RawQuery = r.URL.Path, r.URL.RawQuery
	headers := make(http.Header)
	for name, values := range r.Header {
		lower := strings.ToLower(name)
		if lower == "authorization" || lower == "cookie" || lower == "sec-websocket-protocol" || strings.HasPrefix(lower, "x-flowai-") || isHopHeader(lower) || strings.HasPrefix(lower, "sec-websocket-") {
			continue
		}
		for _, value := range values {
			headers.Add(name, value)
		}
	}
	headers.Set("X-FlowAI-Operator-ID", claims.OperatorID)
	headers.Set("X-FlowAI-Team-ID", claims.TeamID)
	headers.Set("X-FlowAI-Request-ID", newRequestID())
	if claims.TeamName != nil {
		headers.Set("X-FlowAI-Team-Name", *claims.TeamName)
	}
	downstream, response, err := websocket.DefaultDialer.DialContext(r.Context(), target.String(), headers)
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
		}
		writeError(w, http.StatusBadGateway, "state_registry_unavailable", "State Registry is unavailable")
		return
	}
	defer downstream.Close()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	if selectedProtocol != "" {
		upgrader.Subprotocols = []string{selectedProtocol}
	}
	client, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer client.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	var clientWrite sync.Mutex
	expires := time.NewTimer(time.Until(time.Unix(claims.ExpiresAt, 0)))
	defer expires.Stop()
	go func() {
		select {
		case <-expires.C:
			clientWrite.Lock()
			_ = client.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4401, "working_token_expired"), time.Now().Add(time.Second))
			clientWrite.Unlock()
			cancel()
		case <-ctx.Done():
		}
	}()
	errCh := make(chan error, 2)
	go copyWebSocket(ctx, downstream, client, &clientWrite, errCh)
	go copyWebSocket(ctx, client, downstream, nil, errCh)
	select {
	case <-ctx.Done():
	case <-errCh:
	}
}

func websocketBearer(r *http.Request) (string, string) {
	const bearerPrefix = "Bearer "
	if authorization := r.Header.Get("Authorization"); strings.HasPrefix(authorization, bearerPrefix) {
		return strings.TrimSpace(strings.TrimPrefix(authorization, bearerPrefix)), ""
	}
	for _, protocol := range websocket.Subprotocols(r) {
		if !strings.HasPrefix(protocol, browserBearerProtocolPrefix) {
			continue
		}
		encoded := strings.TrimPrefix(protocol, browserBearerProtocolPrefix)
		token, err := base64.RawURLEncoding.DecodeString(encoded)
		if err == nil && len(token) > 0 {
			return string(token), protocol
		}
	}
	return "", ""
}

func copyWebSocket(ctx context.Context, source, destination *websocket.Conn, writeMu *sync.Mutex, result chan<- error) {
	for {
		messageType, payload, err := source.ReadMessage()
		if err != nil {
			result <- err
			return
		}
		if writeMu != nil {
			writeMu.Lock()
		}
		err = destination.WriteMessage(messageType, payload)
		if writeMu != nil {
			writeMu.Unlock()
		}
		if err != nil {
			result <- err
			return
		}
		select {
		case <-ctx.Done():
			result <- ctx.Err()
			return
		default:
		}
	}
}

func proxyREST(w http.ResponseWriter, r *http.Request, dependencies Dependencies) {
	const bearerPrefix = "Bearer "
	authorization := r.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, bearerPrefix) || dependencies.ProxyBaseURL == "" {
		writeError(w, http.StatusUnauthorized, "invalid_working_token", "working token is invalid")
		return
	}
	auth, ok := authenticateWorkingToken(w, r, dependencies, strings.TrimSpace(strings.TrimPrefix(authorization, bearerPrefix)))
	if !ok {
		return
	}
	claims := auth.Claims
	base, err := url.Parse(dependencies.ProxyBaseURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, "state_registry_unavailable", "State Registry is unavailable")
		return
	}
	target := base.ResolveReference(&url.URL{Path: r.URL.Path, RawQuery: r.URL.RawQuery})
	child, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), r.Body)
	if err != nil {
		writeError(w, http.StatusBadGateway, "state_registry_unavailable", "State Registry is unavailable")
		return
	}
	for name, values := range r.Header {
		lower := strings.ToLower(name)
		if lower == "authorization" || lower == "cookie" || strings.HasPrefix(lower, "x-flowai-") || isHopHeader(lower) {
			continue
		}
		for _, value := range values {
			child.Header.Add(name, value)
		}
	}
	child.Header.Set("X-FlowAI-Operator-ID", claims.OperatorID)
	child.Header.Set("X-FlowAI-Team-ID", claims.TeamID)
	child.Header.Set("X-FlowAI-Request-ID", newRequestID())
	if claims.TeamName != nil {
		child.Header.Set("X-FlowAI-Team-Name", *claims.TeamName)
	}
	response, err := http.DefaultClient.Do(child)
	if err != nil {
		writeError(w, http.StatusBadGateway, "state_registry_unavailable", "State Registry is unavailable")
		return
	}
	defer response.Body.Close()
	for name, values := range response.Header {
		if isHopHeader(strings.ToLower(name)) {
			continue
		}
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

func authenticateWorkingToken(w http.ResponseWriter, r *http.Request, dependencies Dependencies, token string) (authenticatedContext, bool) {
	if token == "" || dependencies.WorkingTokens == nil || dependencies.Sessions == nil {
		writeError(w, http.StatusUnauthorized, "invalid_working_token", "working token is invalid")
		return authenticatedContext{}, false
	}
	claims, err := dependencies.WorkingTokens.Verify(token, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_working_token", "working token is invalid")
		return authenticatedContext{}, false
	}
	if err := dependencies.Sessions.ValidateTokenAccess(r.Context(), claims.OperatorID, claims.TeamID, claims.JTI, time.Now().UTC()); err != nil {
		writeError(w, http.StatusForbidden, "team_not_accessible", "team is not accessible")
		return authenticatedContext{}, false
	}
	return authenticatedContext{Claims: claims}, true
}

func isHopHeader(name string) bool {
	switch name {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	}
	return false
}

func logout(w http.ResponseWriter, r *http.Request, dependencies Dependencies) {
	cookie, err := r.Cookie("flowai_session")
	if err != nil || cookie.Value == "" || dependencies.Sessions == nil {
		invalidSession(w, r)
		return
	}
	if err := dependencies.Sessions.Delete(r.Context(), cookie.Value); errors.Is(err, store.ErrSessionNotFound) {
		invalidSession(w, r)
		return
	} else if err != nil {
		writeError(w, http.StatusServiceUnavailable, "state_registry_unavailable", "session persistence is unavailable")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "flowai_session", Value: "", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func issueToken(w http.ResponseWriter, r *http.Request, dependencies Dependencies) {
	cookie, err := r.Cookie("flowai_session")
	if err != nil || cookie.Value == "" || dependencies.Sessions == nil || dependencies.WorkingTokens == nil {
		invalidSession(w, r)
		return
	}
	var input struct {
		TeamID string `json:"team_id"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF || input.TeamID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "token request is invalid")
		return
	}
	if !refreshMembership(w, r, cookie.Value, dependencies) {
		return
	}
	tokenContext, err := dependencies.Sessions.TokenContext(r.Context(), cookie.Value, input.TeamID, time.Now().UTC())
	if errors.Is(err, store.ErrSessionNotFound) {
		invalidSession(w, r)
		return
	}
	if errors.Is(err, store.ErrTeamNotAccessible) {
		writeError(w, http.StatusForbidden, "team_not_accessible", "team is not accessible")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "state_registry_unavailable", "session persistence is unavailable")
		return
	}
	if dependencies.Teams != nil && dependencies.Presentation != nil {
		canonical, err := dependencies.Teams.GetTeam(r.Context(), tokenContext.TeamID, requestID(r))
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "state_registry_unavailable", "State Registry is unavailable")
			return
		}
		if err := dependencies.Presentation.RefreshPresentation(r.Context(), canonical.TeamID, canonical.TeamName, canonical.ArchivedAt); err != nil {
			writeError(w, http.StatusServiceUnavailable, "state_registry_unavailable", "team presentation persistence is unavailable")
			return
		}
		tokenContext.TeamName = &canonical.TeamName
	}
	token, err := dependencies.WorkingTokens.Sign(tokenContext.OperatorID, tokenContext.TeamID, tokenContext.TeamName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid_session", "working token could not be issued")
		return
	}
	if err := dependencies.Sessions.RecordToken(r.Context(), tokenContext, token.JTI, token.IssuedAt, token.ExpiresAt); err != nil {
		writeError(w, http.StatusInternalServerError, "invalid_session", "working token could not be recorded")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"access_token": token.AccessToken, "token_type": "Bearer", "expires_in": token.ExpiresIn})
}

func refreshMembership(w http.ResponseWriter, r *http.Request, sessionID string, dependencies Dependencies) bool {
	refreshable, ok := dependencies.Sessions.(RefreshableSessionStore)
	if !ok || dependencies.Membership == nil || dependencies.SessionEncryptor == nil {
		return true
	}
	now := time.Now().UTC()
	state, err := refreshable.MembershipState(r.Context(), sessionID, now)
	if errors.Is(err, store.ErrSessionNotFound) {
		invalidSession(w, r)
		return false
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "state_registry_unavailable", "session persistence is unavailable")
		return false
	}
	if dependencies.MembershipMaxAge > 0 && now.Sub(state.ObservedAt) <= dependencies.MembershipMaxAge {
		return true
	}
	plaintext, err := dependencies.SessionEncryptor.Decrypt(state.EncryptedProviderState, []byte(sessionID))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_session", "browser session is invalid")
		return false
	}
	teamIDs, providerState, err := dependencies.Membership.Refresh(r.Context(), plaintext, state.Subject)
	if err != nil {
		writeError(w, http.StatusBadGateway, "oidc_provider_unavailable", "OIDC provider is unavailable")
		return false
	}
	encrypted, err := dependencies.SessionEncryptor.Encrypt(providerState, []byte(sessionID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid_session", "session could not be refreshed")
		return false
	}
	if err := refreshable.ReplaceMembership(r.Context(), sessionID, encrypted, teamIDs, now); err != nil {
		writeError(w, http.StatusServiceUnavailable, "state_registry_unavailable", "session persistence is unavailable")
		return false
	}
	return true
}

func startLogin(w http.ResponseWriter, r *http.Request, login LoginStarter) {
	if login == nil {
		notImplemented(w, r)
		return
	}
	location, err := login.Start(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "oidc_provider_unavailable", "OIDC provider is unavailable")
		return
	}
	http.Redirect(w, r, location, http.StatusFound)
}

func registerMapping(w http.ResponseWriter, r *http.Request, dependencies Dependencies) {
	if dependencies.AdminAuth == nil || dependencies.Mappings == nil || dependencies.Teams == nil {
		invalidAdminToken(w, r)
		return
	}
	_, err := dependencies.AdminAuth.Authenticate(r.Context(), r.Header.Get("Authorization"))
	if err != nil {
		switch {
		case errors.Is(err, adminauth.ErrProviderUnavailable):
			writeError(w, http.StatusBadGateway, "admin_identity_provider_unavailable", "administrator identity provider is unavailable")
		case errors.Is(err, adminauth.ErrInsufficientRole):
			writeError(w, http.StatusForbidden, "insufficient_admin_role", "administrator role is required")
		default:
			writeError(w, http.StatusUnauthorized, "invalid_admin_token", "administrator token is invalid")
		}
		return
	}
	var input struct {
		Issuer     string `json:"issuer"`
		OIDCTeamID string `json:"oidc_team_id"`
		TeamID     string `json:"team_id"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF || input.Issuer == "" || input.OIDCTeamID == "" || input.TeamID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "mapping request is invalid")
		return
	}
	if input.Issuer != dependencies.ConfiguredIssuer {
		writeError(w, http.StatusBadRequest, "invalid_request", "issuer is not configured")
		return
	}
	requestID := requestID(r)
	team, err := dependencies.Teams.GetTeam(r.Context(), input.TeamID, requestID)
	if err != nil {
		if errors.Is(err, stateregistry.ErrTeamNotFound) {
			writeErrorWithID(w, http.StatusNotFound, "canonical_team_not_found", "canonical team was not found", requestID)
		} else {
			writeErrorWithID(w, http.StatusServiceUnavailable, "state_registry_unavailable", "State Registry is unavailable", requestID)
		}
		return
	}
	mapping, created, err := dependencies.Mappings.Register(r.Context(), store.Mapping{Issuer: input.Issuer, OIDCTeamID: input.OIDCTeamID, TeamID: team.TeamID, TeamName: &team.TeamName, ArchivedAt: team.ArchivedAt})
	if err != nil {
		if errors.Is(err, store.ErrMappingConflict) {
			writeErrorWithID(w, http.StatusConflict, "mapping_conflict", "mapping conflicts with an existing mapping", requestID)
		} else {
			writeErrorWithID(w, http.StatusServiceUnavailable, "state_registry_unavailable", "mapping persistence is unavailable", requestID)
		}
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"issuer": mapping.Issuer, "oidc_team_id": mapping.OIDCTeamID, "team_id": mapping.TeamID, "team_name": mapping.TeamName, "archived_at": mapping.ArchivedAt})
}

func callback(w http.ResponseWriter, r *http.Request, dependencies Dependencies) {
	query := r.URL.Query()
	hasCode := query.Get("code") != ""
	hasError := query.Get("error") != ""
	if query.Get("state") == "" || hasCode == hasError {
		writeError(w, http.StatusBadRequest, "invalid_request", "callback must contain exactly one result")
		return
	}
	if hasError || dependencies.OIDC == nil || dependencies.Sessions == nil || dependencies.SessionEncryptor == nil {
		writeError(w, http.StatusUnauthorized, "invalid_oidc_identity", "OIDC identity is invalid")
		return
	}
	identity, err := dependencies.OIDC.Complete(r.Context(), query.Get("code"), query.Get("state"))
	if err != nil {
		if errors.Is(err, oidc.ErrProviderUnavailable) || errors.Is(err, jwtverify.ErrJWKSUnavailable) {
			writeError(w, http.StatusBadGateway, "oidc_provider_unavailable", "OIDC provider is unavailable")
		} else {
			writeError(w, http.StatusUnauthorized, "invalid_oidc_identity", "OIDC identity is invalid")
		}
		return
	}
	sessionID, err := randomOpaqueToken(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid_session", "session could not be created")
		return
	}
	encrypted, err := dependencies.SessionEncryptor.Encrypt(identity.ProviderState, []byte(sessionID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid_session", "session could not be created")
		return
	}
	now := time.Now().UTC()
	ttl := dependencies.SessionTTL
	if ttl <= 0 {
		ttl = time.Hour
	}
	if err := dependencies.Sessions.Create(r.Context(), store.SessionInput{SessionID: sessionID, OperatorID: identity.OperatorID, Issuer: identity.Issuer, Subject: identity.Subject, EncryptedProviderState: encrypted, OIDCTeamIDs: identity.OIDCTeamIDs, ObservedAt: now, ExpiresAt: now.Add(ttl)}); err != nil {
		writeError(w, http.StatusInternalServerError, "invalid_session", "session could not be created")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "flowai_session", Value: sessionID, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(ttl.Seconds())})
	location := dependencies.WebUIURL
	if location == "" {
		location = "/"
	}
	http.Redirect(w, r, location, http.StatusFound)
}

func listTeams(w http.ResponseWriter, r *http.Request, dependencies Dependencies) {
	cookie, err := r.Cookie("flowai_session")
	if err != nil || cookie.Value == "" || dependencies.Sessions == nil {
		invalidSession(w, r)
		return
	}
	teams, err := dependencies.Sessions.AccessibleTeams(r.Context(), cookie.Value, time.Now().UTC())
	if errors.Is(err, store.ErrSessionNotFound) {
		invalidSession(w, r)
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "state_registry_unavailable", "session persistence is unavailable")
		return
	}
	result := make([]map[string]any, 0, len(teams))
	for _, team := range teams {
		if dependencies.Teams != nil && dependencies.Presentation != nil {
			canonical, err := dependencies.Teams.GetTeam(r.Context(), team.TeamID, requestID(r))
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "state_registry_unavailable", "State Registry is unavailable")
				return
			}
			if err := dependencies.Presentation.RefreshPresentation(r.Context(), canonical.TeamID, canonical.TeamName, canonical.ArchivedAt); err != nil {
				writeError(w, http.StatusServiceUnavailable, "state_registry_unavailable", "team presentation persistence is unavailable")
				return
			}
			team.TeamName, team.ArchivedAt = &canonical.TeamName, canonical.ArchivedAt
		}
		result = append(result, map[string]any{"team_id": team.TeamID, "team_name": team.TeamName, "archived_at": team.ArchivedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"teams": result})
}

func invalidSession(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusUnauthorized, "invalid_session", "browser session is invalid")
}

func invalidAdminToken(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusUnauthorized, "invalid_admin_token", "administrator token is invalid")
}

func notImplemented(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusNotImplemented, map[string]string{
		"code":    "not_implemented",
		"message": "v0007 behavior has not been implemented",
	})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeErrorWithID(w, status, code, message, newRequestID())
}

func writeErrorWithID(w http.ResponseWriter, status int, code, message, requestID string) {
	writeJSON(w, status, errorResponse{Code: code, Message: message, RequestID: requestID})
}

func requestID(r *http.Request) string {
	if value := r.Header.Get("X-FlowAI-Request-ID"); value != "" {
		return value
	}
	return newRequestID()
}

func newRequestID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("secure random source unavailable")
	}
	return fmt.Sprintf("%x", value)
}

func randomOpaqueToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
