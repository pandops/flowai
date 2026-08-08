// Package executor implements the v0005 K8s Executor state machine.
// The K8s Executor mirrors the durable-v0002 contract but maps the
// runtime from Docker containers to Kubernetes Pods, adds the v0005
// persistent recovery cache and OS file lock, and reuses the
// finalized scope-token contract verbatim.
//
// The package never imports executor_docker_openhands/* (per
// AGENTS.md). Each per-service package owns its dependencies.
package executor

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/flowai/platform/executor_k8s_openhands/internal/cache"
	"github.com/flowai/platform/executor_k8s_openhands/internal/config"
	"github.com/flowai/platform/executor_k8s_openhands/internal/k8sclient"
	"github.com/flowai/platform/executor_k8s_openhands/internal/platform"
	"github.com/flowai/platform/executor_k8s_openhands/internal/stateregistryclient"
)

// CacheDirPath returns the absolute path to the cache directory for
// the Executor. When the operator has not supplied an explicit
// override, the Executor's <data_dir>/cache is the documented
// default.
func CacheDirPath() string { return filepath.Join(DataDirPath(), "cache") }

// DataDirPath is the documented base directory for Executor
// persistent state on a host-backed volume or PVC. The directory
// is created owner-only by LoadConfig if it does not exist.
func DataDirPath() string { return "/var/lib/flowai/executor-k8s" }

// CleanupIdentity returns the stable identity used for housekeeping.
func CleanupIdentity() string { return "executor-k8s-openhands" }

