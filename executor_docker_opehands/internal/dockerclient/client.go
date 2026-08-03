// Package dockerclient is the Docker Engine integration boundary for the
// Executor. All Docker Engine SDK types are kept inside this package so the
// business logic in internal/executor never imports the upstream Docker
// module. The exposed surface (the Client interface and the surrounding
// pure-Go types) has no SDK types in it.
//
// Per ADR-0002 we use the official Docker Engine SDK for Go
// (github.com/docker/docker/client) over the Docker daemon UNIX socket.
// The socket path is configurable via DOCKER_SOCKET_PATH (default
// /var/run/docker.sock).
//
// Implementation notes:
//
//   - PullImage honours the IMAGE_PULL_POLICY setting ("always",
//     "if-not-present", "never"). "never" is a no-op, "if-not-present"
//     skips the pull when ImageInspect succeeds, otherwise we fall through
//     to ImagePull and drain the JSON progress stream.
//   - ListOwned uses label-based filtering via SDK filters.Args.
//   - StartContainer creates the container with the documented labels
//     (flowai.executor_id, flowai.cleanup_id, flowai.runtime, flowai.task_id)
//     and binds host_port:container_port=8000/tcp.
//   - ContainerURL returns the URL the Executor should use to talk to the
//     container on the published host port. Real Docker engines return
//     127.0.0.1:<hostPort>; test fakes redirect as needed.
package dockerclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	dockertypes "github.com/docker/docker/api/types/container"
	dockerevents "github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	dockerimage "github.com/docker/docker/api/types/image"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"

	"github.com/flowai/platform/executor_docker_opehands/internal/logging"
)

// Labels used by the Executor to identify its containers.
//
// flowai.executor_id is unique per process instance (matches the State
// Registry identity). flowai.cleanup_id is stable across process restarts
// (so a fresh process can find and remove leftover containers from a
// previous run). They are intentionally distinct.
//
// flowai.team_id, flowai.executor_scope, flowai.command_id, and
// flowai.resolved_image_source are v0002 record-keeping labels the
// Docker metadata surface exposes to contract tests; the values are
// carried verbatim from the State Registry's claim response.
const (
	LabelExecutorID          = "flowai.executor_id"
	LabelCleanupID           = "flowai.cleanup_id"
	LabelRuntime             = "flowai.runtime"
	LabelTaskID              = "flowai.task_id"
	LabelTeamID              = "flowai.team_id"
	LabelExecutorScope       = "flowai.executor_scope"
	LabelCommandID           = "flowai.command_id"
	LabelResolvedImageSource = "flowai.resolved_image_source"
	RuntimeOpenHands         = "openhands"
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

// EventMessage is a reduced form of the Docker events message.
type EventMessage struct {
	Type      string
	Action    string
	ActorID   string
	ActorName string
	Time      time.Time
}

// errNotFound is the package-local sentinel for 404s returned to tests that
// construct errors directly.
var errNotFound = errors.New("docker: not found")

// Client is the abstraction the Executor depends on. The implementation is
// the SDK-backed SDKClient; tests may substitute their own.
type Client interface {
	PullImage(ctx context.Context, ref, policy string) error
	ListOwned(ctx context.Context, executorID string) ([]ContainerRef, error)
	ListLeftover(ctx context.Context, cleanupID string) ([]ContainerRef, error)
	StartContainer(ctx context.Context, spec ContainerSpec) (*ContainerRef, error)
	StopContainer(ctx context.Context, containerID string, timeout time.Duration) error
	ForceKill(ctx context.Context, containerID string) error
	ContainerLogs(ctx context.Context, containerID string) (io.ReadCloser, error)
	// SubscribeEvents subscribes to the Docker events stream filtered by
	// the given EventFilter. The filter is forwarded to the Docker daemon
	// using the documented `filters.Args` shape; callers must use
	// EventLabelFilter entries (not invalid "label:key" keys).
	SubscribeEvents(ctx context.Context, filter EventFilter) (<-chan EventMessage, <-chan error)
	// ContainerURL returns the URL the Executor should use to talk to a
	// container published on the given host port. Real Docker impls return
	// 127.0.0.1:<hostPort>; test fakes may redirect.
	ContainerURL(hostPort int) string
}

// EventFilterKey enumerates the documented Docker filter keys. Using a
// closed set lets the compiler catch mistakes like the previous
// `"label:cleanup"` (not a real key) before they reach the daemon.
type EventFilterKey string

const (
	EventFilterKeyType      EventFilterKey = "type"
	EventFilterKeyLabel     EventFilterKey = "label"
	EventFilterKeyEvent     EventFilterKey = "event"
	EventFilterKeyContainer EventFilterKey = "container"
)

// EventFilter is an ordered list of (key, value) pairs accepted by the
// Docker events API. The Docker filter model is a multi-valued map: the
// same key (e.g. "label") may repeat with different values.
type EventFilter struct {
	Entries []EventFilterEntry
}

// EventFilterEntry is one (key, value) pair.
type EventFilterEntry struct {
	Key   EventFilterKey
	Value string
}

// NewEventFilter constructs an EventFilter from (key, value) pairs.
// Multiple entries with the same key are preserved (Docker filters use a
// set semantics per key).
func NewEventFilter(entries ...EventFilterEntry) EventFilter {
	return EventFilter{Entries: entries}
}

// IsNotFound reports whether err means a Docker 404.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errdefs.IsNotFound(err) {
		return true
	}
	return errors.Is(err, errNotFound)
}

