// Package executor implements the v0001 Docker Executor.
//
// The Executor is a long-running Go process that:
//
//   - Generates a unique executor_id UID at startup.
//   - Registers the active Executor instance with the mocked State Registry.
//   - Cleans up any leftover containers from a previous run via Docker labels.
//   - Polls the mocked Router for queued matching tasks via GET /v1/tasks?filter=.
//   - For each queued task, starts one OpenHands container (up to capacity),
//     waits for it to be healthy, and submits the task prompt to OpenHands.
//   - Forwards OpenHands events, Docker lifecycle events, and Executor/task
//     lifecycle events to the mocked State Registry.
//   - Handles interrupt_task and append_task_message Router actions.
//   - Drains all owned containers on SIGTERM, with a configurable deadline
//     before force-kill.
//
// Capacity is bounded by EXECUTOR_MAX_CONTAINERS and the configured
// OPENHANDS_HOST_PORT_START/END port range.
package executor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/flowai/platform/executor-docker/internal/dockerclient"
	"github.com/flowai/platform/executor-docker/internal/logging"
	"github.com/flowai/platform/executor-docker/internal/mockedclient"
	"github.com/flowai/platform/executor-docker/internal/openhands"
	"github.com/flowai/platform/executor-docker/internal/platform"
)

// State is the Executor process state.
type State string

const (
	StateStarting   State = "starting"
	StateRegistering State = "registering"
	StateReady      State = "ready"
	StateBusy       State = "busy"
	StateDraining   State = "draining"
	StateStopped    State = "stopped"
)

// Config holds the full Executor configuration. It is built from a YAML file
// and overridden by environment variables.
type Config struct {
	ExecutorID           string        `yaml:"executor_id"` // optional, generated if empty
	ExecutorAPIBind      string        `yaml:"executor_api_bind"`
	RoutingTarget        string        `yaml:"routing_target"`
	MaxContainers        int           `yaml:"executor_max_containers"`
	DockerSocketPath     string        `yaml:"docker_socket_path"`
	OpenHandsImage       string        `yaml:"openhands_image"`
	OpenHandsPortStart   int           `yaml:"openhands_host_port_start"`
	OpenHandsPortEnd     int           `yaml:"openhands_host_port_end"`
	OpenHandsAPIKey      string        `yaml:"openhands_api_key"`
	OpenHandsInterruptEP string        `yaml:"openhands_interrupt_endpoint"`
	OpenHandsMessageEP   string        `yaml:"openhands_message_endpoint"`
	OpenHandsMessageType string        `yaml:"openhands_message_event_type"`
	OpenHandsInterruptTO time.Duration `yaml:"openhands_interrupt_timeout"`
	OpenHandsStartupTO   time.Duration `yaml:"openhands_startup_timeout"`
	OpenHandsDrainTO     time.Duration `yaml:"openhands_drain_timeout"`
	ImagePullPolicy      string        `yaml:"image_pull_policy"`
	LogLevel             string        `yaml:"log_level"`
	MockedServerURL      string        `yaml:"mocked_server_url"`
	EnvScopeToken        string        `yaml:"env_scope_token"`
	PollInterval         time.Duration `yaml:"poll_interval"`
}

