// Package dockerclient wraps the Docker Engine HTTP REST API calls the
// Executor needs. It exposes a small internal interface so unit tests can
// substitute a mock implementation without touching business logic.
//
// Why HTTP instead of the Docker SDK? The SDK has a complicated module
// history (the docker/docker repo was split into moby/moby/api modules in
// 2024, breaking downstream go.mod setups). The Docker daemon REST API is
// stable, well-documented, and trivial to call over a UNIX socket with the
// standard library. For v0001 we need only a handful of endpoints.
package dockerclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Labels used by the Executor to identify its containers.
const (
	LabelExecutorID  = "flowai.executor_id"
	LabelRuntime     = "flowai.runtime"
	LabelTaskID      = "flowai.task_id"
	RuntimeOpenHands = "openhands"
)

// PortMapping describes a host:container port binding.
type PortMapping struct {
	HostPort      int
	ContainerPort int
	Protocol      string // "tcp" or "udp"
}

// ContainerSpec describes one container the Executor wants to start.
type ContainerSpec struct {
	Name    string
	Image   string
	Env     []string
	Labels  map[string]string
	Ports   []PortMapping
	Command []string
}

// ContainerRef identifies a running container the Executor owns.
type ContainerRef struct {
	ID     string
	Name   string
	Image  string
	Labels map[string]string
}

type dockerAPIContainer struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	Labels map[string]string `json:"Labels"`
}

type dockerAPIContainerCreate struct {
	ID       string   `json:"Id"`
	Warnings []string `json:"Warnings"`
}

// EventMessage is a reduced form of the Docker events message.
type EventMessage struct {
	Type      string
	Action    string
	ActorID   string
	ActorName string
	Time      time.Time
}

type dockerAPIEvent struct {
	Type   string `json:"Type"`
	Action string `json:"Action"`
	Actor  struct {
		ID         string            `json:"ID"`
		Attributes map[string]string `json:"Attributes"`
	} `json:"Actor"`
	Time int64 `json:"time"`
}

// Client is the abstraction the Executor depends on.
type Client interface {
	PullImage(ctx context.Context, ref, policy string) error
	ListOwned(ctx context.Context, executorID string) ([]ContainerRef, error)
	StartContainer(ctx context.Context, spec ContainerSpec) (*ContainerRef, error)
	StopContainer(ctx context.Context, containerID string, timeout time.Duration) error
	ForceKill(ctx context.Context, containerID string) error
	ContainerLogs(ctx context.Context, containerID string) (io.ReadCloser, error)
	SubscribeEvents(ctx context.Context, filter map[string]string) (<-chan EventMessage, <-chan error)
	// ContainerURL returns the URL the Executor should use to talk to a
	// container published on the given host port. Real Docker impls return
	// 127.0.0.1:<hostPort>; test fakes may redirect.
	ContainerURL(hostPort int) string
}

// HTTPClient is the default implementation backed by the Docker daemon's HTTP API.
type HTTPClient struct {
	baseURL string
	http    *http.Client

	hook *TestHook // optional, used by tests
}

// NewHTTPClient returns a Client backed by the Docker daemon at sockPath.
func NewHTTPClient(sockPath string) (*HTTPClient, error) {
	if sockPath == "" {
		sockPath = "/var/run/docker.sock"
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "unix", sockPath)
		},
		MaxIdleConns:        10,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  true,
		TLSHandshakeTimeout: 5 * time.Second,
	}
	return &HTTPClient{
		baseURL: "http://docker",
		http:    &http.Client{Transport: tr, Timeout: 60 * time.Second},
	}, nil
}

// Ping verifies the daemon is reachable.
func (c *HTTPClient) Ping(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodGet, "/_ping", nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("docker ping: status %d", resp.StatusCode)
}