// SDKClient is the production Client backed by the official Docker Engine
// SDK. The underlying *dockerclient.Client is intentionally kept unexported
// so SDK types never escape the package boundary.
type SDKClient struct {
	cli *dockerclient.Client
}

// NewSDKClient returns a Client backed by the official Docker Engine SDK.
// sockPath is the path to the Docker daemon UNIX socket
// (e.g. /var/run/docker.sock). When empty, the SDK default applies.
func NewSDKClient(sockPath string) (*SDKClient, error) {
	if sockPath == "" {
		sockPath = "/var/run/docker.sock"
	}
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.WithHost("unix://"+sockPath),
		dockerclient.WithVersionFromEnv(),
		dockerclient.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &SDKClient{cli: cli}, nil
}

// Ping verifies the daemon is reachable.
func (c *SDKClient) Ping(ctx context.Context) error {
	if _, err := c.cli.Ping(ctx); err != nil {
		return fmt.Errorf("docker ping: %w", err)
	}
	return nil
}

// PullImage implements Client.
func (c *SDKClient) PullImage(ctx context.Context, ref, policy string) error {
	policy = strings.ToLower(strings.TrimSpace(policy))
	if policy == "" {
		policy = "if-not-present"
	}
	logger := logging.FromContext(ctx)
	switch policy {
	case "never":
		return nil
	case "if-not-present":
		if _, err := c.cli.ImageInspect(ctx, ref); err == nil {
			return nil
		}
		// Image not present locally; fall through to pull.
		logger.Info("pulling openhands image", "ref", ref, "policy", policy)
	case "always":
		logger.Info("force-pulling openhands image", "ref", ref, "policy", policy)
	default:
		// Unknown policies degrade to pulling once.
		logger.Warn("unknown image_pull_policy; defaulting to pull", "policy", policy, "ref", ref)
	}

	rc, err := c.cli.ImagePull(ctx, ref, dockerimage.PullOptions{})
	if err != nil {
		return fmt.Errorf("docker pull %s: %w", ref, err)
	}
	defer rc.Close()
	// Drain the progress stream so the daemon finishes the pull.
	if _, err := io.Copy(io.Discard, rc); err != nil {
		return fmt.Errorf("docker pull drain %s: %w", ref, err)
	}
	return nil
}

// ListOwned implements Client. It returns containers whose flowai.executor_id
// label matches executorID. The matching process executor_id is unique per
// process, so this is used for live introspection of the current run only.
func (c *SDKClient) ListOwned(ctx context.Context, executorID string) ([]ContainerRef, error) {
	return c.listByLabel(ctx, LabelExecutorID, executorID)
}

// ListLeftover implements Client. It returns containers whose flowai.cleanup_id
// label matches cleanupID. The cleanup_id is stable across process restarts,
// so this is what startup cleanup uses to find previous-run leftovers.
func (c *SDKClient) ListLeftover(ctx context.Context, cleanupID string) ([]ContainerRef, error) {
	return c.listByLabel(ctx, LabelCleanupID, cleanupID)
}

func (c *SDKClient) listByLabel(ctx context.Context, key, value string) ([]ContainerRef, error) {
	// Docker / Podman expect the filter key to be the literal string
	// "label" and the value to be "key=value" (or repeated pairs). Earlier
	// versions of this file passed `key` directly, which Podman rejects
	// with "X is an invalid filter".
	args := filters.NewArgs(filters.Arg("label", key+"="+value))
	list, err := c.cli.ContainerList(ctx, dockertypes.ListOptions{All: true, Filters: args})
	if err != nil {
		return nil, fmt.Errorf("docker list (label %s=%s): %w", key, value, err)
	}
	out := make([]ContainerRef, 0, len(list))
	for _, ctr := range list {
		name := ""
		if len(ctr.Names) > 0 {
			name = strings.TrimPrefix(ctr.Names[0], "/")
		}
		out = append(out, ContainerRef{
			ID:     ctr.ID,
			Name:   name,
			Image:  ctr.Image,
			Labels: ctr.Labels,
		})
	}
	return out, nil
}