// LoadConfig builds a Config from YAML + env vars. The YAML file may be
// empty/optional; missing values fall through to env defaults.
func LoadConfig(yamlPath string) (*Config, error) {
	cfg := &Config{
		ExecutorAPIBind:      "127.0.0.1:8020",
		RoutingTarget:        "openhands",
		MaxContainers:        2,
		DockerSocketPath:     "/var/run/docker.sock",
		OpenHandsImage:       "ghcr.io/openhands/agent-server:latest-python",
		OpenHandsPortStart:   18000,
		OpenHandsPortEnd:     18099,
		OpenHandsInterruptEP: "/api/conversations/{conversation_id}/pause",
		OpenHandsMessageEP:   "/api/conversations/{conversation_id}/events",
		OpenHandsMessageType: "message",
		OpenHandsInterruptTO: 15 * time.Second,
		OpenHandsStartupTO:   60 * time.Second,
		OpenHandsDrainTO:     30 * time.Second,
		ImagePullPolicy:      "if-not-present",
		LogLevel:             "info",
		MockedServerURL:      "http://127.0.0.1:8080/v1",
		EnvScopeToken:        "default-scope",
		PollInterval:         2 * time.Second,
	}
	if yamlPath != "" {
		// Best-effort YAML load; ignore errors when the file is missing.
		_ = loadYAML(yamlPath, cfg)
	}
	overrideEnv(cfg)
	if cfg.ExecutorID == "" {
		cfg.ExecutorID = "exec-" + uuid.NewString()
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func loadYAML(path string, cfg *Config) error {
	data, err := readFile(path)
	if err != nil {
		return err
	}
	return unmarshalYAML(data, cfg)
}

func overrideEnv(cfg *Config) {
	cfg.ExecutorAPIBind = envOr("EXECUTOR_API_BIND", cfg.ExecutorAPIBind)
	cfg.RoutingTarget = envOr("ROUTING_TARGET", cfg.RoutingTarget)
	cfg.MaxContainers = envInt("EXECUTOR_MAX_CONTAINERS", cfg.MaxContainers)
	cfg.DockerSocketPath = envOr("DOCKER_SOCKET_PATH", cfg.DockerSocketPath)
	cfg.OpenHandsImage = envOr("OPENHANDS_IMAGE", cfg.OpenHandsImage)
	cfg.OpenHandsPortStart = envInt("OPENHANDS_HOST_PORT_START", cfg.OpenHandsPortStart)
	cfg.OpenHandsPortEnd = envInt("OPENHANDS_HOST_PORT_END", cfg.OpenHandsPortEnd)
	cfg.OpenHandsAPIKey = envOr("OPENHANDS_API_KEY", cfg.OpenHandsAPIKey)
	cfg.OpenHandsInterruptEP = envOr("OPENHANDS_INTERRUPT_ENDPOINT", cfg.OpenHandsInterruptEP)
	cfg.OpenHandsMessageEP = envOr("OPENHANDS_MESSAGE_ENDPOINT", cfg.OpenHandsMessageEP)
	cfg.OpenHandsMessageType = envOr("OPENHANDS_MESSAGE_EVENT_TYPE", cfg.OpenHandsMessageType)
	cfg.OpenHandsInterruptTO = envDur("OPENHANDS_INTERRUPT_TIMEOUT_SECONDS", cfg.OpenHandsInterruptTO)
	cfg.OpenHandsStartupTO = envDur("OPENHANDS_STARTUP_TIMEOUT_SECONDS", cfg.OpenHandsStartupTO)
	cfg.OpenHandsDrainTO = envDur("OPENHANDS_DRAIN_TIMEOUT_SECONDS", cfg.OpenHandsDrainTO)
	cfg.ImagePullPolicy = envOr("IMAGE_PULL_POLICY", cfg.ImagePullPolicy)
	cfg.LogLevel = envOr("LOG_LEVEL", cfg.LogLevel)
	cfg.MockedServerURL = envOr("MOCKED_SERVER_URL", cfg.MockedServerURL)
	cfg.EnvScopeToken = envOr("ENV_SCOPE_TOKEN", cfg.EnvScopeToken)
	cfg.PollInterval = envDur("EXECUTOR_POLL_INTERVAL", cfg.PollInterval)
}

// Validate enforces invariants. Returns nil if all checks pass.
func (c *Config) Validate() error {
	if c.MaxContainers < 1 {
		return errors.New("EXECUTOR_MAX_CONTAINERS must be >= 1")
	}
	if c.OpenHandsPortEnd <= c.OpenHandsPortStart {
		return errors.New("OPENHANDS_HOST_PORT_END must be > OPENHANDS_HOST_PORT_START")
	}
	if (c.OpenHandsPortEnd - c.OpenHandsPortStart + 1) < c.MaxContainers {
		return fmt.Errorf("host port range size (%d) must be >= EXECUTOR_MAX_CONTAINERS (%d)",
			c.OpenHandsPortEnd-c.OpenHandsPortStart+1, c.MaxContainers)
	}
	if c.RoutingTarget == "" {
		return errors.New("ROUTING_TARGET must be non-empty")
	}
	return nil
}

// Executor is the long-running Docker Executor process.
type Executor struct {
	cfg      *Config
	logger   *slog.Logger
	docker   dockerclient.Client
	mocked   *mockedclient.Client
	registry *Registry

	// State machine
	state atomic.Value // State

	// Slot tracking
	mu      sync.Mutex
	slots   map[string]*taskSlot // task_id -> slot
	nextPort int
}

// taskSlot represents one running OpenHands container plus its FlowAI task.
type taskSlot struct {
	task          *platform.RouterTask
	containerID   string
	containerName string
	hostPort      int
	startedAt     time.Time

	// OpenHands runtime identifier (conversation/run id) when known.
	openHandsID string

	// Lifecycle channels
	cancel    context.CancelFunc
	doneCh    chan struct{}
	interrupt chan struct{}
}

// New constructs a new Executor.
func New(cfg *Config, docker dockerclient.Client, mocked *mockedclient.Client, logger *slog.Logger) *Executor {
	e := &Executor{
		cfg:      cfg,
		logger:   logger,
		docker:   docker,
		mocked:   mocked,
		registry: NewRegistry(),
		slots:    map[string]*taskSlot{},
	}
	e.nextPort = cfg.OpenHandsPortStart
	e.state.Store(StateStarting)
	return e
}

// State returns the current Executor state.
func (e *Executor) State() State { return e.state.Load().(State) }

func (e *Executor) setState(s State) {
	e.state.Store(s)
	e.logger.Info("state transition", "state", string(s))
}

// Run starts the Executor and blocks until ctx is canceled or a fatal error
// occurs. It owns the SIGTERM-driven graceful shutdown.
func (e *Executor) Run(ctx context.Context) error {
	if err := e.register(ctx); err != nil {
		e.setState(StateStopped)
		e.appendExecutorEventBestEffort(ctx, platform.ExecutorEventFailed, map[string]interface{}{"error": err.Error()})
		return fmt.Errorf("register: %w", err)
	}
	e.setState(StateReady)
	e.appendExecutorEventBestEffort(ctx, platform.ExecutorEventHealthy, nil)

	// Startup cleanup of any leftover containers.
	if err := e.cleanupLeftover(ctx); err != nil {
		e.logger.Warn("startup cleanup warning", "err", err.Error())
	}

	// Subscribe to Docker events for the lifetime of the Executor.
	go e.subscribeDockerEvents(ctx)

	// Main poll loop.
	err := e.pollLoop(ctx)
	// ctx canceled (SIGTERM) or fatal error -> drain.
	e.setState(StateDraining)
	e.appendExecutorEventBestEffort(ctx, platform.ExecutorEventStopping, nil)
	if drainErr := e.drain(ctx); drainErr != nil {
		e.logger.Error("drain error", "err", drainErr.Error())
	}
	e.setState(StateStopped)
	e.appendExecutorEventBestEffort(context.Background(), platform.ExecutorEventStopped, nil)
	return err
}

func (e *Executor) register(ctx context.Context) error {
	e.setState(StateRegistering)
	rec := &platform.ExecutorRecord{
		ExecutorID:        e.cfg.ExecutorID,
		ExecutorType:      platform.ExecutorTypeDockerOpenHands,
		RoutingTarget:     e.cfg.RoutingTarget,
		Capacity:          e.cfg.MaxContainers,
		RunningChildCount: 0,
		Metadata: map[string]interface{}{
			"openhands_image":             e.cfg.OpenHandsImage,
			"openhands_host_port_start":   e.cfg.OpenHandsPortStart,
			"openhands_host_port_end":     e.cfg.OpenHandsPortEnd,
			"version":                     "v0001",
		},
	}
	if _, err := e.mocked.RegisterExecutor(ctx, rec); err != nil {
		return err
	}
	e.appendExecutorEventBestEffort(ctx, platform.ExecutorEventRegistered, nil)
	return nil
}

// cleanupLeftover removes any leftover containers from a previous run.
func (e *Executor) cleanupLeftover(ctx context.Context) error {
	owned, err := e.docker.ListOwned(ctx, e.cfg.ExecutorID)
	if err != nil {
		return err
	}
	for _, c := range owned {
		if err := e.docker.ForceKill(ctx, c.ID); err != nil {
			e.logger.Warn("force-kill leftover failed", "container_id", c.ID, "err", err.Error())
		}
	}
	return nil
}

// pollLoop polls the mocked Router for matching tasks and starts containers
// up to capacity. It exits when ctx is canceled.
func (e *Executor) pollLoop(ctx context.Context) error {
	ticker := time.NewTicker(e.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := e.tick(ctx); err != nil {
				e.logger.Warn("poll tick error", "err", err.Error())
			}
		}
	}
}

// tick runs one poll iteration.
func (e *Executor) tick(ctx context.Context) error {
	tasks, err := e.mocked.ListTasks(ctx, e.cfg.RoutingTarget)
	if err != nil {
		return err
	}
	e.handlePendingActions(ctx, tasks)
	if e.freeSlots() == 0 {
		return nil
	}
	for i := range tasks {
		t := tasks[i]
		if t.Status != platform.TaskStatusQueued {
			continue
		}
		if e.slotCount() >= e.cfg.MaxContainers {
			break
		}
		if _, ok := e.getSlot(t.TaskID); ok {
			continue
		}
		if err := e.startTask(ctx, &t); err != nil {
			e.logger.Error("start task failed", "task_id", t.TaskID, "err", err.Error())
		}
	}
	return nil
}

// handlePendingActions processes interrupt_task and append_task_message
// actions returned on running tasks.
func (e *Executor) handlePendingActions(ctx context.Context, tasks []platform.RouterTask) {
	for i := range tasks {
		t := tasks[i]
		slot, ok := e.getSlot(t.TaskID)
		if !ok {
			continue
		}
		for _, raw := range t.PendingActions {
			switch raw.Type {
			case platform.ActionInterruptTask:
				e.handleInterrupt(ctx, slot)
			case platform.ActionAppendTaskMessage:
				if raw.Content != "" {
					e.handleAppendMessage(ctx, slot, raw.Content)
				}
			}
		}
	}
}

// startTask starts one OpenHands container for the queued task.
func (e *Executor) startTask(ctx context.Context, t *platform.RouterTask) error {
	port, err := e.allocatePort()
	if err != nil {
		return err
	}
	spec := dockerclient.ContainerSpec{
		Name:  "oh-" + shortID(t.TaskID),
		Image: e.cfg.OpenHandsImage,
		Env:   []string{},
		Labels: map[string]string{
			dockerclient.LabelExecutorID: e.cfg.ExecutorID,
			dockerclient.LabelRuntime:    dockerclient.RuntimeOpenHands,
			dockerclient.LabelTaskID:     t.TaskID,
		},
		Ports: []dockerclient.PortMapping{
			{HostPort: port, ContainerPort: 8000, Protocol: "tcp"},
		},
	}
	if err := e.docker.PullImage(ctx, spec.Image, e.cfg.ImagePullPolicy); err != nil {
		e.releasePort(port)
		e.appendTaskEventBestEffort(ctx, t.TaskID, platform.TaskSourceExecutor, "task.failed",
			map[string]interface{}{"phase": "image_pull", "error": err.Error()})
		return err
	}
	ref, err := e.docker.StartContainer(ctx, spec)
	if err != nil {
		e.releasePort(port)
		e.appendTaskEventBestEffort(ctx, t.TaskID, platform.TaskSourceExecutor, "task.failed",
			map[string]interface{}{"phase": "container_create", "error": err.Error()})
		return err
	}

	rctx, cancel := context.WithCancel(ctx)
	slot := &taskSlot{
		task:          t,
		containerID:   ref.ID,
		containerName: ref.Name,
		hostPort:      port,
		startedAt:     time.Now().UTC(),
		doneCh:        make(chan struct{}),
		interrupt:     make(chan struct{}, 1),
		cancel:        cancel,
	}
	e.addSlot(t.TaskID, slot)

	e.setBusyIfNotIdle()

	// Journal task.started before/alongside submission.
	e.appendTaskEventBestEffort(ctx, t.TaskID, platform.TaskSourceExecutor, "task.started",
		map[string]interface{}{"container_id": ref.ID, "container_name": ref.Name, "host_port": port})
	e.appendTaskEventBestEffort(ctx, t.TaskID, platform.TaskSourceExecutor, "task.start_message",
		map[string]interface{}{"prompt": t.Prompt})

	// Pull image if needed (best-effort; already pulled by spec.Image). We
	// rely on Docker to pull on create; nothing else to do here.
	_ = rctx

	// Run the per-task goroutine.
	go e.runTask(rctx, slot)

	return nil
}

// runTask is the per-task goroutine: wait for healthy, submit prompt, watch events.
func (e *Executor) runTask(ctx context.Context, slot *taskSlot) {
	defer close(slot.doneCh)
	defer e.removeSlot(slot.task.TaskID)

	addr := e.docker.ContainerURL(slot.hostPort)
	client := openhands.NewClient(addr, e.cfg.OpenHandsAPIKey, nil)

	// Wait for /health.
	if err := client.HealthCheck(ctx, e.cfg.OpenHandsStartupTO); err != nil {
		e.appendTaskEventBestEffort(ctx, slot.task.TaskID, platform.TaskSourceExecutor, "task.failed",
			map[string]interface{}{"phase": "health_check", "error": err.Error()})
		e.forceKillContainer(slot)
		return
	}

	// Submit the prompt.
	conv, err := client.StartConversation(ctx, slot.task.Prompt)
	if err != nil {
		e.appendTaskEventBestEffort(ctx, slot.task.TaskID, platform.TaskSourceExecutor, "task.failed",
			map[string]interface{}{"phase": "submit", "error": err.Error()})
		e.forceKillContainer(slot)
		return
	}
	slot.openHandsID = conv.ConversationID

	// Stream events from OpenHands logs (a stand-in for a real WS). The
	// v0001 contract is "Executor forwards each intermediate message/event
	// to the mocked State Registry without modification", so we tail the
	// container logs and append each line as an openhands.event.
	if err := e.tailOpenHandsLogs(ctx, slot, client); err != nil && !errors.Is(err, context.Canceled) {
		e.appendTaskEventBestEffort(ctx, slot.task.TaskID, platform.TaskSourceExecutor, "task.failed",
			map[string]interface{}{"phase": "stream", "error": err.Error()})
		e.forceKillContainer(slot)
		return
	}
}

// tailOpenHandsLogs reads OpenHands container logs and forwards each
// intermediate line as an openhands.event task event until the context is
// canceled or the container exits.
func (e *Executor) tailOpenHandsLogs(ctx context.Context, slot *taskSlot, _ *openhands.Client) error {
	rc, err := e.docker.ContainerLogs(ctx, slot.containerID)
	if err != nil {
		return err
	}
	defer rc.Close()
	buf := make([]byte, 4096)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, err := rc.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			e.appendTaskEventBestEffort(ctx, slot.task.TaskID, platform.TaskSourceOpenHands, "openhands.event",
				map[string]interface{}{"chunk": chunk, "openhands_conversation_id": slot.openHandsID})
		}
		if err != nil {
			return err
		}
	}
}

