package adminauth

import (
	"errors"
	"testing"
)

func TestValuesAtPointerDistinguishesMissingRoleClaim(t *testing.T) {
	t.Parallel()
	if _, err := valuesAtPointer(map[string]any{}, "/realm_access/roles"); !errors.Is(err, errClaimMissing) {
		t.Fatalf("valuesAtPointer() error = %v, want errClaimMissing", err)
	}
	if _, err := valuesAtPointer(map[string]any{"realm_access": map[string]any{"roles": "admin"}}, "/realm_access/roles"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("valuesAtPointer() malformed error = %v, want ErrInvalid", err)
	}
}