// PullImage implements Client.
func (c *HTTPClient) PullImage(ctx context.Context, ref, policy string) error {
	policy = strings.ToLower(policy)
	if policy == "" {
		policy = "if-not-present"
	}
	if policy == "never" {
		return nil
	}
	if policy == "if-not-present" {
		resp, err := c.do(ctx, http.MethodGet, "/images/"+ref+"/json", nil, nil)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
	}
	resp, err := c.do(ctx, http.MethodPost, "/images/create?fromImage="+url.QueryEscape(ref), nil, nil)
	if err != nil {
		return fmt.Errorf("docker pull %s: %w", ref, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// ListOwned implements Client.
func (c *HTTPClient) ListOwned(ctx context.Context, executorID string) ([]ContainerRef, error) {
	q := url.Values{}
	q.Set("all", "1")
	q.Set("filters", buildFilterJSON(map[string]string{LabelExecutorID: executorID}))
	resp, err := c.do(ctx, http.MethodGet, "/containers/json", nil, q)
	if err != nil {
		return nil, fmt.Errorf("docker list containers: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	var raw []dockerAPIContainer
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode docker list: %w", err)
	}
	out := make([]ContainerRef, 0, len(raw))
	for _, r := range raw {
		name := ""
		if len(r.Names) > 0 {
			name = strings.TrimPrefix(r.Names[0], "/")
		}
		out = append(out, ContainerRef{
			ID:     r.ID,
			Name:   name,
			Image:  r.Image,
			Labels: r.Labels,
		})
	}
	return out, nil
}

// StartContainer implements Client.
func (c *HTTPClient) StartContainer(ctx context.Context, spec ContainerSpec) (*ContainerRef, error) {
	portBindings := make(map[string][]map[string]string, len(spec.Ports))
	exposedPorts := make(map[string]any, len(spec.Ports))
	for _, p := range spec.Ports {
		key := fmt.Sprintf("%d/%s", p.ContainerPort, strings.ToLower(p.Protocol))
		portBindings[key] = []map[string]string{{"HostPort": fmt.Sprintf("%d", p.HostPort)}}
		exposedPorts[key] = map[string]any{}
	}
	body := map[string]any{
		"Image":        spec.Image,
		"Env":          spec.Env,
		"Labels":       spec.Labels,
		"ExposedPorts": exposedPorts,
		"Cmd":          spec.Command,
		"HostConfig": map[string]any{
			"PortBindings": portBindings,
			"AutoRemove":   false,
		},
	}
	resp, err := c.do(ctx, http.MethodPost, "/containers/create?name="+url.QueryEscape(spec.Name), jsonBody(body), nil)
	if err != nil {
		return nil, fmt.Errorf("docker create: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("docker create: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var created dockerAPIContainerCreate
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return nil, fmt.Errorf("decode create response: %w", err)
	}
	if err := c.startByID(ctx, created.ID); err != nil {
		return nil, err
	}
	return &ContainerRef{
		ID:     created.ID,
		Name:   spec.Name,
		Image:  spec.Image,
		Labels: spec.Labels,
	}, nil
}

func (c *HTTPClient) startByID(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodPost, "/containers/"+id+"/start", nil, nil)
	if err != nil {
		return fmt.Errorf("docker start: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("docker start: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

// StopContainer implements Client.
func (c *HTTPClient) StopContainer(ctx context.Context, containerID string, timeout time.Duration) error {
	q := url.Values{}
	q.Set("t", fmt.Sprintf("%d", int(timeout.Seconds())))
	resp, err := c.do(ctx, http.MethodPost, "/containers/"+containerID+"/stop", nil, q)
	if err != nil && !IsNotFound(err) {
		return err
	}
	if resp != nil {
		resp.Body.Close()
	}
	rm, err := c.do(ctx, http.MethodDelete, "/containers/"+containerID, nil, url.Values{"force": {"true"}, "v": {"true"}})
	if err != nil && !IsNotFound(err) {
		return err
	}
	if rm != nil {
		rm.Body.Close()
	}
	return nil
}

// ForceKill implements Client.
func (c *HTTPClient) ForceKill(ctx context.Context, containerID string) error {
	resp, err := c.do(ctx, http.MethodPost, "/containers/"+containerID+"/kill", nil, url.Values{"signal": {"SIGKILL"}})
	if err != nil && !IsNotFound(err) {
		return err
	}
	if resp != nil {
		resp.Body.Close()
	}
	rm, err := c.do(ctx, http.MethodDelete, "/containers/"+containerID, nil, url.Values{"force": {"true"}, "v": {"true"}})
	if err != nil && !IsNotFound(err) {
		return err
	}
	if rm != nil {
		rm.Body.Close()
	}
	return nil
}

// ContainerLogs implements Client.
func (c *HTTPClient) ContainerLogs(ctx context.Context, containerID string) (io.ReadCloser, error) {
	q := url.Values{}
	q.Set("stdout", "1")
	q.Set("stderr", "1")
	q.Set("follow", "1")
	resp, err := c.do(ctx, http.MethodGet, "/containers/"+containerID+"/logs", nil, q)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("docker logs: status %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// SubscribeEvents implements Client.
func (c *HTTPClient) SubscribeEvents(ctx context.Context, filter map[string]string) (<-chan EventMessage, <-chan error) {
	out := make(chan EventMessage, 16)
	errc := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errc)
		q := url.Values{}
		if len(filter) > 0 {
			q.Set("filters", buildFilterJSON(filter))
		}
		resp, err := c.do(ctx, http.MethodGet, "/events", nil, q)
		if err != nil {
			errc <- err
			return
		}
		defer resp.Body.Close()
		reader := bufio.NewReaderSize(resp.Body, 64*1024)
		for {
			if ctx.Err() != nil {
				errc <- ctx.Err()
				return
			}
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 {
				var ev dockerAPIEvent
				if jerr := json.Unmarshal(line, &ev); jerr == nil {
					out <- EventMessage{
						Type:      ev.Type,
						Action:    ev.Action,
						ActorID:   ev.Actor.ID,
						ActorName: ev.Actor.Attributes["name"],
						Time:      time.Unix(ev.Time, 0),
					}
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
					return
				}
				errc <- err
				return
			}
		}
	}()
	return out, errc
}

// ContainerURL implements Client.
func (c *HTTPClient) ContainerURL(hostPort int) string {
	return fmt.Sprintf("http://127.0.0.1:%d", hostPort)
}

func (c *HTTPClient) do(ctx context.Context, method, path string, body io.Reader, query url.Values) (*http.Response, error) {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		reader = body
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, errNotFound
	}
	return resp, nil
}

var errNotFound = errors.New("docker: not found")

// IsNotFound reports whether err means a Docker 404.
func IsNotFound(err error) bool { return errors.Is(err, errNotFound) }

// jsonBody returns an io.Reader wrapping the JSON encoding of v.
func jsonBody(v any) io.Reader {
	buf := &bytes.Buffer{}
	if err := json.NewEncoder(buf).Encode(v); err != nil {
		return bytes.NewReader([]byte("{}"))
	}
	return buf
}

// buildFilterJSON encodes a flat filter map to the JSON shape Docker expects.
func buildFilterJSON(m map[string]string) string {
	expanded := map[string][]string{}
	for k, v := range m {
		expanded[k] = []string{v}
	}
	b, _ := json.Marshal(expanded)
	return string(b)
}

// TestHook lets tests inject a custom transport.
type TestHook struct {
	mu sync.Mutex
	tr http.RoundTripper
}

// SetRoundTripper installs a custom RoundTripper.
func (h *TestHook) SetRoundTripper(rt http.RoundTripper) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tr = rt
}

// Compile-time interface check.
var _ Client = (*HTTPClient)(nil)