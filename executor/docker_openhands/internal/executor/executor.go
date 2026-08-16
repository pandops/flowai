// Package executor implements the Docker Executor backed exclusively by the
// durable State Registry. It registers under scope=team|system and one
//
//	authorized tag; discovers pending tasks via
//	GET /v1/executors/{id}/tasks?tag=<tag> in FIFO order; claims
//	each eligible task atomically via POST /v1/executors/{id}/claim
//	with a stable command_id; opens the team-bound environment
//	values via GET /v1/environments/{id}/open?task_id={id} using
//	the scope token returned by the claim; starts one container
//	per claim using the resolved_image from the claim response
//	verbatim; emits `running` after claim and exactly one of
//	`finished` or `failed` at terminal state. Capacity is enforced
//	locally: the Executor never starts a runtime before a
//	successful 200 claim and never starts one on 404,
//	409 older_task_must_be_claimed_first, or
//	409 task_already_claimed.
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
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/flowai/platform/executor/docker_openhands/internal/cache"
	"github.com/flowai/platform/executor/docker_openhands/internal/dockerclient"
	"github.com/flowai/platform/executor/docker_openhands/internal/logging"
	"github.com/flowai/platform/executor/docker_openhands/internal/openhands"
	"github.com/flowai/platform/executor/docker_openhands/internal/platform"
	"github.com/flowai/platform/executor/docker_openhands/internal/stateregistryclient"
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
	CacheDir             string        `yaml:"cache_dir"`
	ExecutorAPIBind      string        `yaml:"executor_api_bind"`
	MaxContainers        int           `yaml:"executor_max_containers"`
	DockerSocketPath     string        `yaml:"docker_socket_path"`
	DockerPublishedHost  string        `yaml:"docker_published_host"`
	OpenHandsImage       string        `yaml:"openhands_image"`
	OpenHandsPortStart   int           `yaml:"openhands_host_port_start"`
	OpenHandsPortEnd     int           `yaml:"openhands_host_port_end"`
	OpenHandsAPIKey      string        `yaml:"openhands_api_key"`
	OpenHandsStartupTO   time.Duration `yaml:"openhands_startup_timeout"`
	OpenHandsDrainTO     time.Duration `yaml:"openhands_drain_timeout"`
	ImagePullPolicy      string        `yaml:"image_pull_policy"`
	LogLevel             string        `yaml:"log_level"`
	StateRegistryURL     string        `yaml:"state_registry_url"`
	Scope                string        `yaml:"scope"`
	TeamID               string        `yaml:"team_id"`
	AuthorizedTag        string        `yaml:"authorized_tag"`
	PollInterval         time.Duration `yaml:"poll_interval"`
	WebSocketDialTimeout time.Duration `yaml:"websocket_dial_timeout"`

	// Legacy State Registry TLS material. Accepted for staged
	// configuration cleanup but never read: the backend transport
	// is plaintext after v0009. The Executor's HTTP client never
	// constructs a tls.Config; the values below stay as no-op
	// compatibility inputs. A later change may remove them from
	// the schema after deployments converge.
	StateRegistryTLSCertPath     string `yaml:"state_registry_tls_client_cert"`
	StateRegistryTLSKeyPath      string `yaml:"state_registry_tls_client_key"`
	StateRegistryTLSServerCAPath string `yaml:"state_registry_tls_server_ca"`

	// FinishedCleanupDelay is the retention window after the
	// State Registry accepts a terminal `finished` event. Each
	// defaults to 0s for immediate cleanup. The container stays
	// counted against local capacity until the delay expires or an
	// explicit shutdown/drain cleans up sooner; a later cleanup
	// failure MUST NOT append a second terminal event.
	FinishedCleanupDelay time.Duration `yaml:"finished_cleanup_delay"`
	FailedCleanupDelay   time.Duration `yaml:"failed_cleanup_delay"`

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
		MaxContainers:        2,
		DockerSocketPath:     "",
		DockerPublishedHost:  "127.0.0.1",
		OpenHandsImage:       "ghcr.io/openhands/agent-server:latest-python",
		OpenHandsPortStart:   18000,
		OpenHandsPortEnd:     18099,
		OpenHandsStartupTO:   60 * time.Second,
		OpenHandsDrainTO:     30 * time.Second,
		ImagePullPolicy:      "if-not-present",
		LogLevel:             "info",
		PollInterval:         2 * time.Second,
		WebSocketDialTimeout: 5 * time.Second,
		// V1 conversation startup defaults.
		OpenHandsWorkspace:   "/workspace/project",
		OpenHandsLLMUsageID:  "flowai-executor",
		OpenHandsInitialRun:  true,
		FinishedCleanupDelay: 0,
		FailedCleanupDelay:   0,
	}
	if userCacheDir, err := os.UserCacheDir(); err == nil {
		cfg.CacheDir = filepath.Join(userCacheDir, "flowai", "executor-docker-openhands")
	}
	if yamlPath != "" {
		_ = loadYAML(yamlPath, cfg)
	}
	overrideEnv(cfg)
	// When the operator has not configured an OpenHands session API
	// key, mint a fresh opaque one. Injecting this into the
	// OpenHands container as SESSION_API_KEY makes the agent-server
	// bind on 0.0.0.0 (default without auth is 127.0.0.1), so Docker
	// port publishing can reach it without overriding the image's
	// command — arbitrary image entrypoints stay untouched. The
	// openhands.Client already sends cfg.OpenHandsAPIKey in
	// X-Session-API-Key on every call, so the in-container server and
	// the Executor stay in sync.
	if cfg.OpenHandsAPIKey == "" {
		cfg.OpenHandsAPIKey = "flowai-" + uuid.NewString()
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
	cfg.MaxContainers = envInt("EXECUTOR_MAX_CONTAINERS", cfg.MaxContainers)
	cfg.DockerSocketPath = envOr("DOCKER_SOCKET_PATH", cfg.DockerSocketPath)
	cfg.DockerPublishedHost = envOr("DOCKER_PUBLISHED_HOST", cfg.DockerPublishedHost)
	cfg.OpenHandsImage = envOr("OPENHANDS_IMAGE", cfg.OpenHandsImage)
	cfg.OpenHandsPortStart = envInt("OPENHANDS_HOST_PORT_START", cfg.OpenHandsPortStart)
	cfg.OpenHandsPortEnd = envInt("OPENHANDS_HOST_PORT_END", cfg.OpenHandsPortEnd)
	cfg.OpenHandsAPIKey = envOr("OPENHANDS_API_KEY", cfg.OpenHandsAPIKey)
	cfg.OpenHandsStartupTO = envDur("OPENHANDS_STARTUP_TIMEOUT_SECONDS", cfg.OpenHandsStartupTO)
	cfg.OpenHandsDrainTO = envDur("OPENHANDS_DRAIN_TIMEOUT_SECONDS", cfg.OpenHandsDrainTO)
	cfg.ImagePullPolicy = envOr("IMAGE_PULL_POLICY", cfg.ImagePullPolicy)
	cfg.LogLevel = envOr("LOG_LEVEL", cfg.LogLevel)
	cfg.StateRegistryURL = envOr("EXECUTOR_STATE_REGISTRY_URL", cfg.StateRegistryURL)
	cfg.Scope = envOr("EXECUTOR_SCOPE", cfg.Scope)
	cfg.TeamID = envOr("EXECUTOR_TEAM_ID", cfg.TeamID)
	cfg.AuthorizedTag = envOr("EXECUTOR_AUTHORIZED_TAG", cfg.AuthorizedTag)
	cfg.PollInterval = envDur("EXECUTOR_POLL_INTERVAL", cfg.PollInterval)
	cfg.CacheDir = envOr("EXECUTOR_CACHE_DIR", cfg.CacheDir)
	cfg.WebSocketDialTimeout = envDur("OPENHANDS_WS_DIAL_TIMEOUT_SECONDS", cfg.WebSocketDialTimeout)
	// Legacy State Registry TLS material env overrides. Accepted
	// for staged configuration cleanup but never read after v0009.
	cfg.StateRegistryTLSCertPath = envOr("EXECUTOR_STATE_REGISTRY_TLS_CLIENT_CERT", cfg.StateRegistryTLSCertPath)
	cfg.StateRegistryTLSKeyPath = envOr("EXECUTOR_STATE_REGISTRY_TLS_CLIENT_KEY", cfg.StateRegistryTLSKeyPath)
	cfg.StateRegistryTLSServerCAPath = envOr("EXECUTOR_STATE_REGISTRY_TLS_SERVER_CA", cfg.StateRegistryTLSServerCAPath)
	// v0005 cleanup-delay overrides (each default 0s for immediate cleanup).
	cfg.FinishedCleanupDelay = envDur("EXECUTOR_FINISHED_CLEANUP_DELAY", cfg.FinishedCleanupDelay)
	cfg.FailedCleanupDelay = envDur("EXECUTOR_FAILED_CLEANUP_DELAY", cfg.FailedCleanupDelay)
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
	if strings.TrimSpace(c.DockerSocketPath) == "" {
		return errors.New("DOCKER_SOCKET_PATH must be configured explicitly")
	}
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
	if c.StateRegistryURL == "" {
		return errors.New("EXECUTOR_STATE_REGISTRY_URL is required")
	}
	{
		if c.Scope != "team" && c.Scope != "system" {
			return errors.New("EXECUTOR_SCOPE must be team or system")
		}
		if c.Scope == "team" && c.TeamID == "" {
			return errors.New("EXECUTOR_TEAM_ID is required for team scope")
		}
		if c.Scope == "system" && c.TeamID != "" {
			return errors.New("EXECUTOR_TEAM_ID must be empty for system scope")
		}
		if c.AuthorizedTag == "" {
			return errors.New("EXECUTOR_AUTHORIZED_TAG must be non-empty")
		}
		// v0009: the State Registry transport is plain HTTP. Legacy
		// backend TLS/mTLS fields are accepted but never read and
		// never validated. A later change may remove them from
		// the schema after deployments converge.
		stateRegistryURL, err := url.Parse(c.StateRegistryURL)
		if err != nil || (stateRegistryURL.Scheme != "http" && stateRegistryURL.Scheme != "https") || stateRegistryURL.Hostname() == "" {
			return fmt.Errorf("EXECUTOR_STATE_REGISTRY_URL must use http or https scheme, got %q", c.StateRegistryURL)
		}
	}
	if c.WebSocketDialTimeout <= 0 {
		return errors.New("OPENHANDS_WS_DIAL_TIMEOUT_SECONDS must be > 0")
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
	if c.FinishedCleanupDelay < 0 {
		return errors.New("EXECUTOR_FINISHED_CLEANUP_DELAY must be >= 0")
	}
	if c.FailedCleanupDelay < 0 {
		return errors.New("EXECUTOR_FAILED_CLEANUP_DELAY must be >= 0")
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
	cleanup string

	// v0002 is the sole task/state client.
	v0002 *stateregistryclient.Client
	cache *cache.Store

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

	// slotReservations counts claimed-but-not-yet-installed
	// tasks on the v0002 path. Together with len(slots) it forms
	// the local capacity gate; a task that fails to install
	// (e.g. Docker pull fails) decrements the reservation
	// before the next tick re-evaluates FIFO.
	slotReservations int

	// commandIDs pins the stable command_id used to claim a given
	// task_id. The same task retry MUST reuse the same command_id so
	// the State Registry's idempotent (task_id, command_id) handler
	// returns the original 200 without appending an additional event.
	// The map is cleaned up when the slot is terminalized.
	commandIDs   map[string]string
	logMu        sync.Mutex
	publishedLog map[string]struct{}

	stateOK     atomic.Bool
	openHandsOK atomic.Bool

	// muCounts guards lastRunning; readers take it briefly to read or
	// detect a counter change after add/remove slot mutations.
	muCounts sync.Mutex
}

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
// doneOnce/exitOnce serialise lifecycle channel closure.
type taskSlot struct {
	v0002Task     *v0002TaskRef
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

	// acceptedTerminalMu guards the accepted-at fields; cleanup
	// needs the accepted type to select finished_cleanup_delay or
	// failed_cleanup_delay, and the time at which the State
	// Registry returned 202.
	acceptedTerminalMu sync.Mutex
	acceptedTerminal   string // "" | platform.TaskEventTypeFinished | platform.TaskEventTypeFailed
	acceptedAt         time.Time

	cancel   context.CancelFunc
	doneCh   chan struct{}
	doneOnce sync.Once
	exitCh   chan struct{}
	exitOnce sync.Once
}

// v0002TaskRef is the per-slot v0002 claim record. The fields
// are immutable from the time the slot is created; the canonical
// TaskListEntry is the value the State Registry returned in the
// 200 claim response.
type v0002TaskRef struct {
	task          platform.V0002TaskListEntry
	commandID     string
	resolvedImage string
	imageSource   string
	llmAPIKey     string
	llmBaseURL    string
	llmModel      string
}

// slotTaskID returns the canonical task_id from the immutable claim response.
func slotTaskID(s *taskSlot) string {
	if s == nil || s.v0002Task == nil {
		return ""
	}
	return s.v0002Task.task.TaskID
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

// recordAcceptedTerminal records the State Registry-accepted
// terminal event type and acceptance time so cleanupContainer can
// select the v0005 finished_cleanup_delay or failed_cleanup_delay.
// The selected delay is honoured only after this record; earlier
// cleanup paths stay bounded.
func (s *taskSlot) recordAcceptedTerminal(eventType string) {
	if s == nil {
		return
	}
	s.acceptedTerminalMu.Lock()
	defer s.acceptedTerminalMu.Unlock()
	s.acceptedTerminal = eventType
	s.acceptedAt = time.Now().UTC()
}

// acceptedTerminal returns the State Registry-accepted terminal
// event type ("", "finished", or "failed") and the time at which the
// State Registry accepted it. Used by cleanupContainer to apply the
// configured cleanup_delay after the terminal-event acceptance.
func (s *taskSlot) acceptedTerminalRecord() (string, time.Time) {
	if s == nil {
		return "", time.Time{}
	}
	s.acceptedTerminalMu.Lock()
	defer s.acceptedTerminalMu.Unlock()
	return s.acceptedTerminal, s.acceptedAt
}

// New constructs a new Executor.
func New(cfg *Config, docker dockerclient.Client, logger *slog.Logger) *Executor {
	e := &Executor{
		cfg:           cfg,
		logger:        logger,
		docker:        docker,
		slots:         map[string]*taskSlot{},
		nextPort:      cfg.OpenHandsPortStart,
		acceptedTasks: map[string]taskLifecycle{},
		commandIDs:    map[string]string{},
		publishedLog:  map[string]struct{}{},
		fatalCh:       make(chan struct{}),
	}
	e.state.Store(StateStarting)
	return e
}

// attachV0002 wires a constructed v0002 client into the Executor. It
// is called by the test helpers and the v0002 registration path; the
// production startup path calls it inline when cfg.StateRegistryURL
// is set.
func (e *Executor) attachV0002(c *stateregistryclient.Client) { e.v0002 = c }

func (e *Executor) State() State { return e.state.Load().(State) }

func (e *Executor) IsStateRegistryRegistered() bool { return e.stateOK.Load() }
func (e *Executor) IsOpenHandsReachable() bool      { return e.openHandsOK.Load() }

// AttachCache wires the process-lifetime persistent store after its exclusive
// lock has been acquired by main.
func (e *Executor) AttachCache(store *cache.Store) { e.cache = store }

// ExecutorID returns the cached or freshly registered identity.
func (e *Executor) ExecutorID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg.ExecutorID
}

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
		return fmt.Errorf("cleanup id: %w", err)
	}
	e.cleanup = cleanupID

	if err := e.cleanupLeftover(ctx); err != nil {
		e.logger.Warn("startup cleanup warning", "err", err.Error())
	}

	if err := e.register(ctx); err != nil {
		e.setState(StateStopped)
		return fmt.Errorf("register: %w", err)
	}
	e.stateOK.Store(true)
	e.setState(StateReady)
	e.appendV0002ExecutorEvent(context.Background(), platform.V0002ExecutorEventHealthy, nil)

	// Owned run context: cancellation drives Run() to drain. The
	// pollLoop also listens on fatalCh so a Docker stream loss can
	// exit the polling loop without waiting for the outer ctx.
	runCtx, runCancel := context.WithCancel(ctx)
	e.runCancel = runCancel
	defer runCancel()

	pollErr := e.pollLoop(runCtx)
	e.setState(StateStopping)
	e.appendV0002ExecutorEvent(context.Background(), platform.V0002ExecutorEventStopping, nil)

	drainCtx, drainCancel := context.WithTimeout(context.Background(), e.cfg.OpenHandsDrainTO+5*time.Second)
	e.drain(drainCtx)
	drainCancel()

	e.setState(StateStopped)
	e.appendV0002ExecutorEvent(context.Background(), platform.V0002ExecutorEventStopped, nil)

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
		e.appendV0002ExecutorEvent(context.Background(), platform.V0002ExecutorEventFailed,
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
	ident := stateregistryclient.Identity{
		ExecutorID: e.cfg.ExecutorID,
		Scope:      e.cfg.Scope,
	}
	if e.cfg.Scope == "team" {
		ident.TeamID = e.cfg.TeamID
	}
	// v0009: backend connections use plain HTTP. The HTTP client
	// stays nil so stateregistryclient.New constructs the default
	// 30s-timeout client; legacy TLS material fields stay as
	// compatibility inputs that the client never reads.
	client, err := stateregistryclient.New(e.cfg.StateRegistryURL, ident, nil)
	if err != nil {
		return err
	}
	var teamID *string
	if e.cfg.Scope == "team" {
		t := e.cfg.TeamID
		teamID = &t
	}
	md, _ := json.Marshal(map[string]interface{}{
		"runtime":    "docker",
		"tool":       "openhands",
		"cleanup_id": e.cleanup,
	})
	body := stateregistryclient.RegisterExecutorRequest{
		Scope:           e.cfg.Scope,
		TeamID:          teamID,
		ExecutorType:    platform.ExecutorTypeDockerOpenHands,
		AuthorizedTag:   e.cfg.AuthorizedTag,
		MaxCapacity:     e.cfg.MaxContainers,
		RunningCount:    0,
		RuntimeMetadata: md,
	}
	if e.cfg.ExecutorID == "" {
		record, createErr := client.CreateExecutor(ctx, body)
		if createErr != nil {
			return createErr
		}
		if e.cache == nil {
			return errors.New("first registration requires persistent cache")
		}
		if persistErr := e.cache.PutExecutorID(ctx, record.ExecutorID); persistErr != nil {
			return fmt.Errorf("persist executor_id: %w", persistErr)
		}
		e.mu.Lock()
		e.cfg.ExecutorID = record.ExecutorID
		e.mu.Unlock()
	} else {
		_, err = client.RegisterExecutor(ctx, body)
	}
	if err != nil {
		return err
	}
	e.v0002 = client
	return nil
}

func sameOptionalString(left, right *string) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
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
	return nil
}

