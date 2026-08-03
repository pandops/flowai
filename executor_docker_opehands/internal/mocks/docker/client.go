// Package mocks contains shared test fakes for FlowAI backend services.
// Production-mocked services (the v0001 mocked task server) live under
// internal/mocks/task-server; this package is for in-memory test doubles.
package mocks

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/flowai/platform/executor_docker_opehands/internal/dockerclient"
)

// FakeDocker is an in-memory implementation of dockerclient.Client for tests.
type FakeDocker struct {
	mu sync.Mutex

	// Containers keyed by container ID.
	containers map[string]*fakeContainer
	// Images present locally.
	images map[string]bool
	// Events channel for tests that want to subscribe.
	Events chan dockerclient.EventMessage
	// Failure injected per operation (test-only).
	FailNext struct {
		Pull            bool
		Create          bool
		Start           bool
		Stop            bool
		Kill            bool
		EventsError     error
		EventsCloseErr  bool
		EventsCloseMsgs bool
	}
	// Auto-kill on container start (simulates immediate failure).
	KillOnStart bool

	// Per-container HostPort -> URL override. When a container is started
	// with a HostPort that has an entry here, the executor should be told
	// to talk to the override URL via ContainerURLFor.
	URLOverrides map[int]string

	// pullCallCount tracks how many times PullImage was called.
	pullCallCount int
	// startedRefs records each successful StartContainer.
	startedRefs []dockerclient.ContainerRef
	// startedPorts records the host port of each successful StartContainer.
	startedPorts []int

	closeEventsMu sync.Once
}

type fakeContainer struct {
	ID       string
	Name     string
	Image    string
	Labels   map[string]string
	Running  bool
	HostPort int
}

// NewFakeDocker constructs a FakeDocker with empty state.
func NewFakeDocker() *FakeDocker {
	return &FakeDocker{
		containers:   map[string]*fakeContainer{},
		images:       map[string]bool{},
		Events:       make(chan dockerclient.EventMessage, 64),
		URLOverrides: map[int]string{},
	}
}

// ContainerURLFor returns the URL the Executor should use to talk to a
// container started on hostPort. If a URL override is set for that port,
// it is returned. Otherwise the default 127.0.0.1:port URL is returned.
func (f *FakeDocker) ContainerURLFor(hostPort int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.URLOverrides[hostPort]; ok {
		return u
	}
	return fmt.Sprintf("http://127.0.0.1:%d", hostPort)
}

// PullImage implements dockerclient.Client.
func (f *FakeDocker) PullImage(ctx context.Context, ref, policy string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pullCallCount++
	if f.FailNext.Pull {
		f.FailNext.Pull = false
		return fmt.Errorf("fake pull failure")
	}
	f.images[ref] = true
	return nil
}

// ListOwned implements dockerclient.Client.
func (f *FakeDocker) ListOwned(ctx context.Context, executorID string) ([]dockerclient.ContainerRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []dockerclient.ContainerRef{}
	for _, c := range f.containers {
		if c.Labels[dockerclient.LabelExecutorID] == executorID {
			out = append(out, dockerclient.ContainerRef{ID: c.ID, Name: c.Name, Image: c.Image, Labels: c.Labels})
		}
	}
	return out, nil
}

// ListLeftover implements dockerclient.Client. The fake matches by the
// flowai.cleanup_id label, mirroring the production client behaviour.
func (f *FakeDocker) ListLeftover(ctx context.Context, cleanupID string) ([]dockerclient.ContainerRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []dockerclient.ContainerRef{}
	for _, c := range f.containers {
		if c.Labels[dockerclient.LabelCleanupID] == cleanupID {
			out = append(out, dockerclient.ContainerRef{ID: c.ID, Name: c.Name, Image: c.Image, Labels: c.Labels})
		}
	}
	return out, nil
}

// StartContainer implements dockerclient.Client.
func (f *FakeDocker) StartContainer(ctx context.Context, spec dockerclient.ContainerSpec) (*dockerclient.ContainerRef, error) {
	f.mu.Lock()
	if f.FailNext.Create {
		f.FailNext.Create = false
		f.mu.Unlock()
		return nil, fmt.Errorf("fake create failure")
	}
	if f.FailNext.Start {
		f.FailNext.Start = false
		f.mu.Unlock()
		return nil, fmt.Errorf("fake start failure")
	}
	id := fmt.Sprintf("cid-%d", len(f.containers)+1)
	var hostPort int
	if len(spec.Ports) > 0 {
		hostPort = spec.Ports[0].HostPort
	}
	c := &fakeContainer{ID: id, Name: spec.Name, Image: spec.Image, Labels: spec.Labels, Running: !f.KillOnStart, HostPort: hostPort}
	f.containers[id] = c
	started := dockerclient.ContainerRef{ID: id, Name: spec.Name, Image: spec.Image, Labels: spec.Labels}
	f.startedRefs = append(f.startedRefs, started)
	f.startedPorts = append(f.startedPorts, hostPort)
	f.mu.Unlock()
	if f.KillOnStart {
		// emit a die event so subscribers see the failure path.
		f.Events <- dockerclient.EventMessage{
			Type: "container", Action: "die",
			ActorID: id, ActorName: spec.Name, Time: time.Now(),
		}
	}
	return &started, nil
}

