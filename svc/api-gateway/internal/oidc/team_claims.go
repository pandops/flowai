// Package oidc implements provider-neutral OIDC protocol and team-claim handling.
package oidc

import (
	"errors"
	"fmt"
	"strings"
)

type AdapterKind string

const (
	StringArray AdapterKind = "string_array"
	ObjectArray AdapterKind = "object_array"
)

type TeamClaimAdapter struct {
	Kind         AdapterKind
	ClaimPointer string
	IDField      string
	NameField    string
}

func (a TeamClaimAdapter) Validate() error {
	if a.Kind != StringArray && a.Kind != ObjectArray {
		return fmt.Errorf("unsupported team claim adapter %q", a.Kind)
	}
	if _, err := parsePointer(a.ClaimPointer); err != nil {
		return fmt.Errorf("claim pointer: %w", err)
	}
	if a.Kind == StringArray && (a.IDField != "" || a.NameField != "") {
		return errors.New("string_array does not accept object fields")
	}
	if a.Kind == ObjectArray && strings.TrimSpace(a.IDField) == "" {
		return errors.New("object_array requires id_field")
	}
	return nil
}

func (a TeamClaimAdapter) Normalize(claims map[string]any) ([]string, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	value, err := resolvePointer(claims, a.ClaimPointer)
	if err != nil {
		return nil, err
	}
	items, ok := value.([]any)
	if !ok {
		return nil, errors.New("team claim must be an array")
	}
	result := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for index, item := range items {
		var id string
		switch a.Kind {
		case StringArray:
			id, ok = item.(string)
		case ObjectArray:
			object, objectOK := item.(map[string]any)
			if objectOK {
				id, ok = object[a.IDField].(string)
			} else {
				ok = false
			}
		}
		if !ok || id == "" {
			return nil, fmt.Errorf("team claim element %d has no non-empty identifier", index)
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result, nil
}

func NormalizeScopes(additional []string) ([]string, error) {
	result := []string{"openid"}
	seen := map[string]struct{}{"openid": {}}
	for _, raw := range additional {
		scope := strings.TrimSpace(raw)
		if scope == "" || strings.ContainsAny(scope, " \t\r\n") {
			return nil, fmt.Errorf("invalid OIDC scope %q", raw)
		}
		if _, duplicate := seen[scope]; duplicate {
			continue
		}
		seen[scope] = struct{}{}
		result = append(result, scope)
	}
	return result, nil
}

func resolvePointer(document map[string]any, pointer string) (any, error) {
	tokens, err := parsePointer(pointer)
	if err != nil {
		return nil, err
	}
	var current any = document
	for _, token := range tokens {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, errors.New("claim pointer traverses a non-object")
		}
		current, ok = object[token]
		if !ok || current == nil {
			return nil, errors.New("claim pointer target is missing or null")
		}
	}
	return current, nil
}

func parsePointer(pointer string) ([]string, error) {
	if pointer == "" || !strings.HasPrefix(pointer, "/") {
		return nil, errors.New("must be a non-empty RFC 6901 JSON pointer")
	}
	raw := strings.Split(pointer[1:], "/")
	tokens := make([]string, len(raw))
	for i, token := range raw {
		var builder strings.Builder
		for j := 0; j < len(token); j++ {
			if token[j] != '~' {
				builder.WriteByte(token[j])
				continue
			}
			if j+1 >= len(token) || (token[j+1] != '0' && token[j+1] != '1') {
				return nil, errors.New("contains an invalid escape")
			}
			j++
			if token[j] == '0' {
				builder.WriteByte('~')
			} else {
				builder.WriteByte('/')
			}
		}
		tokens[i] = builder.String()
	}
	return tokens, nil
}
