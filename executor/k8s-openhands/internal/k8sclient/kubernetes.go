package k8sclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

const (
	serviceAccountTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	serviceAccountCAPath    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
)

// KubernetesClient implements Client directly against the Kubernetes core/v1
// REST API. Keeping the adapter narrow avoids exposing Kubernetes types to the
// Executor state machine.
type KubernetesClient struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewKubernetesClient constructs a client with an injected HTTP transport.
// It is primarily useful for tests and explicit out-of-cluster wiring.
func NewKubernetesClient(baseURL, token string, client *http.Client) (*KubernetesClient, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("parse kubernetes API URL %q: %w", baseURL, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("parse kubernetes API URL %q: absolute URL is required", baseURL)
	}
	if client == nil {
		return nil, errors.New("kubernetes HTTP client is required")
	}
	return &KubernetesClient{baseURL: u.String(), token: strings.TrimSpace(token), client: client}, nil
}

// NewInClusterClient loads the mounted ServiceAccount token and CA bundle.
func NewInClusterClient() (*KubernetesClient, error) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT_HTTPS")
	if port == "" {
		port = "443"
	}
	if host == "" {
		return nil, errors.New("KUBERNETES_SERVICE_HOST is required")
	}
	token, err := os.ReadFile(serviceAccountTokenPath)
	if err != nil {
		return nil, fmt.Errorf("read service account token: %w", err)
	}
	caPEM, err := os.ReadFile(serviceAccountCAPath)
	if err != nil {
		return nil, fmt.Errorf("read service account CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("parse service account CA")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	return NewKubernetesClient("https://"+host+":"+port, string(token), &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	})
}

func (c *KubernetesClient) CreatePod(ctx context.Context, spec PodSpec) (*PodRef, error) {
	body := podObject{
		APIVersion: "v1",
		Kind:       "Pod",
		Metadata:   objectMeta{Name: spec.Name, Namespace: spec.Namespace, Labels: spec.Labels},
		Spec: podObjectSpec{
			ServiceAccountName: spec.ServiceAccountName,
			RestartPolicy:      "Never",
			Containers: []containerSpec{{
				Name:            "agent",
				Image:           spec.Image,
				ImagePullPolicy: normalizePullPolicy(spec.ImagePullPolicy),
				Command:         spec.Command,
				Args:            spec.Args,
				Env:             envVars(spec.Env),
				Ports:           containerPorts(spec.Port),
			}},
		},
	}
	var created podObject
	if err := c.doJSON(ctx, http.MethodPost, podsPath(spec.Namespace), body, &created); err != nil {
		return nil, fmt.Errorf("create pod %s/%s: %w", spec.Namespace, spec.Name, err)
	}
	return &PodRef{Name: created.Metadata.Name, Namespace: created.Metadata.Namespace, UID: created.Metadata.UID}, nil
}

func (c *KubernetesClient) GetPod(ctx context.Context, namespace, name string) (*PodState, error) {
	var pod podObject
	if err := c.doJSON(ctx, http.MethodGet, podPath(namespace, name), nil, &pod); err != nil {
		return nil, fmt.Errorf("get pod %s/%s: %w", namespace, name, err)
	}
	state := podState(pod)
	return &state, nil
}

func (c *KubernetesClient) DeletePod(ctx context.Context, namespace, name string) error {
	err := c.doJSON(ctx, http.MethodDelete, podPath(namespace, name), nil, nil)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete pod %s/%s: %w", namespace, name, err)
	}
	return nil
}

func (c *KubernetesClient) ListMatchingPods(ctx context.Context, namespace string, labels map[string]string) ([]PodState, error) {
	selector := make([]string, 0, len(labels))
	for key, value := range labels {
		selector = append(selector, key+"="+value)
	}
	sort.Strings(selector)
	u := podsPath(namespace)
	if len(selector) > 0 {
		u += "?labelSelector=" + url.QueryEscape(strings.Join(selector, ","))
	}
	var list podList
	if err := c.doJSON(ctx, http.MethodGet, u, nil, &list); err != nil {
		return nil, fmt.Errorf("list pods in %s: %w", namespace, err)
	}
	out := make([]PodState, 0, len(list.Items))
	for _, pod := range list.Items {
		out = append(out, podState(pod))
	}
	return out, nil
}

func (c *KubernetesClient) WatchPod(ctx context.Context, namespace, name string) (<-chan PodState, func(), error) {
	watchCtx, cancel := context.WithCancel(ctx)
	updates := make(chan PodState, 1)
	go func() {
		defer close(updates)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			pod, err := c.GetPod(watchCtx, namespace, name)
			if err == nil {
				select {
				case updates <- *pod:
				case <-watchCtx.Done():
					return
				}
			}
			select {
			case <-ticker.C:
			case <-watchCtx.Done():
				return
			}
		}
	}()
	return updates, cancel, nil
}

