// Package executor implements the v0001 Docker Executor.
//
// The Executor is a long-running Go process that:
//
//   - Generates a unique executor_id UID and a stable cleanup_id at startup.
//   - Removes leftover OpenHands containers from previous runs (matched by
//     cleanup_id) BEFORE registering with the State Registry.
//   - Registers the active Executor instance with the mocked State Registry.
//   - Polls the mocked Router for queued matching tasks via
//     GET /v1/tasks?filter=<routing_target>.
//   - For each queued task, resolves env values from the mocked Env
//     Registry, starts one OpenHands container (up to capacity), waits for
//     it to be healthy, and submits the task prompt to OpenHands.
//   - Subscribes to the official OpenHands agent-server WebSocket event
//     stream and forwards each intermediate and terminal event to the
//     mocked State Registry without modification.
//   - Handles interrupt_task and append_task_message Router actions via the
//     same unified interrupt path that SIGTERM uses during busy state.
//   - Drains all owned containers on SIGTERM, with a configurable deadline
//     before force-kill.
//
// Container cleanup is centralised on the slot via a single
// sync.Once-guarded method so every error path (normal terminal,
// successful interrupt, timeout, health failure, submit failure,
// stream failure, container_died, SIGTERM) removes or force-kills the
// container exactly once before releasing the slot/port.
//
// Capacity is bounded by EXECUTOR_MAX_CONTAINERS and the configured
// OPENHANDS_HOST_PORT_START/END port range.
package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/flowai/platform/executor_docker_opehands/internal/dockerclient"
	"github.com/flowai/platform/executor_docker_opehands/internal/logging"
	"github.com/flowai/platform/executor_docker_opehands/internal/mockedclient"
	"github.com/flowai/platform/executor_docker_opehands/internal/openhands"
	"github.com/flowai/platform/executor_docker_opehands/internal/platform"
)

// State is the Executor process state.
type State string

const (
	StateStarting    State = "starting"
	StateRegistering State = "registering"
	StateReady       State = "ready"
	StateBusy        State = "busy"
	StateStopping    State = "stopping"
	StateStopped     State = "stopped"
	StateFailed      State = "failed"
)

// Config holds the full Executor configuration. It is built from a YAML
// file and overridden by environment variables.
type Config struct {
	ExecutorID           string        `yaml:"executor_id"`
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
	WebSocketDialTimeout time.Duration `yaml:"websocket_dial_timeout"`

	// OpenHands V1 conversation startup.
	OpenHandsWorkspace    string `yaml:"openhands_workspace"`
	OpenHandsLLMModel     string `yaml:"openhands_llm_model"`
	OpenHandsLLMAPIKey    string `yaml:"openhands_llm_api_key"`
	OpenHandsLLMBaseURL   string `yaml:"openhands_llm_base_url"`
	OpenHandsLLMUsageID   string `yaml:"openhands_llm_usage_id"`
	OpenHandsAgentProfile string `yaml:"openhands_agent_profile_id"`
	OpenHandsInitialRun   bool   `yaml:"openhands_initial_run"`
}

// LoadConfig builds a Config from YAML + env vars.
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
		WebSocketDialTimeout: 5 * time.Second,
		// V1 conversation startup defaults.
		OpenHandsWorkspace:  "/workspace/project",
		OpenHandsLLMUsageID: "flowai-executor",
		OpenHandsInitialRun: true,
	}
	if yamlPath != "" {
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
	cfg.WebSocketDialTimeout = envDur("OPENHANDS_WS_DIAL_TIMEOUT_SECONDS", cfg.WebSocketDialTimeout)
	// V1 conversation startup env.
	cfg.OpenHandsWorkspace = envOr("OPENHANDS_WORKSPACE", cfg.OpenHandsWorkspace)
	cfg.OpenHandsLLMModel = envOr("OPENHANDS_LLM_MODEL", cfg.OpenHandsLLMModel)
	cfg.OpenHandsLLMAPIKey = envOr("OPENHANDS_LLM_API_KEY", cfg.OpenHandsLLMAPIKey)
	cfg.OpenHandsLLMBaseURL = envOr("OPENHANDS_LLM_BASE_URL", cfg.OpenHandsLLMBaseURL)
	cfg.OpenHandsLLMUsageID = envOr("OPENHANDS_LLM_USAGE_ID", cfg.OpenHandsLLMUsageID)
	cfg.OpenHandsAgentProfile = envOr("OPENHANDS_AGENT_PROFILE_ID", cfg.OpenHandsAgentProfile)
	cfg.OpenHandsInitialRun = envBool("OPENHANDS_INITIAL_RUN", cfg.OpenHandsInitialRun)
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
	if c.WebSocketDialTimeout <= 0 {
		return errors.New("OPENHANDS_WS_DIAL_TIMEOUT_SECONDS must be > 0")
	}
	if c.OpenHandsInterruptTO <= 0 {
		return errors.New("OPENHANDS_INTERRUPT_TIMEOUT_SECONDS must be > 0")
	}
	if c.OpenHandsStartupTO <= 0 {
		return errors.New("OPENHANDS_STARTUP_TIMEOUT_SECONDS must be > 0")
	}
	if c.OpenHandsDrainTO <= 0 {
		return errors.New("OPENHANDS_DRAIN_TIMEOUT_SECONDS must be > 0")
	}
	if c.OpenHandsWorkspace == "" {
		return errors.New("OPENHANDS_WORKSPACE must be non-empty")
	}
	// V1 conversation startup requires either a server-side agent
	// profile id OR an inline model + api key + usage_id. Profiles
	// resolve server-side to a fully configured Agent.
	hasProfile := c.OpenHandsAgentProfile != ""
	hasInline := c.OpenHandsLLMModel != "" && c.OpenHandsLLMAPIKey != "" && c.OpenHandsLLMUsageID != ""
	if !hasProfile && !hasInline {
		return errors.New("OpenHands V1 requires either OPENHANDS_AGENT_PROFILE_ID " +
			"or (OPENHANDS_LLM_MODEL + OPENHANDS_LLM_API_KEY + OPENHANDS_LLM_USAGE_ID)")
	}
	return nil
}