// StartContainer implements Client.
func (c *SDKClient) StartContainer(ctx context.Context, spec ContainerSpec) (*ContainerRef, error) {
	cfg := &dockertypes.Config{
		Image:  spec.Image,
		Env:    append([]string(nil), spec.Env...),
		Labels: copyLabels(spec.Labels),
		Cmd:    append([]string(nil), spec.Command...),
	}
	hostCfg := &dockertypes.HostConfig{
		AutoRemove: false,
	}
	if len(spec.Ports) > 0 {
		portBindings, exposedPorts := buildPortBindings(spec.Ports)
		cfg.ExposedPorts = exposedPorts
		hostCfg.PortBindings = portBindings
	}

	created, err := c.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, spec.Name)
	if err != nil {
		return nil, fmt.Errorf("docker create: %w", err)
	}
	if err := c.cli.ContainerStart(ctx, created.ID, dockertypes.StartOptions{}); err != nil {
		return nil, fmt.Errorf("docker start: %w", err)
	}
	return &ContainerRef{
		ID:     created.ID,
		Name:   spec.Name,
		Image:  spec.Image,
		Labels: copyLabels(spec.Labels),
	}, nil
}

// StopContainer implements Client.
func (c *SDKClient) StopContainer(ctx context.Context, containerID string, timeout time.Duration) error {
	t := int(timeout.Seconds())
	stopOpts := dockertypes.StopOptions{Timeout: &t}
	if err := c.cli.ContainerStop(ctx, containerID, stopOpts); err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	removeOpts := dockertypes.RemoveOptions{Force: true, RemoveVolumes: true}
	if err := c.cli.ContainerRemove(ctx, containerID, removeOpts); err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	return nil
}

// ForceKill implements Client.
func (c *SDKClient) ForceKill(ctx context.Context, containerID string) error {
	if err := c.cli.ContainerKill(ctx, containerID, "SIGKILL"); err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	removeOpts := dockertypes.RemoveOptions{Force: true, RemoveVolumes: true}
	if err := c.cli.ContainerRemove(ctx, containerID, removeOpts); err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	return nil
}

// ContainerLogs implements Client. The caller is responsible for closing the
// returned reader when done.
func (c *SDKClient) ContainerLogs(ctx context.Context, containerID string) (io.ReadCloser, error) {
	opts := dockertypes.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Timestamps: false,
	}
	return c.cli.ContainerLogs(ctx, containerID, opts)
}

// SubscribeEvents implements Client. Each EventFilterEntry is forwarded to
// the Docker events API using `filters.Args.Add(key, value)`; the SDK
// correctly accumulates repeated keys.
func (c *SDKClient) SubscribeEvents(ctx context.Context, filter EventFilter) (<-chan EventMessage, <-chan error) {
	args := filters.NewArgs()
	for _, e := range filter.Entries {
		args.Add(string(e.Key), e.Value)
	}
	opts := dockerevents.ListOptions{Filters: args}
	src, errs := c.cli.Events(ctx, opts)

	out := make(chan EventMessage, 16)
	errc := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errc)
		for {
			select {
			case <-ctx.Done():
				errc <- ctx.Err()
				return
			case e, ok := <-src:
				if !ok {
					return
				}
				actorName := ""
				if e.Actor.Attributes != nil {
					actorName = e.Actor.Attributes["name"]
				}
				t := time.Unix(e.Time, 0)
				if e.Time == 0 {
					t = time.Now()
				}
				out <- EventMessage{
					Type:      string(e.Type),
					Action:    string(e.Action),
					ActorID:   e.Actor.ID,
					ActorName: actorName,
					Time:      t,
				}
			case e, ok := <-errs:
				if !ok {
					return
				}
				if e != nil && !errors.Is(e, context.Canceled) && !errors.Is(e, io.EOF) {
					errc <- e
					return
				}
			}
		}
	}()
	return out, errc
}

// ContainerURL implements Client.
func (c *SDKClient) ContainerURL(hostPort int) string {
	return fmt.Sprintf("http://127.0.0.1:%d", hostPort)
}

// Close releases any resources held by the underlying SDK client.
func (c *SDKClient) Close() error {
	if c.cli == nil {
		return nil
	}
	return c.cli.Close()
}

// buildPortBindings converts the simple PortMapping list into the SDK's
// nat.PortMap and ExposedPorts set.
func buildPortBindings(ports []PortMapping) (nat.PortMap, nat.PortSet) {
	bindings := nat.PortMap{}
	exposed := nat.PortSet{}
	for _, p := range ports {
		proto := strings.ToLower(p.Protocol)
		if proto == "" {
			proto = "tcp"
		}
		key, err := nat.NewPort(proto, fmt.Sprintf("%d", p.ContainerPort))
		if err != nil {
			continue
		}
		bindings[key] = []nat.PortBinding{
			{HostIP: "127.0.0.1", HostPort: fmt.Sprintf("%d", p.HostPort)},
		}
		exposed[key] = struct{}{}
	}
	return bindings, exposed
}

func copyLabels(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Compile-time interface check.
var _ Client = (*SDKClient)(nil)