// StopContainer implements dockerclient.Client.
func (f *FakeDocker) StopContainer(ctx context.Context, containerID string, timeout time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailNext.Stop {
		f.FailNext.Stop = false
		return fmt.Errorf("fake stop failure")
	}
	c, ok := f.containers[containerID]
	if !ok {
		return fmt.Errorf("not found: %s", containerID)
	}
	c.Running = false
	delete(f.containers, containerID)
	return nil
}

// ForceKill implements dockerclient.Client.
func (f *FakeDocker) ForceKill(ctx context.Context, containerID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailNext.Kill {
		f.FailNext.Kill = false
		return fmt.Errorf("fake kill failure")
	}
	c, ok := f.containers[containerID]
	if !ok {
		return nil
	}
	c.Running = false
	delete(f.containers, containerID)
	return nil
}

// ContainerLogs implements dockerclient.Client. The fake blocks until ctx
// is canceled, then returns an EOF so the executor tears down the reader.
func (f *FakeDocker) ContainerLogs(ctx context.Context, containerID string) (io.ReadCloser, error) {
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		<-ctx.Done()
		_ = pw.CloseWithError(io.EOF)
	}()
	return pr, nil
}

// SubscribeEvents implements dockerclient.Client. Set
// `FailNext.EventsError` to push an error on the test-only errc
// channel (forcing the executor's subscribeDockerEvents to take the
// fatal-stream-loss branch). Set `FailNext.EventsCloseErr` to close
// both channels immediately.
func (f *FakeDocker) SubscribeEvents(ctx context.Context, filter dockerclient.EventFilter) (<-chan dockerclient.EventMessage, <-chan error) {
	errc := make(chan error, 1)
	if f.FailNext.EventsError != nil {
		errc <- f.FailNext.EventsError
	}
	if f.FailNext.EventsCloseErr {
		close(errc)
	}
	if f.FailNext.EventsCloseMsgs {
		// close the shared Events channel once
		f.closeEventsOnce()
	}
	return f.Events, errc
}

// closeEventsOnce closes f.Events the first time it is called.
func (f *FakeDocker) closeEventsOnce() {
	f.closeEventsMu.Do(func() {
		close(f.Events)
	})
}

// ContainerURL implements dockerclient.Client.
func (f *FakeDocker) ContainerURL(hostPort int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.URLOverrides[hostPort]; ok {
		return u
	}
	return fmt.Sprintf("http://127.0.0.1:%d", hostPort)
}

// PullCount returns how many times PullImage was called.
func (f *FakeDocker) PullCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pullCallCount
}

// StartedRefsSnapshot returns a copy of successfully started containers.
func (f *FakeDocker) StartedRefsSnapshot() []dockerclient.ContainerRef {
	f.mu.Lock()
	defer f.mu.Unlock()
	refs := make([]dockerclient.ContainerRef, len(f.startedRefs))
	copy(refs, f.startedRefs)
	return refs
}

// StartedPortsSnapshot returns the host ports for each successful
// StartContainer, in order. Tests use it to assert distinct allocations
// across concurrent tasks.
func (f *FakeDocker) StartedPortsSnapshot() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int, len(f.startedPorts))
	copy(out, f.startedPorts)
	return out
}

// EmitDie emits a die event for a container name (test helper).
func (f *FakeDocker) EmitDie(name string) {
	f.Events <- dockerclient.EventMessage{
		Type: "container", Action: "die",
		ActorID: name, ActorName: name, Time: time.Now(),
	}
}

// HasContainer reports whether the fake still tracks a container by ID.
func (f *FakeDocker) HasContainer(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.containers[id]
	return ok
}

// EmitHealthFail emits a die event simulating an unhealthy health-check (test helper).
func (f *FakeDocker) EmitHealthFail(name string) {
	f.Events <- dockerclient.EventMessage{
		Type: "container", Action: "health_status: unhealthy",
		ActorID: name, ActorName: name, Time: time.Now(),
	}
}

// Ensure compile-time interface conformance.
var _ dockerclient.Client = (*FakeDocker)(nil)