// handleInterrupt signals the task slot to interrupt.
func (e *Executor) handleInterrupt(ctx context.Context, slot *taskSlot) {
	select {
	case slot.interrupt <- struct{}{}:
	default:
	}
	e.appendTaskEventBestEffort(ctx, slot.task.TaskID, platform.TaskSourceExecutor, "task.cancel_requested", nil)
	go func() {
		tctx, cancel := context.WithTimeout(context.Background(), e.cfg.OpenHandsInterruptTO)
		defer cancel()
		addr := e.docker.ContainerURL(slot.hostPort)
		client := openhands.NewClient(addr, e.cfg.OpenHandsAPIKey, nil)
		convID := slot.openHandsID
		if convID == "" {
			convID = "unknown"
		}
		endpoint := stringsReplace(e.cfg.OpenHandsInterruptEP, "{conversation_id}", convID)
		if err := client.PauseConversation(tctx, convID); err != nil && endpoint != "" {
			e.logger.Warn("interrupt failed", "task_id", slot.task.TaskID, "err", err.Error())
			e.appendTaskEventBestEffort(context.Background(), slot.task.TaskID, platform.TaskSourceExecutor, "task.failed",
				map[string]interface{}{"phase": "interrupt", "error": err.Error()})
			e.forceKillContainer(slot)
			return
		}
		e.appendTaskEventBestEffort(context.Background(), slot.task.TaskID, platform.TaskSourceExecutor, "task.interrupted", nil)
	}()
}