// CleanupIDPath returns the configured path for the stable cleanup identity.
func (c *Config) CleanupIDPath() string { return CleanupIDPath() }

// OpenHandsWSURL returns the absolute WebSocket events-socket URL for
// the container serving conversationID. The V1 agent-server exposes
// the event stream at WS /sockets/events/{id}.
func OpenHandsWSURL(containerURL, conversationID string) string {
	base := strings.TrimRight(containerURL, "/")
	if strings.HasPrefix(base, "https://") {
		base = "wss://" + strings.TrimPrefix(base, "https://")
	} else if strings.HasPrefix(base, "http://") {
		base = "ws://" + strings.TrimPrefix(base, "http://")
	}
	return base + "/sockets/events/" + conversationID
}

// Executor is the long-running Docker Executor process.
type Executor struct {
	cfg     *Config
	logger  *slog.Logger
	docker  dockerclient.Client
	mocked  *mockedclient.Client
	cleanup string

	state atomic.Value // State

	// runCancel cancels the polling/streaming lifecycle (set in Run()).
	// Docker supervision fatal errors call it to drive Run() to a
	// draining -> stopped transition with a non-nil fatal error.
	runCancel context.CancelFunc
	// fatalCh is closed once when Docker supervision is lost. pollLoop
	// selects on this so a closed Docker event stream stops new task
	// acceptance before Run() drains.
	fatalCh   chan struct{}
	fatalOnce sync.Once
	// fatalErr stores the originating fatal phase for Run() to return.
	fatalErr atomic.Pointer[string]

	mu          sync.Mutex
	slots       map[string]*taskSlot // task_id -> slot
	nextPort    int
	lastRunning int // last reported running_child_count for registration

	// acceptedTasks tracks every task_id this Executor process accepted
	// (slot created) or terminalized, bounded to capacity plus per-slot
	// post-terminal lifetime.
	acceptedTasks map[string]taskLifecycle

	// pendingActionSeen indexes action_ids per task_id for O(1)
	// dedupe. The inner map is removed when the slot is terminalized,
	// bounding memory to the task's active lifetime.
	pendingActionSeen map[string]map[actionID]struct{}

	stateOK     atomic.Bool
	openHandsOK atomic.Bool

	// muCounts guards lastRunning; readers take it briefly to read or
	// detect a counter change after add/remove slot mutations.
	muCounts sync.Mutex
}

type actionID = string

type taskLifecycle uint8

const (
	taskLifecycleNone taskLifecycle = iota
	taskLifecycleRunning
	taskLifecycleTerminal
)

// taskSlot represents one running OpenHands container plus its FlowAI
// task. terminalMu serialises terminal-event emission; cleanupOnce
// guarantees the container is removed exactly once across every
// error path; doneOnce/exitOnce serialise done/exit channel close;
// pendingActions guards the per-slot dedupe map.
type taskSlot struct {
	task          *platform.RouterTask
	containerID   string
	containerName string
	hostPort      int
	startedAt     time.Time

	openHandsMu  sync.RWMutex
	openHandsID  string
	openHandsURL string

	terminalMu    sync.Mutex
	terminal      bool
	terminalCause string // "stream", "interrupted", "timeout", "completed", "container_died", ...

	cleanupOnce sync.Once

	cancel   context.CancelFunc
	doneCh   chan struct{}
	doneOnce sync.Once
	exitCh   chan struct{}
	exitOnce sync.Once

	// per-slot dedupe; removed by terminal cleanup.
	pendingActions map[actionID]struct{}
	pendingMu      sync.Mutex
}

// cleanup idempotently stops+removes the container for this slot. It
// is safe to call from any goroutine and at most one stop+remove pair
// is sent per slot. The supplied context is bounded by the caller; a
// fallback bounded context backs a force-kill if stop fails. Never
// uses an unbounded background context.
func (s *taskSlot) cleanup(ctx context.Context, stopFn func(context.Context, string, time.Duration) error, killFn func(context.Context, string) error) {
	s.cleanupOnce.Do(func() {
		// A short Docker stop is usually enough; if the in-container V1
		// server is hung (a known failure mode), the stop ctx times out
		// and we fall back to SIGKILL.
		sctx, scancel := context.WithTimeout(ctx, 3*time.Second)
		defer scancel()
		if err := stopFn(sctx, s.containerID, 2*time.Second); err == nil {
			return
		}
		kctx, kcancel := context.WithTimeout(ctx, 2*time.Second)
		defer kcancel()
		_ = killFn(kctx, s.containerID)
	})
}

func (s *taskSlot) signalDone() { s.doneOnce.Do(func() { close(s.doneCh) }) }
func (s *taskSlot) signalExit() { s.exitOnce.Do(func() { close(s.exitCh) }) }
func (s *taskSlot) setOpenHandsID(id, baseURL string) {
	s.openHandsMu.Lock()
	defer s.openHandsMu.Unlock()
	s.openHandsID = id
	s.openHandsURL = baseURL
}
func (s *taskSlot) getOpenHandsID() string {
	s.openHandsMu.RLock()
	defer s.openHandsMu.RUnlock()
	return s.openHandsID
}
func (s *taskSlot) getOpenHandsURL() string {
	s.openHandsMu.RLock()
	defer s.openHandsMu.RUnlock()
	return s.openHandsURL
}

// claimTerminal returns true exactly once for the lifetime of the slot.
func (s *taskSlot) claimTerminal() bool {
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	if s.terminal {
		return false
	}
	s.terminal = true
	return true
}

// isTerminal reports whether a terminal event has already been recorded.
func (s *taskSlot) isTerminal() bool {
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	return s.terminal
}

// markPending records that an action_id was applied to this slot.
// Returns false if the action_id was already applied (so callers skip
// forwarding it).
func (s *taskSlot) markPending(id actionID) bool {
	if id == "" {
		return true
	}
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if s.pendingActions == nil {
		s.pendingActions = map[actionID]struct{}{}
	}
	if _, ok := s.pendingActions[id]; ok {
		return false
	}
	s.pendingActions[id] = struct{}{}
	return true
}

