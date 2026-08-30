// Package stateregistry implements API Gateway's narrow State Registry view.
package stateregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

var (
	ErrTeamNotFound = errors.New("canonical team not found")
	ErrUnavailable  = errors.New("state registry unavailable")
)

type Team struct {
	TeamID     string     `json:"team_id"`
	TeamName   string     `json:"team_name"`
	ArchivedAt *time.Time `json:"archived_at"`
}

type Client struct {
	baseURL *url.URL
	http    *http.Client
}

func New(rawBaseURL string, timeout time.Duration) (*Client, error) {
	baseURL, err := url.Parse(rawBaseURL)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, errors.New("state registry URL must be absolute")
	}
	if timeout <= 0 {
		return nil, errors.New("state registry timeout must be positive")
	}
	return &Client{baseURL: baseURL, http: &http.Client{Timeout: timeout}}, nil
}

func (c *Client) GetTeam(ctx context.Context, teamID, requestID string) (Team, error) {
	if teamID == "" {
		return Team{}, errors.New("team_id is required")
	}
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "/internal/v1/teams/" + url.PathEscape(teamID)})
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Team{}, fmt.Errorf("create team request: %w", err)
	}
	request.Header.Set("X-FlowAI-Request-ID", requestID)
	response, err := c.http.Do(request)
	if err != nil {
		return Team{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
		var team Team
		decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
		if err := decoder.Decode(&team); err != nil || team.TeamID != teamID || team.TeamName == "" {
			return Team{}, fmt.Errorf("%w: invalid team response", ErrUnavailable)
		}
		return team, nil
	case http.StatusNotFound:
		return Team{}, ErrTeamNotFound
	default:
		return Team{}, fmt.Errorf("%w: status %d", ErrUnavailable, response.StatusCode)
	}
}