// handleAppendMessage forwards a message to OpenHands and journals it.
func (e *Executor) handleAppendMessage(ctx context.Context, slot *taskSlot, content string) {
	addr := e.docker.ContainerURL(slot.hostPort)
	client := openhands.NewClient(addr, e.cfg.OpenHandsAPIKey, nil)
	convID := slot.openHandsID
	if convID == "" {
		convID = "unknown"
	}
	if err := client.AppendEvent(ctx, convID, e.cfg.OpenHandsMessageType, "user", content); err != nil {
		e.appendTaskEventBestEffort(ctx, slot.task.TaskID, platform.TaskSourceExecutor, "task.failed",
			map[string]interface{}{"phase": "append_message", "error": err.Error()})
		return
	}
	e.appendTaskEventBestEffort(ctx, slot.task.TaskID, platform.TaskSourceExecutor, "task.message_forwarded",
		map[string]interface{}{"content": content})
}

// drain stops accepting new tasks and removes all owned containers within
// the configured deadline. After the deadline, leftover containers are
// force-killed.
func (e *Executor) drain(ctx context.Context) error {
	deadline := time.Now().Add(e.cfg.OpenHandsDrainTO)
	e.mu.Lock()
	slots := make([]*taskSlot, 0, len(e.slots))
	for _, s := range e.slots {
		slots = append(slots, s)
	}
	e.mu.Unlock()

	for _, s := range slots {
		s.cancel()
	}
	for _, s := range slots {
		select {
		case <-s.doneCh:
			// graceful
		case <-time.After(time.Until(deadline)):
			if time.Now().After(deadline) {
				e.forceKillContainer(s)
			}
		}
		if err := e.docker.StopContainer(ctx, s.containerID, 5*time.Second); err != nil {
			e.logger.Warn("drain stop failed", "container_id", s.containerID, "err", err.Error())
			e.forceKillContainer(s)
		}
		e.appendTaskEventBestEffort(context.Background(), s.task.TaskID, platform.TaskSourceExecutor, "task.finished",
			map[string]interface{}{"reason": "executor_drained"})
	}
	return nil
}