// dropPending removes the per-slot dedupe map to bound memory. Called
// from terminalizeTask so the map cannot outlive the slot's active
// lifecycle.
func (s *taskSlot) dropPending() {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	s.pendingActions = nil
}

// New constructs a new Executor.
func New(cfg *Config, docker dockerclient.Client, mocked *mockedclient.Client, logger *slog.Logger) *Executor {
	e := &Executor{
		cfg:               cfg,
		logger:            logger,
		docker:            docker,
		mocked:            mocked,
		slots:             map[string]*taskSlot{},
		nextPort:          cfg.OpenHandsPortStart,
		acceptedTasks:     map[string]taskLifecycle{},
		pendingActionSeen: map[string]map[actionID]struct{}{},
		fatalCh:           make(chan struct{}),
	}
	e.state.Store(StateStarting)
	return e
}

func (e *Executor) State() State { return e.state.Load().(State) }

func (e *Executor) IsStateRegistryRegistered() bool { return e.stateOK.Load() }
func (e *Executor) IsOpenHandsReachable() bool      { return e.openHandsOK.Load() }

func (e *Executor) setState(s State) {
	e.state.Store(s)
	e.logger.Info("state transition", "state", string(s))
}

// CleanupID returns the stable cleanup identity used to scope leftover
// containers from previous runs.
func (e *Executor) CleanupID() string { return e.cleanup }

// Run starts the Executor and blocks until ctx is canceled, the
// process is told to drain (SIGTERM), or a fatal supervision error
// occurs. When ctx is canceled or a fatal event fires, drain runs on
// a fresh bounded context so the OpenHands pause / Docker stop calls
// continue to work.
func (e *Executor) Run(ctx context.Context) error {
	cleanupID, err := loadOrCreateCleanupID(e.cfg.CleanupIDPath())
	if err != nil {
		e.setState(StateStopped)
		e.appendExecutorEventBestEffort(context.Background(), platform.ExecutorEventFailed,
			map[string]interface{}{"error": err.Error(), "phase": "cleanup_id"})
		return fmt.Errorf("cleanup id: %w", err)
	}
	e.cleanup = cleanupID

	if err := e.cleanupLeftover(ctx); err != nil {
		e.logger.Warn("startup cleanup warning", "err", err.Error())
	}

	if err := e.register(ctx); err != nil {
		e.setState(StateStopped)
		e.appendExecutorEventBestEffort(context.Background(), platform.ExecutorEventFailed,
			map[string]interface{}{"error": err.Error(), "phase": "register"})
		return fmt.Errorf("register: %w", err)
	}
	e.stateOK.Store(true)
	e.setState(StateReady)
	e.appendExecutorEventBestEffort(context.Background(), platform.ExecutorEventHealthy, nil)

	// Owned run context: cancellation drives Run() to drain. The
	// pollLoop also listens on fatalCh so a Docker stream loss can
	// exit the polling loop without waiting for the outer ctx.
	runCtx, runCancel := context.WithCancel(ctx)
	e.runCancel = runCancel
	defer runCancel()

	dockerCtx, dockerCancel := context.WithCancel(runCtx)
	go e.subscribeDockerEvents(dockerCtx)
	defer dockerCancel()

	pollErr := e.pollLoop(runCtx)
	e.setState(StateStopping)
	e.appendExecutorEventBestEffort(context.Background(), platform.ExecutorEventStopping, nil)

	drainCtx, drainCancel := context.WithTimeout(context.Background(), e.cfg.OpenHandsDrainTO+5*time.Second)
	e.drain(drainCtx)
	drainCancel()

	e.setState(StateStopped)
	e.appendExecutorEventBestEffort(context.Background(), platform.ExecutorEventStopped, nil)

	// Return fatal supervision error if one fired; otherwise the
	// polling-loop return (nil for graceful, err for explicit cancel).
	if msg := e.fatalErr.Load(); msg != nil {
		return fmt.Errorf("fatal: %s", *msg)
	}
	return pollErr
}

// markFatal records a fatal supervision event. Closes fatalCh exactly
// once, captures the originating phase, cancels the run-owned context
// (so pollLoop returns and Run() reaches drain), and transitions the
// state to non-ready.
func (e *Executor) markFatal(phase string) {
	e.fatalOnce.Do(func() {
		e.fatalErr.Store(&phase)
		e.appendExecutorEventBestEffort(context.Background(), platform.ExecutorEventFailed,
			map[string]interface{}{"phase": phase})
		e.setState(StateFailed)
		if e.runCancel != nil {
			e.runCancel()
		}
		close(e.fatalCh)
	})
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
			"openhands_image":           e.cfg.OpenHandsImage,
			"openhands_host_port_start": e.cfg.OpenHandsPortStart,
			"openhands_host_port_end":   e.cfg.OpenHandsPortEnd,
			"cleanup_id":                e.cleanup,
			"version":                   "v0001",
		},
	}
	if _, err := e.mocked.RegisterExecutor(ctx, rec); err != nil {
		return err
	}
	e.appendExecutorEventBestEffort(ctx, platform.ExecutorEventRegistered, nil)
	return nil
}

func (e *Executor) cleanupLeftover(ctx context.Context) error {
	owned, err := e.docker.ListLeftover(ctx, e.cleanup)
	if err != nil {
		return err
	}
	for _, c := range owned {
		if err := e.docker.ForceKill(ctx, c.ID); err != nil {
			e.logger.Warn("force-kill leftover failed", "container_id", c.ID, "err", err.Error())
		}
	}
	if len(owned) > 0 {
		e.appendExecutorEventBestEffort(ctx, platform.ExecutorEventStarted,
			map[string]interface{}{"phase": "startup_cleanup", "removed": len(owned), "cleanup_id": e.cleanup})
	}
	return nil
}