// Config holds the v0005 K8s Executor configuration. It is built
// from a YAML file plus environment overrides and validated before
// use; the validated Config is the only input to New.
type Config struct {
	ExecutorID       string        `yaml:"executor_id"`
	ExecutorAPIBind  string        `yaml:"executor_api_bind"`
	MaxPods          int           `yaml:"executor_max_pods"`
	OpenHandsImage   string        `yaml:"openhands_image"`
	OpenHandsAPIKey  string        `yaml:"openhands_api_key"`
	OpenHandsPort    int           `yaml:"openhands_port"`
	ImagePullPolicy  string        `yaml:"image_pull_policy"`
	LogLevel         string        `yaml:"log_level"`
	StateRegistryURL string        `yaml:"state_registry_url"`
	Scope            string        `yaml:"scope"`
	TeamID           string        `yaml:"team_id"`
	AuthorizedTag    string        `yaml:"authorized_tag"`
	PollInterval     time.Duration `yaml:"poll_interval"`
	Namespace        string        `yaml:"namespace"`
	ServiceAccount   string        `yaml:"service_account"`
	StorageClassName string        `yaml:"storage_class_name"`
	CacheDir         string        `yaml:"cache_dir"`

	// Legacy State Registry TLS material. Accepted for staged
	// configuration cleanup but never read: the backend transport
	// is plaintext after v0009. The Executor's HTTP client never
	// constructs a tls.Config; the values below stay as no-op
	// compatibility inputs.
	StateRegistryTLSCertPath     string `yaml:"state_registry_tls_client_cert"`
	StateRegistryTLSKeyPath      string `yaml:"state_registry_tls_client_key"`
	StateRegistryTLSServerCAPath string `yaml:"state_registry_tls_server_ca"`

	// v0005 cleanup delays (each default 0s, each non-negative).
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

// CleanupIDPath returns the configured path for the stable cleanup identity.
func (c *Config) CleanupIDPath() string { return CleanupIdentity() }

// LoadConfig builds a Config from YAML + env vars. The defaults
// match the E2E harness configuration so the in-cluster Deployment
// has zero unexpected values when launched.
func LoadConfig(yamlPath string) (*Config, error) {
	cfg := &Config{
		ExecutorAPIBind:      "127.0.0.1:8030",
		MaxPods:              2,
		OpenHandsImage:       "ghcr.io/openhands/agent-server:latest-python",
		OpenHandsPort:        8000,
		ImagePullPolicy:      "if-not-present",
		LogLevel:             "info",
		PollInterval:         2 * time.Second,
		Namespace:            "flowai-executor-k8s",
		ServiceAccount:       "executor-k8s-openhands",
		StorageClassName:     "flowai-local-path",
		OpenHandsWorkspace:   "/workspace/project",
		OpenHandsLLMUsageID:  "flowai-executor",
		OpenHandsInitialRun:  true,
		FinishedCleanupDelay: 0,
		FailedCleanupDelay:   0,
	}
	if yamlPath != "" {
		_ = configLoadYAML(yamlPath, cfg)
	}
	overrideEnv(cfg)
	if cfg.CacheDir == "" {
		cfg.CacheDir = CacheDirPath()
	}
	if cfg.OpenHandsAPIKey == "" {
		cfg.OpenHandsAPIKey = "flowai-" + uuid.NewString()
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate enforces invariants. Returns nil if all checks pass.
func (c *Config) Validate() error {
	if c.MaxPods < 1 {
		return errors.New("EXECUTOR_MAX_PODS must be >= 1")
	}
	if c.StateRegistryURL == "" {
		return errors.New("EXECUTOR_STATE_REGISTRY_URL is required")
	}
	switch c.Scope {
	case platform.ExecutorScopeTeam:
		if c.TeamID == "" {
			return errors.New("EXECUTOR_TEAM_ID is required for team scope")
		}
	case platform.ExecutorScopeSystem:
		if c.TeamID != "" {
			return errors.New("EXECUTOR_TEAM_ID must be empty for system scope")
		}
	default:
		return errors.New("EXECUTOR_SCOPE must be team or system")
	}
	if c.AuthorizedTag == "" {
		return errors.New("EXECUTOR_AUTHORIZED_TAG must be non-empty")
	}
	if c.Namespace == "" {
		return errors.New("EXECUTOR_NAMESPACE is required")
	}
	if c.ServiceAccount == "" {
		return errors.New("EXECUTOR_SERVICE_ACCOUNT is required")
	}
	if c.StorageClassName == "" {
		return errors.New("EXECUTOR_STORAGE_CLASS_NAME is required")
	}
	if c.CacheDir == "" {
		return errors.New("EXECUTOR_CACHE_DIR is required")
	}
	stateRegistryURL, err := url.Parse(c.StateRegistryURL)
	if err != nil || (stateRegistryURL.Scheme != "http" && stateRegistryURL.Scheme != "https") || stateRegistryURL.Hostname() == "" {
		return fmt.Errorf("EXECUTOR_STATE_REGISTRY_URL must use http or https scheme, got %q", c.StateRegistryURL)
	}
	if c.OpenHandsWorkspace == "" {
		return errors.New("OPENHANDS_WORKSPACE must be non-empty")
	}
	hasProfile := c.OpenHandsAgentProfile != ""
	hasInline := c.OpenHandsLLMModel != "" && c.OpenHandsLLMAPIKey != "" && c.OpenHandsLLMUsageID != ""
	if !hasProfile && !hasInline {
		return errors.New("OpenHands V1 requires either OPENHANDS_AGENT_PROFILE_ID " +
			"or (OPENHANDS_LLM_MODEL + OPENHANDS_LLM_API_KEY + OPENHANDS_LLM_USAGE_ID)")
	}
	if c.FinishedCleanupDelay < 0 {
		return errors.New("EXECUTOR_FINISHED_CLEANUP_DELAY must be >= 0")
	}
	if c.FailedCleanupDelay < 0 {
		return errors.New("EXECUTOR_FAILED_CLEANUP_DELAY must be >= 0")
	}
	return nil
}

func overrideEnv(cfg *Config) {
	cfg.ExecutorAPIBind = envOr("EXECUTOR_API_BIND", cfg.ExecutorAPIBind)
	cfg.MaxPods = envInt("EXECUTOR_MAX_PODS", cfg.MaxPods)
	cfg.OpenHandsImage = envOr("OPENHANDS_IMAGE", cfg.OpenHandsImage)
	cfg.OpenHandsAPIKey = envOr("OPENHANDS_API_KEY", cfg.OpenHandsAPIKey)
	cfg.ImagePullPolicy = envOr("IMAGE_PULL_POLICY", cfg.ImagePullPolicy)
	cfg.LogLevel = envOr("LOG_LEVEL", cfg.LogLevel)
	cfg.StateRegistryURL = envOr("EXECUTOR_STATE_REGISTRY_URL", cfg.StateRegistryURL)
	cfg.Scope = envOr("EXECUTOR_SCOPE", cfg.Scope)
	cfg.TeamID = envOr("EXECUTOR_TEAM_ID", cfg.TeamID)
	cfg.AuthorizedTag = envOr("EXECUTOR_AUTHORIZED_TAG", cfg.AuthorizedTag)
	cfg.PollInterval = envDur("EXECUTOR_POLL_INTERVAL", cfg.PollInterval)
	cfg.Namespace = envOr("EXECUTOR_NAMESPACE", cfg.Namespace)
	cfg.ServiceAccount = envOr("EXECUTOR_SERVICE_ACCOUNT", cfg.ServiceAccount)
	cfg.StorageClassName = envOr("EXECUTOR_STORAGE_CLASS_NAME", cfg.StorageClassName)
	cfg.CacheDir = envOr("EXECUTOR_CACHE_DIR", cfg.CacheDir)
	cfg.OpenHandsWorkspace = envOr("OPENHANDS_WORKSPACE", cfg.OpenHandsWorkspace)
	cfg.OpenHandsLLMModel = envOr("OPENHANDS_LLM_MODEL", cfg.OpenHandsLLMModel)
	cfg.OpenHandsLLMAPIKey = envOr("OPENHANDS_LLM_API_KEY", cfg.OpenHandsLLMAPIKey)
	cfg.OpenHandsLLMBaseURL = envOr("OPENHANDS_LLM_BASE_URL", cfg.OpenHandsLLMBaseURL)
	cfg.OpenHandsLLMUsageID = envOr("OPENHANDS_LLM_USAGE_ID", cfg.OpenHandsLLMUsageID)
	cfg.OpenHandsAgentProfile = envOr("OPENHANDS_AGENT_PROFILE_ID", cfg.OpenHandsAgentProfile)
	cfg.OpenHandsInitialRun = envBool("OPENHANDS_INITIAL_RUN", cfg.OpenHandsInitialRun)
	cfg.FinishedCleanupDelay = envDur("EXECUTOR_FINISHED_CLEANUP_DELAY", cfg.FinishedCleanupDelay)
	cfg.FailedCleanupDelay = envDur("EXECUTOR_FAILED_CLEANUP_DELAY", cfg.FailedCleanupDelay)
}

// State enumerates the high-level Executor states surfaced to the
// readiness probe and the audit log.
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

// Executor is the v0005 K8s Executor's main state machine. The
// zero value is unusable; construct via New.
type Executor struct {
	cfg *Config

	state atomic.Value

	// muCounts guards busy/idle transitions.
	muCounts sync.Mutex
	lastSeen map[string]uint8

	// mu protects runtime identity.
	mu               sync.Mutex
	pods             map[string]*podSlot
	slotReservations int

	registry *stateregistryclient.Client
	kube     k8sclient.Client
	cache    *cache.Store
	lock     *cache.LockFile

	logger func(format string, args ...any)
}

// New constructs an Executor. registry and kube are required; cache
// and lock may be nil when the harness wants to exercise only the
// lifecycle path. logger is optional.
func New(cfg *Config, registry *stateregistryclient.Client, kube k8sclient.Client, store *cache.Store, lock *cache.LockFile, logger func(string, ...any)) *Executor {
	if logger == nil {
		logger = func(string, ...any) {}
	}
	e := &Executor{
		cfg:      cfg,
		registry: registry,
		kube:     kube,
		cache:    store,
		lock:     lock,
		logger:   logger,
		lastSeen: map[string]uint8{},
		pods:     map[string]*podSlot{},
	}
	e.state.Store(StateStarting)
	return e
}

// State returns the current Executor state.
func (e *Executor) State() State { return e.state.Load().(State) }

// Config returns the configuration the Executor was built with.
func (e *Executor) Config() *Config { return e.cfg }

// ExecutorID returns the current cached or freshly registered identity.
func (e *Executor) ExecutorID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg.ExecutorID
}

// CachedExecutorID returns the State Registry-generated executor_id
// stored in the recovery cache. An empty string indicates a fresh
// cache that has never completed first-start registration.
func (e *Executor) CachedExecutorID() string {
	if e.cache == nil {
		return ""
	}
	id, err := e.cache.ExecutorID(context.Background())
	if err != nil {
		return ""
	}
	return id
}

func (e *Executor) setState(s State) { e.state.Store(s) }

// podSlot is the in-process observation of one task Pod. The bbolt
// assignments bucket is the durable source of truth.
type podSlot struct {
	taskID         string
	teamID         string
	commandID      string
	podName        string
	podNamespace   string
	podUID         string
	resolvedImage  string
	imageSource    string
	conversationID string
	prompt         string
	lastObserved   time.Time

	acceptedTerminalMu sync.Mutex
	acceptedTerminal   string
	acceptedAt         time.Time

	cancel           context.CancelFunc
	doneCh           chan struct{}
	exitCh           chan struct{}
	restartsObserved int32
}

// configLoadYAML is split out for tests.
func configLoadYAML(path string, cfg *Config) error {
	return config.Load(path, cfg)
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(envLookup(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := strings.TrimSpace(envLookup(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(envLookup(key))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return fallback
}

func envDur(key string, fallback time.Duration) time.Duration {
	if v := strings.TrimSpace(envLookup(key)); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		var s int
		if _, err := fmt.Sscanf(v, "%d", &s); err == nil {
			return time.Duration(s) * time.Second
		}
	}
	return fallback
}

func envLookup(k string) string { return os.Getenv(k) }

// CleanupID returns the stable cleanup identity used to scope
// leftover runtime artefacts during startup.
func (e *Executor) CleanupID() string { return CleanupIdentity() }