func (e *Executor) forceKillContainer(slot *taskSlot) {
	if err := e.docker.ForceKill(context.Background(), slot.containerID); err != nil {
		e.logger.Warn("force-kill failed", "container_id", slot.containerID, "err", err.Error())
	}
}

// subscribeDockerEvents subscribes to container lifecycle events and
// forwards them as task events to the State Registry.
func (e *Executor) subscribeDockerEvents(ctx context.Context) {
	filter := map[string]string{
		"type":  "container",
		"label": dockerclient.LabelExecutorID + "=" + e.cfg.ExecutorID,
	}
	msgs, errs := e.docker.SubscribeEvents(ctx, filter)
	for {
		select {
		case <-ctx.Done():
			return
		case err := <-errs:
			if err != nil && !errors.Is(err, context.Canceled) {
				e.logger.Warn("docker events error", "err", err.Error())
			}
			return
		case m := <-msgs:
			taskID := m.ActorName
			if idx := indexOf(taskID, "-"); idx >= 0 {
				// best-effort: parse "oh-<taskid-short>" back to task_id; we
				// only need a non-empty label for now.
				taskID = m.ActorName
			}
			e.appendTaskEventBestEffort(ctx, taskID, platform.TaskSourceDocker,
				"docker.container."+m.Action,
				map[string]interface{}{"container_id": m.ActorID, "container_name": m.ActorName, "docker_action": m.Action, "at": m.Time})
			if m.Action == "die" {
				e.handleContainerDie(ctx, m.ActorID)
			}
		}
	}
}

