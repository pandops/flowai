// Package adminauth authorizes externally provisioned system administrators.
package adminauth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/flowai/platform/svc/api-gateway/internal/jwtverify"
)

var (
	ErrInvalidToken        = errors.New("invalid administrator token")
	ErrInsufficientRole    = errors.New("insufficient administrator role")
	ErrProviderUnavailable = errors.New("administrator identity provider unavailable")
)

type Authenticator struct {
	verifier     *jwtverify.Verifier
	rolePointer  string
	requiredRole string
	now          func() time.Time
}

func New(verifier *jwtverify.Verifier, rolePointer, requiredRole string) (*Authenticator, error) {
	if verifier == nil || rolePointer == "" || requiredRole == "" {
		return nil, errors.New("administrator trust configuration is incomplete")
	}
	if !strings.HasPrefix(rolePointer, "/") {
		return nil, errors.New("invalid role pointer")
	}
	return &Authenticator{verifier: verifier, rolePointer: rolePointer, requiredRole: requiredRole, now: time.Now}, nil
}

func (a *Authenticator) Authenticate(ctx context.Context, authorization string) (string, error) {
	const prefix = "Bearer "
	if !strings.HasPrefix(authorization, prefix) || strings.TrimSpace(strings.TrimPrefix(authorization, prefix)) == "" {
		return "", ErrInvalidToken
	}
	claims, err := a.verifier.Verify(ctx, strings.TrimSpace(strings.TrimPrefix(authorization, prefix)), a.now())
	if errors.Is(err, jwtverify.ErrJWKSUnavailable) {
		return "", ErrProviderUnavailable
	}
	if err != nil {
		return "", ErrInvalidToken
	}
	subject, ok := claims["sub"].(string)
	if !ok || subject == "" {
		return "", ErrInvalidToken
	}
	roles, err := jwtverify.ValuesAtPointer(claims, a.rolePointer)
	if errors.Is(err, jwtverify.ErrClaimMissing) {
		return "", ErrInsufficientRole
	}
	if err != nil {
		return "", ErrInvalidToken
	}
	for _, role := range roles {
		if role == a.requiredRole {
			return subject, nil
		}
	}
	return "", ErrInsufficientRole
}
