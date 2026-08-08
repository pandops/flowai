// Package k8sclient is the v0005 concrete K8s Executor client to
// the Kubernetes API. The Executor owns a narrow interface here so
// the pod-lifecycle code stays testable behind an in-memory fake.
//
// The Executor runs INSIDE Kubernetes in production E2E. In every
// environment the K8s API base URL is irrelevant; the v0005 client
// uses an in-cluster configuration when run inside a Pod and an
// explicit kubeconfig path when run locally. The deployment
// network policy owns the caller boundary; the K8s API server
// authenticates the ServiceAccount token at the documented
// namespace scope and rejects everything else.
package k8sclient

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// PodSpec is the per-task Pod record the Executor wants the K8s
// API to materialize. The Executor never starts a Pod before a
// successful 200 claim response from the State Registry; the
// ResolvedImage is the v0005 four-level-precedence value, used
// verbatim.
type PodSpec struct {
	Name               string
	Namespace          string
	Image              string
	ImagePullPolicy    string
	Command            []string
	Args               []string
	Env                map[string]string
	Port               int
	ExecutorID         string
	TaskID             string
	CommandID          string
	TeamID             string
	Scope              string
	ResolvedImageSrc   string
	Runtime            string // "k8s"
	ServiceAccountName string
	Labels             map[string]string
}

// PodStatus enumerates the coarse Pod states the Executor tracks.
type PodStatus string

const (
	PodStatusUnknown   PodStatus = "unknown"
	PodStatusPending   PodStatus = "pending"
	PodStatusRunning   PodStatus = "running"
	PodStatusSucceeded PodStatus = "succeeded"
	PodStatusFailed    PodStatus = "failed"
)

// PodRef is the canonical K8s identity the Executor persists in the
// recovery cache and uses for restart reconciliation.
type PodRef struct {
	Name      string
	Namespace string
	UID       string
}

// Client is the narrow K8s interface the executor package consumes.
// The production implementation wraps the K8s typed client; the
// test implementation drives a deterministic in-memory state
// machine that captures every call for assertion.
type Client interface {
	CreatePod(ctx context.Context, spec PodSpec) (*PodRef, error)
	GetPod(ctx context.Context, namespace, name string) (*PodState, error)
	DeletePod(ctx context.Context, namespace, name string) error
	ListMatchingPods(ctx context.Context, namespace string, labels map[string]string) ([]PodState, error)
	WatchPod(ctx context.Context, namespace, name string) (<-chan PodState, func(), error)
	Healthy(ctx context.Context) bool
}

// PodState is the read-side K8s observation the Executor mirrors
// onto its local cache. UID is the canonical Pod identifier the
// Executor uses for restart reconciliation.
type PodState struct {
	Ref               PodRef
	PodIP             string
	Status            PodStatus
	Image             string
	Labels            map[string]string
	RestartCount      int32
	ContainerStatuses []ContainerStatus
	StartedAt         time.Time
}

// ContainerStatus mirrors the K8s container status the Executor
// uses to detect agent-container restarts before OpenHands
// reports terminal execution_status.
type ContainerStatus struct {
	Name         string
	Ready        bool
	RestartCount int32
	Image        string
	State        string
}

// Label constants applied by the K8s Executor. They are part of the
// v0005 wire contract and must remain stable across releases.
const (
	LabelExecutorID          = "flowai.executor_id"
	LabelTeamID              = "flowai.team_id"
	LabelTaskID              = "flowai.task_id"
	LabelCommandID           = "flowai.command_id"
	LabelExecutorScope       = "flowai.executor_scope"
	LabelResolvedImageSource = "flowai.resolved_image_source"
	LabelRuntime             = "flowai.runtime"
)

// BuildTaskLabels returns the canonical v0005 label set every task
// Pod MUST carry. Callers pass the Executor-owned identity and the
// immutable claim record; the function returns a fresh map so the
// Executor never mutates the shared identity state.
func BuildTaskLabels(executorID, teamID, taskID, commandID, scope, imageSource string) map[string]string {
	return map[string]string{
		LabelExecutorID:          executorID,
		LabelTeamID:              teamID,
		LabelTaskID:              taskID,
		LabelCommandID:           commandID,
		LabelExecutorScope:       scope,
		LabelResolvedImageSource: imageSource,
		LabelRuntime:             "k8s",
	}
}

// NewHTTPHealthClient returns an HTTP-only client used by the
// readiness probe to confirm the K8s API server is reachable. The
// K8s API server is the documented caller boundary; the network
// policy in front of the API server is configured by the
// deployment, not by the Executor. The constructor returns nil
// when the base URL is empty so a localhost harness can skip the
// check entirely.
func NewHTTPHealthClient(baseURL string) *HTTPHealthClient {
	if baseURL == "" {
		return nil
	}
	return &HTTPHealthClient{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 2 * time.Second},
	}
}

// HTTPHealthClient pings a K8s API server base URL via plain HTTP
// for readiness reporting only. It carries no service-account
// token and never performs mutating calls.
type HTTPHealthClient struct {
	baseURL string
	client  *http.Client
}

// Healthy returns whether the K8s API server responds with a 2xx
// or 401/403 status (the latter proves the server is reachable
// even when the requesting identity is rejected by RBAC).
func (h *HTTPHealthClient) Healthy(ctx context.Context) bool {
	if h == nil || h.client == nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.baseURL+"/healthz", nil)
	if err != nil {
		return false
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusUnauthorized, http.StatusForbidden:
		return true
	}
	return false
}

// ErrUnsupported is returned when an operation is rejected because
// no production K8s client is configured (e.g. inside a unit test).
var ErrUnsupported = errors.New("k8sclient: no production client configured")