// handleContainerDie processes an unexpected die event.
func (e *Executor) handleContainerDie(ctx context.Context, containerID string) {
	e.mu.Lock()
	var slot *taskSlot
	for _, s := range e.slots {
		if s.containerID == containerID {
			slot = s
			break
		}
	}
	e.mu.Unlock()
	if slot == nil {
		return
	}
	e.appendTaskEventBestEffort(ctx, slot.task.TaskID, platform.TaskSourceExecutor, "task.failed",
		map[string]interface{}{"reason": "container_died"})
	e.forceKillContainer(slot)
	if err := e.docker.StopContainer(ctx, containerID, 0); err != nil && !dockerclient.IsNotFound(err) {
		e.logger.Warn("post-die stop failed", "err", err.Error())
	}
}

func (e *Executor) appendExecutorEventBestEffort(ctx context.Context, t platform.ExecutorEventType, payload map[string]interface{}) {
	ev := mockedclient.NewExecutorEvent(e.cfg.ExecutorID, t, payload)
	if err := e.mocked.AppendExecutorEvent(ctx, ev); err != nil {
		e.logger.Warn("executor event append failed", "type", string(t), "err", err.Error())
	}
}

func (e *Executor) appendTaskEventBestEffort(ctx context.Context, taskID string, source platform.TaskEventSource, eventType string, payload map[string]interface{}) {
	if taskID == "" {
		return
	}
	ev := mockedclient.NewTaskEvent(taskID, e.cfg.ExecutorID, source, eventType, payload)
	if err := e.mocked.AppendTaskEvent(ctx, ev); err != nil {
		e.logger.Warn("task event append failed", "task_id", taskID, "type", eventType, "err", err.Error())
	}
}