// pollLoop discovers and claims State Registry tasks up to local capacity.
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
	return e.tickV0002(ctx)
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

// envToDockerEnv returns a stable Docker environment representation.
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

// dockerEnvWithSessionAPIKey defensively copies the supplied env
// map and forces SESSION_API_KEY to the configured key when non-
// empty. When the configured key is empty, any caller-provided
// SESSION_API_KEY is stripped so the helper remains the single owner
// of that variable. The input map is never mutated.
func dockerEnvWithSessionAPIKey(values map[string]string, key string) []string {
	copied := make(map[string]string, len(values))
	for k, v := range values {
		copied[k] = v
	}
	if key == "" {
		delete(copied, "SESSION_API_KEY")
	} else {
		copied["SESSION_API_KEY"] = key
	}
	return envToDockerEnv(copied)
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
//  2. cleanupContainer runs while the slot is still present, so a
//     configured terminal retention delay continues to consume capacity.
//  3. removeSlot runs before setReadyIfLast so the slot count check
//     observes the post-removal map (TOCTOU-safe).
//  4. setReadyIfLast publishes executor.idle and refreshes registration
//     only when THIS slot is the terminal-cause of the count going
//     to zero.
//  5. cleanupContainer remains authoritative and runs
//     exactly once thanks to sync.Once and uses bounded contexts.
func (e *Executor) conversationConfig(prompt, taskAPIKey, taskBaseURL, taskModel string) openhands.ConversationConfig {
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
		Kind:  "Agent",
		Tools: []openhands.Tool{{Name: "terminal"}},
		LLM: openhands.LLM{
			Model:   taskModel,
			APIKey:  taskAPIKey,
			UsageID: e.cfg.OpenHandsLLMUsageID,
			BaseURL: taskBaseURL,
		},
	}
	return cfg
}