func (c *KubernetesClient) Healthy(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return false
	}
	c.authorize(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// APIError preserves the Kubernetes response status and bounded response body.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("kubernetes API returned %d: %s", e.StatusCode, e.Body)
}

func (c *KubernetesClient) doJSON(ctx context.Context, method, requestPath string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+requestPath, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	c.authorize(req)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	if output == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(output); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (c *KubernetesClient) authorize(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

func podsPath(namespace string) string {
	return "/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods"
}

func podPath(namespace, name string) string {
	return path.Join(podsPath(namespace), url.PathEscape(name))
}

func normalizePullPolicy(policy string) string {
	switch strings.ToLower(policy) {
	case "always":
		return "Always"
	case "never":
		return "Never"
	default:
		return "IfNotPresent"
	}
}

func envVars(values map[string]string) []envVar {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]envVar, 0, len(keys))
	for _, key := range keys {
		out = append(out, envVar{Name: key, Value: values[key]})
	}
	return out
}

func containerPorts(port int) []containerPort {
	if port <= 0 {
		return nil
	}
	return []containerPort{{Name: "http", ContainerPort: port}}
}

func podState(pod podObject) PodState {
	statuses := make([]ContainerStatus, 0, len(pod.Status.ContainerStatuses))
	var restarts int32
	for _, status := range pod.Status.ContainerStatuses {
		restarts += status.RestartCount
		statuses = append(statuses, ContainerStatus{
			Name: status.Name, Ready: status.Ready, RestartCount: status.RestartCount,
			Image: status.Image, State: containerStateName(status.State),
		})
	}
	return PodState{
		Ref:    PodRef{Name: pod.Metadata.Name, Namespace: pod.Metadata.Namespace, UID: pod.Metadata.UID},
		PodIP:  pod.Status.PodIP,
		Status: mapPodPhase(pod.Status.Phase), Image: firstContainerImage(pod.Spec.Containers),
		Labels: pod.Metadata.Labels, RestartCount: restarts, ContainerStatuses: statuses,
		StartedAt: pod.Status.StartTime,
	}
}

func mapPodPhase(phase string) PodStatus {
	switch phase {
	case "Pending":
		return PodStatusPending
	case "Running":
		return PodStatusRunning
	case "Succeeded":
		return PodStatusSucceeded
	case "Failed":
		return PodStatusFailed
	default:
		return PodStatusUnknown
	}
}

func firstContainerImage(containers []containerSpec) string {
	if len(containers) == 0 {
		return ""
	}
	return containers[0].Image
}

func containerStateName(state containerState) string {
	switch {
	case state.Running != nil:
		return "running"
	case state.Waiting != nil:
		return "waiting"
	case state.Terminated != nil:
		return "terminated"
	default:
		return "unknown"
	}
}

type podObject struct {
	APIVersion string        `json:"apiVersion,omitempty"`
	Kind       string        `json:"kind,omitempty"`
	Metadata   objectMeta    `json:"metadata"`
	Spec       podObjectSpec `json:"spec"`
	Status     podStatus     `json:"status,omitempty"`
}
type podList struct {
	Items []podObject `json:"items"`
}
type objectMeta struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace,omitempty"`
	UID       string            `json:"uid,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}
type podObjectSpec struct {
	ServiceAccountName string          `json:"serviceAccountName,omitempty"`
	RestartPolicy      string          `json:"restartPolicy"`
	Containers         []containerSpec `json:"containers"`
}
type containerSpec struct {
	Name            string          `json:"name"`
	Image           string          `json:"image"`
	ImagePullPolicy string          `json:"imagePullPolicy,omitempty"`
	Command         []string        `json:"command,omitempty"`
	Args            []string        `json:"args,omitempty"`
	Env             []envVar        `json:"env,omitempty"`
	Ports           []containerPort `json:"ports,omitempty"`
}
type envVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type containerPort struct {
	Name          string `json:"name,omitempty"`
	ContainerPort int    `json:"containerPort"`
}
type podStatus struct {
	Phase             string                `json:"phase"`
	PodIP             string                `json:"podIP,omitempty"`
	StartTime         time.Time             `json:"startTime,omitempty"`
	ContainerStatuses []wireContainerStatus `json:"containerStatuses,omitempty"`
}
type wireContainerStatus struct {
	Name         string         `json:"name"`
	Ready        bool           `json:"ready"`
	RestartCount int32          `json:"restartCount"`
	Image        string         `json:"image"`
	State        containerState `json:"state"`
}
type containerState struct {
	Running    map[string]any `json:"running,omitempty"`
	Waiting    map[string]any `json:"waiting,omitempty"`
	Terminated map[string]any `json:"terminated,omitempty"`
}

var _ Client = (*KubernetesClient)(nil)