func (e *Executor) setBusyIfNotIdle() {
	if e.slotCount() > 0 {
		e.setState(StateBusy)
	}
}

func (e *Executor) freeSlots() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg.MaxContainers - len(e.slots)
}

func (e *Executor) slotCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.slots)
}

func (e *Executor) addSlot(taskID string, slot *taskSlot) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.slots[taskID] = slot
}

func (e *Executor) removeSlot(taskID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.slots, taskID)
}

func (e *Executor) getSlot(taskID string) (*taskSlot, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.slots[taskID]
	return s, ok
}

func (e *Executor) allocatePort() (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for p := e.nextPort; p <= e.cfg.OpenHandsPortEnd; p++ {
		used := false
		for _, s := range e.slots {
			if s.hostPort == p {
				used = true
				break
			}
		}
		if !used {
			e.nextPort = p + 1
			return p, nil
		}
	}
	return 0, errors.New("no free host port in range")
}

func (e *Executor) releasePort(port int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if port < e.nextPort {
		e.nextPort = port
	}
}

// RunningChildCount returns the current number of running task containers.
func (e *Executor) RunningChildCount() int {
	return e.slotCount()
}

// Registry is a tiny dependency-injection container.
type Registry struct{}

// NewRegistry returns a Registry.
func NewRegistry() *Registry { return &Registry{} }

// stringsReplace is a minimal strings.ReplaceAll that avoids importing the
// strings package in this file (already used elsewhere but kept local for
// clarity). It's a tiny wrapper.
func stringsReplace(s, old, new string) string {
	return replaceAll(s, old, new)
}

// indexOf returns the first index of sep in s, or -1.
func indexOf(s, sep string) int {
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return i
		}
	}
	return -1
}

// shortID returns the first 8 chars of a UUID.
func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

// readFile is split out for tests.
func readFile(path string) ([]byte, error) {
	return readWholeFile(path)
}

// unmarshalYAML is split out for tests.
func unmarshalYAML(data []byte, v interface{}) error {
	return unmarshalYAMLData(data, v)
}

// envOr returns env value or fallback.
func envOr(k, fb string) string {
	if v, ok := lookupEnv(k); ok && v != "" {
		return v
	}
	return fb
}

// envInt returns env value as int or fallback.
func envInt(k string, fb int) int {
	if v, ok := lookupEnv(k); ok && v != "" {
		var n int
		_, err := fmt.Sscanf(v, "%d", &n)
		if err == nil {
			return n
		}
	}
	return fb
}

// envDur returns env value as duration (interpreted as seconds if no unit) or fallback.
func envDur(k string, fb time.Duration) time.Duration {
	if v, ok := lookupEnv(k); ok && v != "" {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d
		}
		// fall back to seconds.
		var s int
		if _, err := fmt.Sscanf(v, "%d", &s); err == nil {
			return time.Duration(s) * time.Second
		}
	}
	return fb
}

// FromContext returns the Executor from ctx, if any.
func FromContext(ctx context.Context) *Executor {
	if e, ok := ctx.Value(execKey{}).(*Executor); ok {
		return e
	}
	return nil
}

// WithContext returns ctx carrying e.
func WithContext(ctx context.Context, e *Executor) context.Context {
	return context.WithValue(ctx, execKey{}, e)
}

type execKey struct{}

// Logger returns the Executor logger.
func (e *Executor) Logger() *slog.Logger {
	if e.logger == nil {
		return logging.FromContext(context.Background())
	}
	return e.logger
}