// failSlot records task.failed for post-slot terminalisation. The
// caller provides a cause ("health_check" / "submit") and the slot's
// defer chain handles cleanupContainer.
func (e *Executor) streamOpenHandsEvents(ctx context.Context, slot *taskSlot, conversationID string) error {
	containerURL := slot.getOpenHandsURL()
	if containerURL == "" {
		containerURL = e.docker.ContainerURL(slot.hostPort)
	}
	wsURL := OpenHandsWSURL(containerURL, conversationID)
	taskID := slotTaskID(slot)

	dialCtx, cancel := context.WithTimeout(ctx, e.cfg.WebSocketDialTimeout)
	defer cancel()

	dialer := websocket.Dialer{HandshakeTimeout: e.cfg.WebSocketDialTimeout}
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
	e.logger.Info("websocket connected", "task_id", taskID, "conv_id", conversationID)
	defer conn.Close()

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
			e.logger.Warn("websocket read error", "task_id", taskID, "err", err.Error(), "ctx_err", ctx.Err())
			return err
		}
		var envelope any
		if err := json.Unmarshal(data, &envelope); err != nil {
			continue
		}
		e.appendPublishedOpenHandsLogs(ctx, taskID, envelope)
		status := taskStatusFromValue(envelope)
		if !isTerminalTaskStatus(status) {
			continue
		}
		if slot.claimTerminal() {
			slot.terminalCause = "terminal_state:" + status
			eventType := platform.TaskEventTypeFailed
			if status == "finished" {
				eventType = platform.TaskEventTypeFinished
			}
			e.appendV0002TaskEventQuiet(ctx, taskID, eventType, map[string]string{
				"phase":            "terminal_state",
				"openhands_status": status,
			})
			slot.recordAcceptedTerminal(eventType)
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

func taskStatusFromValue(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		if raw, ok := typed["execution_status"].(string); ok {
			if status := strings.ToLower(strings.TrimSpace(raw)); status != "" {
				return status
			}
		}
		for _, nested := range typed {
			if status := taskStatusFromValue(nested); status != "" {
				return status
			}
		}
		for _, key := range []string{"status", "kind", "type"} {
			if raw, ok := typed[key].(string); ok {
				if status := strings.ToLower(strings.TrimSpace(raw)); status != "" {
					return status
				}
			}
		}
	case []any:
		for _, nested := range typed {
			if status := taskStatusFromValue(nested); status != "" {
				return status
			}
		}
	}
	return ""
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

// drain stops accepting new tasks and removes all owned containers within
// the configured deadline.
func (e *Executor) drain(ctx context.Context) error {
	e.mu.Lock()
	slots := make([]*taskSlot, 0, len(e.slots))
	for _, slot := range e.slots {
		slots = append(slots, slot)
	}
	e.mu.Unlock()

	deadline := time.Now().Add(e.cfg.OpenHandsDrainTO)
	for _, slot := range slots {
		if slot.cancel != nil {
			slot.cancel()
		}
		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := e.docker.StopContainer(stopCtx, slot.containerID, 5*time.Second); err != nil {
			e.logger.Warn("drain stop failed", "task_id", slotTaskID(slot), "err", err.Error())
		}
		cancel()
		if time.Now().After(deadline) {
			e.forceKill(slot)
		}
	}
	for _, slot := range slots {
		select {
		case <-slot.exitCh:
		case <-ctx.Done():
			e.logger.Warn("drain: task did not finish within deadline", "task_id", slotTaskID(slot))
		}
	}
	return nil
}

// cleanupContainer idempotently stops+removes the container that
// produced the slot. It uses bounded contexts derived from the
// supplied parent (so callers can pass either the run context or a
// fresh drain context); the inner timeouts are short to bound the
// wait the executor spends in shutdown.
//
// v0005 conformance: when the slot has already recorded an accepted
// terminal event, the configured finished_cleanup_delay (after
// `finished`) or failed_cleanup_delay (after `failed`) is honoured
// before stop+remove; the container continues to count against
// local capacity during the delay; a failed removal SHALL NOT
// append a second terminal task event.
func (e *Executor) cleanupContainer(slot *taskSlot) {
	if slot == nil {
		return
	}
	if !e.applyTerminalCleanupDelay(slot) {
		slot.cleanup(context.Background(), e.docker.StopContainer, e.docker.ForceKill)
	}
}

// applyTerminalCleanupDelay blocks until the configured
// finished_cleanup_delay or failed_cleanup_delay, selected by the
// slot's accepted terminal event, expires. A zero or unset delay
// returns false without blocking. The container removal runs only
// when the delay expired in this call. Earlier paths without a
// recorded terminal event keep their original immediate-cleanup
// semantics.
func (e *Executor) applyTerminalCleanupDelay(slot *taskSlot) (applied bool) {
	if slot == nil || e.cfg == nil {
		return false
	}
	eventType, acceptedAt := slot.acceptedTerminalRecord()
	if eventType == "" {
		return false
	}
	var delay time.Duration
	switch eventType {
	case platform.TaskEventTypeFinished:
		delay = e.cfg.FinishedCleanupDelay
	case platform.TaskEventTypeFailed:
		delay = e.cfg.FailedCleanupDelay
	default:
		return false
	}
	if delay <= 0 {
		return false
	}
	if remaining := time.Until(acceptedAt.Add(delay)); remaining > 0 {
		t := time.NewTimer(remaining)
		<-t.C
	}
	slot.cleanup(context.Background(), e.docker.StopContainer, e.docker.ForceKill)
	return true
}

func (e *Executor) forceKill(slot *taskSlot) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := e.docker.ForceKill(ctx, slot.containerID); err != nil {
		e.logger.Warn("force-kill failed", "container_id", slot.containerID, "err", err.Error())
	}
}

// setReadyIfLast emits executor.idle when the slot count is zero and
// the Executor is not in a stopping/draining/failed state. Defers run
// AFTER removeSlot so the slot count check is post-removal (TOCTOU
// safe).
func (e *Executor) setReadyIfLast() {
	state := e.State()
	if state == StateStopping || state == StateStopped || state == StateFailed || e.slotCount() != 0 {
		return
	}
	e.setState(StateReady)
	e.appendV0002ExecutorEvent(context.Background(), platform.V0002ExecutorEventIdle,
		map[string]interface{}{"running_child_count": 0})
	e.observeSlotCount()
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
	e.muCounts.Unlock()

	e.refreshRegistrationV0002(current)
	if becomingBusy {
		e.setState(StateBusy)
		e.appendV0002ExecutorEvent(context.Background(), platform.V0002ExecutorEventBusy,
			map[string]interface{}{"running_child_count": current})
	}
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
