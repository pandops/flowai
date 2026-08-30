package stateregistry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGetTeam(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/teams/team-alpha" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("X-FlowAI-Request-ID") != "request-1" {
			t.Errorf("request ID missing")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"team_id":"team-alpha","team_name":"Alpha","archived_at":null}`))
	}))
	t.Cleanup(server.Close)
	client, err := New(server.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	team, err := client.GetTeam(context.Background(), "team-alpha", "request-1")
	if err != nil {
		t.Fatal(err)
	}
	if team.TeamName != "Alpha" {
		t.Fatalf("team = %+v", team)
	}
}

func TestGetTeamMapsDependencyOutcomes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		status int
		want   error
	}{{404, ErrTeamNotFound}, {500, ErrUnavailable}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tt.status) }))
		client, err := New(server.URL, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.GetTeam(context.Background(), "team-alpha", "request-1")
		server.Close()
		if !errors.Is(err, tt.want) {
			t.Fatalf("status %d error = %v", tt.status, err)
		}
	}
}
