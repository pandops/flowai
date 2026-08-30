package oidc

import (
	"reflect"
	"testing"
)

func TestTeamClaimAdapterNormalize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		adapter TeamClaimAdapter
		claims  map[string]any
		want    []string
		wantErr bool
	}{
		{"string array deduplicates", TeamClaimAdapter{Kind: StringArray, ClaimPointer: "/teams"}, map[string]any{"teams": []any{"alpha", "alpha", "beta"}}, []string{"alpha", "beta"}, false},
		{"nested escaped pointer", TeamClaimAdapter{Kind: StringArray, ClaimPointer: "/membership~1v1/teams~0current"}, map[string]any{"membership/v1": map[string]any{"teams~current": []any{"alpha"}}}, []string{"alpha"}, false},
		{"object array ignores display", TeamClaimAdapter{Kind: ObjectArray, ClaimPointer: "/membership/teams", IDField: "id", NameField: "name"}, map[string]any{"membership": map[string]any{"teams": []any{map[string]any{"id": "alpha", "name": "forged"}}}}, []string{"alpha"}, false},
		{"empty array valid", TeamClaimAdapter{Kind: StringArray, ClaimPointer: "/teams"}, map[string]any{"teams": []any{}}, []string{}, false},
		{"missing claim", TeamClaimAdapter{Kind: StringArray, ClaimPointer: "/teams"}, map[string]any{}, nil, true},
		{"partly malformed string array", TeamClaimAdapter{Kind: StringArray, ClaimPointer: "/teams"}, map[string]any{"teams": []any{"alpha", ""}}, nil, true},
		{"partly malformed object array", TeamClaimAdapter{Kind: ObjectArray, ClaimPointer: "/teams", IDField: "id"}, map[string]any{"teams": []any{map[string]any{"id": "alpha"}, map[string]any{"name": "missing"}}}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.adapter.Normalize(tt.claims)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Normalize() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Normalize() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestTeamClaimAdapterValidate(t *testing.T) {
	t.Parallel()
	invalid := []TeamClaimAdapter{
		{},
		{Kind: "groups", ClaimPointer: "/teams"},
		{Kind: StringArray},
		{Kind: StringArray, ClaimPointer: "teams"},
		{Kind: StringArray, ClaimPointer: "/teams", IDField: "id"},
		{Kind: ObjectArray, ClaimPointer: "/teams"},
	}
	for _, adapter := range invalid {
		if err := adapter.Validate(); err == nil {
			t.Fatalf("Validate(%+v) succeeded", adapter)
		}
	}
}

func TestNormalizeScopes(t *testing.T) {
	t.Parallel()
	got, err := NormalizeScopes([]string{" profile ", "openid", "profile", "membership.read"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"openid", "profile", "membership.read"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeScopes() = %#v, want %#v", got, want)
	}
	for _, scopes := range [][]string{{""}, {"openid profile"}} {
		if _, err := NormalizeScopes(scopes); err == nil {
			t.Fatalf("NormalizeScopes(%q) succeeded", scopes)
		}
	}
}