// pollLoop polls the mocked Router for matching tasks and starts
// containers up to capacity. It exits when ctx is canceled OR the
// Docker event stream suffers a fatal loss (fatalCh closes).
func (e *Executor) pollLoop(ctx context.Context) error {
	ticker := time.NewTicker(e.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-e.fatalCh:
			return errors.New("docker event stream lost")
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
	free := e.freeSlots()
	if free == 0 {
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
		if e.taskLifecycle(t.TaskID) != taskLifecycleNone {
			continue
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

func (e *Executor) taskLifecycle(taskID string) taskLifecycle {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.acceptedTasks[taskID]
}

func (e *Executor) markAccepted(taskID string, stage taskLifecycle) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.acceptedTasks[taskID] = stage
}

// handlePendingActions dedupes Router pending actions per task_id and
// dispatches the remaining entries to handleInterrupt /
// handleAppendMessage.
func (e *Executor) handlePendingActions(ctx context.Context, tasks []platform.RouterTask) {
	for i := range tasks {
		t := tasks[i]
		slot, ok := e.getSlot(t.TaskID)
		if !ok {
			continue
		}
		for _, raw := range t.PendingActions {
			if !slot.markPending(raw.ActionID) {
				continue
			}
			e.logger.Info("handlePendingActions: action",
				"task_id", t.TaskID, "type", raw.Type, "action_id", raw.ActionID)
			switch raw.Type {
			case platform.ActionInterruptTask:
				e.handleInterrupt(slot, interruptReasonRouter)
			case platform.ActionAppendTaskMessage:
				if raw.Content != "" {
					e.handleAppendMessage(ctx, slot, raw.Content)
				}
			}
		}
	}
}

// startTask starts one OpenHands container for the queued task.
// Pre-slot failures (env_open, image_pull, container_create) record
// task.failed directly without claiming terminal because the slot has
// not been installed in e.slots yet.
func (e *Executor) startTask(ctx context.Context, t *platform.RouterTask) error {
	e.mu.Lock()
	switch e.acceptedTasks[t.TaskID] {
	case taskLifecycleRunning, taskLifecycleTerminal:
		e.mu.Unlock()
		return nil
	default:
		e.acceptedTasks[t.TaskID] = taskLifecycleRunning
	}
	e.mu.Unlock()

	port, err := e.allocatePort()
	if err != nil {
		e.markAccepted(t.TaskID, taskLifecycleTerminal)
		e.removeFromAccepted(t.TaskID)
		return err
	}
	env, err := e.openEnv(ctx, t.TaskID)
	if err != nil {
		e.releasePort(port)
		e.appendTaskEventBestEffort(ctx, t.TaskID, platform.TaskSourceExecutor, "task.failed",
			map[string]interface{}{"phase": "env_open", "error": err.Error()})
		e.markAccepted(t.TaskID, taskLifecycleTerminal)
		e.removeFromAccepted(t.TaskID)
		return err
	}
	spec := dockerclient.ContainerSpec{
		Name:  "oh-" + shortID(t.TaskID),
		Image: e.cfg.OpenHandsImage,
		Env:   envToDockerEnv(env),
		Labels: map[string]string{
			dockerclient.LabelExecutorID: e.cfg.ExecutorID,
			dockerclient.LabelCleanupID:  e.cleanup,
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
		e.markAccepted(t.TaskID, taskLifecycleTerminal)
		e.removeFromAccepted(t.TaskID)
		return err
	}
	ref, err := e.docker.StartContainer(ctx, spec)
	if err != nil {
		e.releasePort(port)
		e.appendTaskEventBestEffort(ctx, t.TaskID, platform.TaskSourceExecutor, "task.failed",
			map[string]interface{}{"phase": "container_create", "error": err.Error()})
		e.markAccepted(t.TaskID, taskLifecycleTerminal)
		e.removeFromAccepted(t.TaskID)
		return err
	}

	rctx, cancel := context.WithCancel(ctx)
	slot := &taskSlot{
		task:           t,
		containerID:    ref.ID,
		containerName:  ref.Name,
		hostPort:       port,
		startedAt:      time.Now().UTC(),
		doneCh:         make(chan struct{}),
		exitCh:         make(chan struct{}),
		cancel:         cancel,
		pendingActions: map[actionID]struct{}{},
	}
	e.addSlot(t.TaskID, slot)
	e.logger.Info("container started",
		"task_id", t.TaskID, "container_id", ref.ID, "host_port", port, "slot_count", e.slotCount())

	// Per-change registration refresh: every slot-count delta triggers
	// a PUT so the State Registry's running_child_count stays aligned
	// with the actual executor.busy / executor.idle events.
	e.observeSlotCount()

	e.appendTaskEventBestEffort(ctx, t.TaskID, platform.TaskSourceExecutor, "task.started",
		map[string]interface{}{
			"container_id":   ref.ID,
			"container_name": ref.Name,
			"host_port":      port,
			"env_values":     envCount(env),
		})
	e.appendTaskEventBestEffort(ctx, t.TaskID, platform.TaskSourceExecutor, "task.start_message",
		map[string]interface{}{"prompt": t.Prompt})

	go e.runTask(rctx, slot)
	return nil
}

// removeFromAccepted removes a never-slotted task from acceptedTasks to
// bound the map after a pre-slot failure.
func (e *Executor) removeFromAccepted(taskID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if cur, ok := e.acceptedTasks[taskID]; ok && cur == taskLifecycleTerminal {
		delete(e.acceptedTasks, taskID)
	}
}

func (e *Executor) openEnv(ctx context.Context, taskID string) (map[string]string, error) {
	values, err := e.mocked.OpenEnv(ctx, e.cfg.ExecutorID, e.cfg.RoutingTarget, taskID, e.cfg.EnvScopeToken)
	if err != nil {
		return nil, err
	}
	if values == nil {
		return map[string]string{}, nil
	}
	return values, nil
}

func envToDockerEnv(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(values))
	for _, k := range keys {
		out = append(out, k+"="+values[k])
	}
	return out
}

func envCount(values map[string]string) int {
	if values == nil {
		return 0
	}
	return len(values)
}

// runTask is the per-task goroutine: wait for healthy, submit prompt,
// stream events until terminal state. Deferred ordering guarantees:
//  1. exitCh is closed last so drain can join after all defers.
//  2. removeSlot runs before setReadyIfLast so the slot count check
//     observes the post-removal map (TOCTOU-safe).
//  3. setReadyIfLast publishes executor.idle and refreshes registration
//     only when THIS slot is the terminal-cause of the count going
//     to zero.
//  4. cleanupContainer is the last authoritative step: it runs
//     exactly once thanks to sync.Once and uses bounded contexts.
func (e *Executor) runTask(ctx context.Context, slot *taskSlot) {
	defer slot.signalExit()
	defer slot.signalDone()
	defer e.cleanupContainer(slot) // bounded ctx closure
	defer e.setReadyIfLast()
	defer e.removeSlot(slot.task.TaskID)
	defer e.terminalizeTask(slot)

	addr := e.docker.ContainerURL(slot.hostPort)
	client := openhands.NewClient(addr, e.cfg.OpenHandsAPIKey, nil)

	if err := client.HealthCheck(ctx, e.cfg.OpenHandsStartupTO); err != nil {
		e.failSlot(slot, "health_check", err.Error())
		return
	}
	e.openHandsOK.Store(true)

	convCfg := e.conversationConfig(slot.task.Prompt)
	conv, err := client.StartConversation(ctx, convCfg)
	if err != nil {
		e.failSlot(slot, "submit", err.Error())
		return
	}
	slot.setOpenHandsID(conv.ID, addr)
	// Real-runtime signal: the conversation was successfully created on
	// the V1 agent-server. Tests and operators can subscribe to this
	// event instead of polling task status (which the mocked router
	// never mutates).
	e.appendTaskEventBestEffort(context.Background(), slot.task.TaskID,
		platform.TaskSourceExecutor, "openhands.conversation_started",
		map[string]interface{}{
			"openhands_conversation_id": conv.ID,
		})

	if err := e.streamOpenHandsEvents(ctx, slot, conv.ID); err != nil {
		if slot.claimTerminal() {
			slot.terminalCause = "stream"
			e.appendTaskEventBestEffort(context.Background(), slot.task.TaskID, platform.TaskSourceExecutor, "task.failed",
				map[string]interface{}{"phase": "stream", "error": err.Error()})
		}
		return
	}
}

// conversationConfig renders the V1 conversation request body from the
// Executor Config. Exactly one of inline Agent and AgentProfileID is
// set per the V1 schema; Config.Validate() rejects configs that set
// neither.
func (e *Executor) conversationConfig(prompt string) openhands.ConversationConfig {
	cfg := openhands.ConversationConfig{
		Workspace: openhands.Workspace{
			Kind:       "LocalWorkspace",
			WorkingDir: e.cfg.OpenHandsWorkspace,
		},
		InitialMessage: openhands.InitialMessage{
			Role: "user",
			Content: []openhands.ContentPart{
				{Type: "text", Text: prompt},
			},
			Run: e.cfg.OpenHandsInitialRun,
		},
	}
	if e.cfg.OpenHandsAgentProfile != "" {
		cfg.AgentProfileID = e.cfg.OpenHandsAgentProfile
		return cfg
	}
	cfg.Agent = &openhands.Agent{
		Kind: "Agent",
		LLM: openhands.LLM{
			Model:   e.cfg.OpenHandsLLMModel,
			APIKey:  e.cfg.OpenHandsLLMAPIKey,
			UsageID: e.cfg.OpenHandsLLMUsageID,
			BaseURL: e.cfg.OpenHandsLLMBaseURL,
		},
	}
	return cfg
}

// failSlot records task.failed for post-slot terminalisation. The
// caller provides a cause ("health_check" / "submit") and the slot's
// defer chain handles cleanupContainer.
func (e *Executor) failSlot(slot *taskSlot, phase, errMsg string) {
	if slot.claimTerminal() {
		slot.terminalCause = phase
		e.appendTaskEventBestEffort(context.Background(), slot.task.TaskID, platform.TaskSourceExecutor, "task.failed",
			map[string]interface{}{"phase": phase, "error": errMsg})
	}
}

// streamOpenHandsEvents subscribes to the OpenHands agent-server
// WebSocket event stream and forwards each frame to the mocked State
// Registry. Terminal status (FINISHED / FAILED / ERROR / STUCK /
// PAUSED) causes the slot to claim terminal exactly once, journal
// the platform terminal event, and unwind.
func (e *Executor) streamOpenHandsEvents(ctx context.Context, slot *taskSlot, conversationID string) error {
	containerURL := slot.getOpenHandsURL()
	if containerURL == "" {
		containerURL = e.docker.ContainerURL(slot.hostPort)
	}
	wsURL := OpenHandsWSURL(containerURL, conversationID)

	dialCtx, cancel := context.WithTimeout(ctx, e.cfg.WebSocketDialTimeout)
	defer cancel()

	dialer := websocket.Dialer{
		HandshakeTimeout: e.cfg.WebSocketDialTimeout,
	}
	headers := httpAPIHeaders(e.cfg.OpenHandsAPIKey)
	var (
		conn *websocket.Conn
		resp *http.Response
		err  error
	)
	for attempt := 0; attempt < 5; attempt++ {
		conn, resp, err = dialer.DialContext(dialCtx, wsURL, headers)
		if err == nil {
			break
		}
		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err != nil {
		return fmt.Errorf("dial %s: %w", wsURL, err)
	}
	e.logger.Info("websocket connected",
		"task_id", slot.task.TaskID, "conv_id", conversationID)
	defer conn.Close()

	// Cancel-driven cancel: a small goroutine closes the WS as soon as
	// ctx is done, unblocking the blocking ReadMessage below. The
	// `done` channel signals when the goroutine has exited so the caller
	// never leaks it. sync.Once protects the close from racing with
	// the goroutine's natural exit.
	done := make(chan struct{})
	var doneOnce sync.Once
	closeDone := func() { doneOnce.Do(func() { close(done) }) }
	go func() {
		defer closeDone()
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	// Wake the goroutine if it is still parked on ctx.Done(); it will
	// close `done` exactly once via closeDone and exit.
	defer closeDone()

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			e.logger.Warn("websocket read error",
				"task_id", slot.task.TaskID, "err", err.Error(), "ctx_err", ctx.Err())
			return err
		}
		raw := json.RawMessage(data)
		var env openhandsEventEnvelope
		if jerr := json.Unmarshal(data, &env); jerr != nil {
			env.Raw = raw
		} else {
			env.Raw = raw
		}
		e.appendTaskEventBestEffort(context.Background(), slot.task.TaskID, platform.TaskSourceOpenHands, "openhands.event",
			map[string]interface{}{
				"openhands_conversation_id": conversationID,
				"raw":                       env.Raw,
			})
		if !isTerminalTaskStatus(env.TaskStatus()) {
			continue
		}
		status := env.TaskStatus()
		if slot.claimTerminal() {
			slot.terminalCause = "terminal_state:" + status
			switch status {
			case "paused":
				e.appendTaskEventBestEffort(context.Background(), slot.task.TaskID, platform.TaskSourceExecutor, "task.interrupted",
					map[string]interface{}{"openhands_status": "paused"})
			case "finished":
				e.appendTaskEventBestEffort(context.Background(), slot.task.TaskID, platform.TaskSourceExecutor, "task.finished", nil)
			case "failed", "error", "stuck":
				e.appendTaskEventBestEffort(context.Background(), slot.task.TaskID, platform.TaskSourceExecutor, "task.failed",
					map[string]interface{}{"phase": "terminal_state", "openhands_status": status})
			}
		}
		return nil
	}
}

type openhandsEventEnvelope struct {
	Type      string          `json:"type"`
	Kind      string          `json:"kind"`
	Status    string          `json:"status"`
	Execution string          `json:"execution_status"`
	State     json.RawMessage `json:"state"`
	Payload   json.RawMessage `json:"payload"`
	Raw       json.RawMessage `json:"-"`
}

// isTerminalTaskStatus reports whether the task status signals a
// terminal conversation state.
func isTerminalTaskStatus(status string) bool {
	switch status {
	case "finished", "failed", "error", "stuck", "paused":
		return true
	}
	return false
}

// TaskStatus returns a normalised conversation status. It prefers
// explicit fields when present; otherwise falls back to known field names.
func (e *openhandsEventEnvelope) TaskStatus() string {
	if v := strings.ToLower(strings.TrimSpace(e.Execution)); v != "" {
		return v
	}
	if v := strings.ToLower(strings.TrimSpace(e.Status)); v != "" {
		return v
	}
	if v := strings.ToLower(strings.TrimSpace(e.Kind)); v != "" {
		return v
	}
	if v := strings.ToLower(strings.TrimSpace(e.Type)); v != "" {
		return v
	}
	return ""
}

func httpAPIHeaders(apiKey string) map[string][]string {
	if apiKey == "" {
		return nil
	}
	return map[string][]string{"X-Session-API-Key": {apiKey}}
}

type interruptReason string

const (
	interruptReasonRouter  interruptReason = "router_pending_action"
	interruptReasonSigterm interruptReason = "sigterm_busy"
)

// handleInterrupt drives the unified Router/SIGTERM interrupt path for a
// slot. The OpenHands pause-ack success wins over the interrupt-timeout
// (a successful pause means the executor will receive a "paused" frame
// or has already been told to clean up). Exactly one terminal task
// event is appended; the slot cleanup runs via the runTask defer.
func (e *Executor) handleInterrupt(slot *taskSlot, reason interruptReason) {
	if slot == nil {
		return
	}
	e.logger.Info("handleInterrupt enter",
		"task_id", slot.task.TaskID, "reason", string(reason))
	e.appendTaskEventBestEffort(context.Background(), slot.task.TaskID, platform.TaskSourceExecutor, "task.cancel_requested",
		map[string]interface{}{"reason": string(reason)})

	go func() {
		openCtx, cancel := context.WithTimeout(context.Background(), e.cfg.OpenHandsInterruptTO)
		defer cancel()
		addr := slot.getOpenHandsURL()
		if addr == "" {
			addr = e.docker.ContainerURL(slot.hostPort)
		}
		client := openhands.NewClient(addr, e.cfg.OpenHandsAPIKey, nil)
		convID := slot.getOpenHandsID()
		if convID == "" {
			convID = "unknown"
		}
		pauseErr := client.PauseConversation(openCtx, convID)
		if pauseErr != nil {
			e.logger.Warn("interrupt: openhands pause failed",
				"task_id", slot.task.TaskID, "err", pauseErr.Error())
		}

		// If the pause handshake succeeded, record task.interrupted
		// BEFORE checking the timeout. A later deadline tick must not
		// race-publish task.failed.
		if pauseErr == nil {
			if slot.claimTerminal() {
				slot.terminalCause = "interrupted"
				e.appendTaskEventBestEffort(context.Background(), slot.task.TaskID, platform.TaskSourceExecutor, "task.interrupted",
					map[string]interface{}{"openhands_status": "paused"})
				e.markAccepted(slot.task.TaskID, taskLifecycleTerminal)
			}
			slot.cancel()
			slot.signalDone()
			return
		}

		// Pause failed: wait for either the timer or the streaming
		// routine to bring the slot home.
		select {
		case <-openCtx.Done():
			slot.cancel()
			e.forceKill(slot)
			if slot.claimTerminal() {
				slot.terminalCause = "interrupt_timeout"
				e.appendTaskEventBestEffort(context.Background(), slot.task.TaskID, platform.TaskSourceExecutor, "task.failed",
					map[string]interface{}{"phase": "interrupt_timeout"})
				e.markAccepted(slot.task.TaskID, taskLifecycleTerminal)
			}
			slot.signalDone()
		case <-slot.doneCh:
			// Streaming routine already terminalized.
		}
	}()
}

// handleAppendMessage forwards a Router-provided message to OpenHands
// and journals task.message_forwarded. On failure the slot is
// terminalized exactly once.
func (e *Executor) handleAppendMessage(ctx context.Context, slot *taskSlot, content string) {
	addr := slot.getOpenHandsURL()
	if addr == "" {
		addr = e.docker.ContainerURL(slot.hostPort)
	}
	client := openhands.NewClient(addr, e.cfg.OpenHandsAPIKey, nil)
	convID := slot.getOpenHandsID()
	if convID == "" {
		convID = "unknown"
	}
	if err := client.AppendEvent(ctx, convID, "user", content); err != nil {
		if slot.claimTerminal() {
			slot.terminalCause = "append_message"
			e.appendTaskEventBestEffort(context.Background(), slot.task.TaskID, platform.TaskSourceExecutor, "task.failed",
				map[string]interface{}{"phase": "append_message", "error": err.Error()})
			e.markAccepted(slot.task.TaskID, taskLifecycleTerminal)
		}
		return
	}
	e.appendTaskEventBestEffort(context.Background(), slot.task.TaskID, platform.TaskSourceExecutor, "task.message_forwarded",
		map[string]interface{}{
			"role":    "user",
			"content": []map[string]string{{"type": "text", "text": content}},
			"run":     true,
		})
}

// drain stops accepting new tasks and removes all owned containers
// within the configured deadline. SIGTERM-during-busy uses the same
// code path through handleInterrupt for each task so the State
// Registry sees the same `task.cancel_requested` event it does for
// Router-driven interrupts.
func (e *Executor) drain(ctx context.Context) error {
	e.mu.Lock()
	slots := make([]*taskSlot, 0, len(e.slots))
	for _, s := range e.slots {
		slots = append(slots, s)
	}
	e.mu.Unlock()

	deadline := time.Now().Add(e.cfg.OpenHandsDrainTO)
	for _, s := range slots {
		e.handleInterrupt(s, interruptReasonSigterm)
		// best-effort stop while the timeout window is open; the
		// slot's defer runs cleanupContainer which retries on
		// failure.
		cctx, ccancel := context.WithTimeout(ctx, 5*time.Second)
		if err := e.docker.StopContainer(cctx, s.containerID, 5*time.Second); err != nil {
			e.logger.Warn("drain stop failed",
				"task_id", s.task.TaskID, "err", err.Error())
		}
		ccancel()
		if time.Now().After(deadline) {
			e.forceKill(s)
		}
	}

	// Best-effort join on each runTask goroutine (all defers
	// complete, including container cleanup).
	for _, s := range slots {
		select {
		case <-s.exitCh:
		case <-ctx.Done():
			e.logger.Warn("drain: runTask did not finish within deadline",
				"task_id", s.task.TaskID)
		}
	}
	return nil
}

// cleanupContainer idempotently stops+removes the container that
// produced the slot. It uses bounded contexts derived from the
// supplied parent (so callers can pass either the run context or a
// fresh drain context); the inner timeouts are short to bound the
// wait the executor spends in shutdown.
func (e *Executor) cleanupContainer(slot *taskSlot) {
	if slot == nil {
		return
	}
	slot.cleanup(context.Background(), e.docker.StopContainer, e.docker.ForceKill)
}

// forceKill is the public hook the interrupt handler uses to escalate
// to ForceKill when the pause handshake did not ack.
func (e *Executor) forceKill(slot *taskSlot) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := e.docker.ForceKill(ctx, slot.containerID); err != nil {
		e.logger.Warn("force-kill failed", "container_id", slot.containerID, "err", err.Error())
	}
}

// subscribeDockerEvents streams Docker events for the Executor's
// labeled containers. A stream loss (peer closed connection or
// unexpected error) marks the Executor fatal so pollLoop unwinds
// and Run() drains existing slots before exiting.
//
// On ctx cancellation, the goroutine performs a non-blocking drain
// of any events already queued on the msgs channel so a "die" event
// that races with SIGTERM is still processed before exit.
func (e *Executor) subscribeDockerEvents(ctx context.Context) {
	filter := dockerclient.NewEventFilter(
		dockerclient.EventFilterEntry{
			Key:   dockerclient.EventFilterKeyType,
			Value: "container",
		},
		dockerclient.EventFilterEntry{
			Key:   dockerclient.EventFilterKeyLabel,
			Value: dockerclient.LabelExecutorID + "=" + e.cfg.ExecutorID,
		},
		dockerclient.EventFilterEntry{
			Key:   dockerclient.EventFilterKeyLabel,
			Value: dockerclient.LabelCleanupID + "=" + e.cleanup,
		},
	)
	msgs, errs := e.docker.SubscribeEvents(ctx, filter)
	for {
		select {
		case <-ctx.Done():
			e.drainEvents(msgs)
			return
		case m, ok := <-msgs:
			if !ok {
				e.handleDockerEventStreamLoss("msgs_closed")
				return
			}
			e.handleEventMessage(m)
		case err, ok := <-errs:
			if !ok {
				e.handleDockerEventStreamLoss("errs_closed")
				return
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				e.logger.Warn("docker events error", "err", err.Error())
				e.handleDockerEventStreamLoss("docker_events_error")
				return
			}
			return
		}
	}
}

// drainEvents best-effort drains any buffered msgs so a die event
// that lands just as ctx cancels is still processed. Safe to call
// with a closed or nil channel.
func (e *Executor) drainEvents(msgs <-chan dockerclient.EventMessage) {
	for {
		select {
		case m, ok := <-msgs:
			if !ok {
				return
			}
			e.handleEventMessage(m)
		default:
			return
		}
	}
}

// handleDockerEventStreamLoss shuts the Executor down when the Docker
// event stream is gone (cannot supervise containers anymore).
func (e *Executor) handleDockerEventStreamLoss(phase string) {
	e.logger.Warn("docker events stream lost", "phase", phase)
	e.markFatal(phase)
}

// handleEventMessage records a single Docker event for a tracked slot
// and processes container "die" -> task.failed.
func (e *Executor) handleEventMessage(m dockerclient.EventMessage) {
	e.mu.Lock()
	var slot *taskSlot
	var taskID string
	for _, s := range e.slots {
		if s.containerID == m.ActorID || s.containerName == m.ActorName {
			slot = s
			taskID = s.task.TaskID
			break
		}
	}
	e.mu.Unlock()
	if taskID == "" {
		return
	}
	e.appendTaskEventBestEffort(context.Background(), taskID, platform.TaskSourceDocker,
		"docker.container."+m.Action,
		map[string]interface{}{"container_id": m.ActorID, "container_name": m.ActorName, "docker_action": m.Action, "at": m.Time})
	if m.Action == "die" {
		e.handleContainerDie(slot)
	}
}

// handleContainerDie records task.failed(phase=container_died) and
// triggers container cleanup via sync.Once. Other tasks continue.
func (e *Executor) handleContainerDie(slot *taskSlot) {
	if slot == nil {
		return
	}
	if slot.claimTerminal() {
		slot.terminalCause = "container_died"
		e.appendTaskEventBestEffort(context.Background(), slot.task.TaskID, platform.TaskSourceExecutor, "task.failed",
			map[string]interface{}{"reason": "container_died"})
		e.markAccepted(slot.task.TaskID, taskLifecycleTerminal)
	}
	// cancel the runTask context so the WS loop unwinds; the
	// runTask defer triggers cleanupContainer.
	slot.cancel()
}

func (e *Executor) appendExecutorEventBestEffort(ctx context.Context, t platform.ExecutorEventType, payload map[string]interface{}) {
	if ctx == nil {
		ctx = context.Background()
	}
	ev := mockedclient.NewExecutorEvent(e.cfg.ExecutorID, t, payload)
	if err := e.mocked.AppendExecutorEvent(ctx, ev); err != nil {
		e.logger.Warn("executor event append failed",
			"type", string(t), "err", err.Error())
	}
}

func (e *Executor) appendTaskEventBestEffort(ctx context.Context, taskID string, source platform.TaskEventSource, eventType string, payload map[string]interface{}) {
	if taskID == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ev := mockedclient.NewTaskEvent(taskID, e.cfg.ExecutorID, source, eventType, payload)
	if err := e.mocked.AppendTaskEvent(ctx, ev); err != nil {
		e.logger.Warn("task event append failed",
			"task_id", taskID, "type", eventType, "err", err.Error())
	}
}

// setReadyIfLast emits executor.idle when the slot count is zero and
// the Executor is not in a stopping/draining/failed state. Defers run
// AFTER removeSlot so the slot count check is post-removal (TOCTOU
// safe).
func (e *Executor) setReadyIfLast() {
	st := e.State()
	if st == StateStopping || st == StateStopped || st == StateFailed {
		return
	}
	count := e.slotCount()
	if count != 0 {
		return
	}
	e.setState(StateReady)
	e.appendExecutorEventBestEffort(context.Background(), platform.ExecutorEventIdle,
		map[string]interface{}{"running_child_count": count})
	e.observeSlotCount()
}

// terminalizeTask centralises the post-defer bookkeeping: removes the
// per-slot dedupe map (bounds memory), marks the acceptedTasks entry
// terminal for replay-prevention, and releases the host port.
func (e *Executor) terminalizeTask(slot *taskSlot) {
	if slot == nil {
		return
	}
	slot.dropPending()
	if slot.claimTerminal() {
		e.markAccepted(slot.task.TaskID, taskLifecycleTerminal)
	}
	e.releasePort(slot.hostPort)
}

// observeSlotCount refreshes the State Registry registration whenever
// the running_child_count differs from the last reported value. It
// also emits executor.busy the first time the count flips 0 -> 1+
// so the audit log is consistent with the registration refresh.
func (e *Executor) observeSlotCount() {
	current := e.slotCount()
	e.muCounts.Lock()
	last := e.lastRunning
	if current == last {
		e.muCounts.Unlock()
		return
	}
	e.lastRunning = current
	becomingBusy := last == 0 && current > 0
	becomingIdle := current == 0 && last > 0
	e.muCounts.Unlock()

	e.refreshRegistration(current)
	if becomingBusy {
		e.setState(StateBusy)
		e.appendExecutorEventBestEffort(context.Background(), platform.ExecutorEventBusy,
			map[string]interface{}{"running_child_count": current})
	}
	if becomingIdle {
		// setReadyIfLast already published executor.idle; nothing
		// extra to do here.
	}
}

// refreshRegistration re-PUTs the Executor record with the current
// running_child_count.
func (e *Executor) refreshRegistration(running int) {
	rec := &platform.ExecutorRecord{
		ExecutorID:        e.cfg.ExecutorID,
		ExecutorType:      platform.ExecutorTypeDockerOpenHands,
		RoutingTarget:     e.cfg.RoutingTarget,
		Capacity:          e.cfg.MaxContainers,
		RunningChildCount: running,
		Metadata: map[string]interface{}{
			"openhands_image":           e.cfg.OpenHandsImage,
			"openhands_host_port_start": e.cfg.OpenHandsPortStart,
			"openhands_host_port_end":   e.cfg.OpenHandsPortEnd,
			"cleanup_id":                e.cleanup,
			"version":                   "v0001",
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if _, err := e.mocked.RegisterExecutor(ctx, rec); err != nil {
		e.logger.Warn("executor registration refresh failed", "err", err.Error())
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
	_, present := e.slots[taskID]
	delete(e.slots, taskID)
	delete(e.pendingActionSeen, taskID)
	e.mu.Unlock()
	if !present {
		return
	}
	// Per-change registration refresh AFTER removal so the running
	// count transition 1->0 (or any) is reported.
	e.observeSlotCount()
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
func (e *Executor) RunningChildCount() int { return e.slotCount() }

// Logger returns the Executor logger.
func (e *Executor) Logger() *slog.Logger {
	if e.logger == nil {
		return logging.FromContext(context.Background())
	}
	return e.logger
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

func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}
