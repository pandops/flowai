// Package mocks holds deterministic in-memory test doubles for
// the Executor local interfaces. They are colocated with the
// service they fake (AGENTS.md rule) and intentionally own no
// shared code with the Docker Executor.
package k8s

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/flowai/platform/executor/k8s-openhands/internal/k8sclient"
)

// FakeClient is an in-memory k8sclient.Client for tests. Every
// method records into a slice the tests assert against.
type FakeClient struct {
	mu sync.Mutex

	Pods          map[string]*fakePodRecord
	CreateCalls   []k8sclient.PodSpec
	GetCalls      []string
	DeleteCalls   []string
	ListCalls     []map[string]string
	CreateError   error
	GetError      error
	DeleteError   error
	healthy       bool
	WatchListener func(spec k8sclient.PodSpec, ref k8sclient.PodRef)
}

// NewFakeClient constructs an empty FakeClient with Healthy default true.
func NewFakeClient() *FakeClient {
	return &FakeClient{Pods: map[string]*fakePodRecord{}, healthy: true}
}

// SetHealthy toggles the readiness flag.
func (f *FakeClient) SetHealthy(v bool) { f.healthy = v }

// SetCreateError forces the next CreatePod call to fail.
func (f *FakeClient) SetCreateError(err error) { f.mu.Lock(); f.CreateError = err; f.mu.Unlock() }

// SetDeleteError forces the next DeletePod call to fail.
func (f *FakeClient) SetDeleteError(err error) { f.mu.Lock(); f.DeleteError = err; f.mu.Unlock() }

type fakePodRecord struct {
	spec     k8sclient.PodSpec
	ref      k8sclient.PodRef
	status   k8sclient.PodStatus
	restarts int32
	created  time.Time
}

// CreatePod persists the Pod and returns a deterministic PodRef.
func (f *FakeClient) CreatePod(ctx context.Context, spec k8sclient.PodSpec) (*k8sclient.PodRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.CreateCalls = append(f.CreateCalls, spec)
	if f.CreateError != nil {
		return nil, f.CreateError
	}
	uid := fmt.Sprintf("uid-%s-%d", spec.Name, time.Now().UnixNano())
	ref := &k8sclient.PodRef{Name: spec.Name, Namespace: spec.Namespace, UID: uid}
	rec := &fakePodRecord{spec: spec, ref: *ref, status: k8sclient.PodStatusRunning, created: time.Now().UTC()}
	f.Pods[spec.Name] = rec
	if f.WatchListener != nil {
		f.WatchListener(spec, *ref)
	}
	return ref, nil
}

// GetPod returns a snapshot of a single Pod by composite identity.
func (f *FakeClient) GetPod(ctx context.Context, namespace, name string) (*k8sclient.PodState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.GetCalls = append(f.GetCalls, name)
	if f.GetError != nil {
		return nil, f.GetError
	}
	rec, ok := f.Pods[name]
	if !ok {
		return nil, fmt.Errorf("pod %s/%s not found", namespace, name)
	}
	return &k8sclient.PodState{
		Ref:          rec.ref,
		Status:       rec.status,
		Image:        rec.spec.Image,
		Labels:       cloneLabels(rec.spec.Labels),
		RestartCount: rec.restarts,
		StartedAt:    rec.created,
	}, nil
}

// DeletePod removes a Pod and records the call.
func (f *FakeClient) DeletePod(ctx context.Context, namespace, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.DeleteCalls = append(f.DeleteCalls, name)
	if f.DeleteError != nil {
		// Clear one-shot error so subsequent calls succeed.
		err := f.DeleteError
		f.DeleteError = nil
		return err
	}
	delete(f.Pods, name)
	return nil
}

// ListMatchingPods returns every Pod whose labels contain every
// entry in the supplied selector (subset semantics).
func (f *FakeClient) ListMatchingPods(ctx context.Context, namespace string, selector map[string]string) ([]k8sclient.PodState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ListCalls = append(f.ListCalls, cloneLabels(selector))
	var out []k8sclient.PodState
	for _, rec := range f.Pods {
		if rec.spec.Namespace != namespace {
			continue
		}
		if !labelsContainAll(rec.spec.Labels, selector) {
			continue
		}
		out = append(out, k8sclient.PodState{
			Ref:          rec.ref,
			Status:       rec.status,
			Image:        rec.spec.Image,
			Labels:       cloneLabels(rec.spec.Labels),
			RestartCount: rec.restarts,
			StartedAt:    rec.created,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref.Name < out[j].Ref.Name })
	return out, nil
}

// WatchPod keeps an update stream open until cancellation; the fake never
// produces live updates unless a test uses a more specialised client.
func (f *FakeClient) WatchPod(ctx context.Context, namespace, name string) (<-chan k8sclient.PodState, func(), error) {
	if _, ok := f.Pods[name]; !ok {
		return nil, nil, fmt.Errorf("pod %s/%s not found", namespace, name)
	}
	watchCtx, cancel := context.WithCancel(ctx)
	ch := make(chan k8sclient.PodState)
	go func() {
		<-watchCtx.Done()
		close(ch)
	}()
	return ch, cancel, nil
}

// Healthy reports the in-memory healthy flag.
func (f *FakeClient) Healthy(ctx context.Context) bool { return f.healthy }

// IncrementRestart bumps the restart count for a Pod, used by tests
// to drive the `pod_restarted_before_finish` failure path.
func (f *FakeClient) IncrementRestart(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if rec, ok := f.Pods[name]; ok {
		rec.restarts++
	}
}

// SetStatus overrides the Pod status observed via GetPod.
func (f *FakeClient) SetStatus(name string, status k8sclient.PodStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if rec, ok := f.Pods[name]; ok {
		rec.status = status
	}
}

// PodSpec returns the spec used to create a Pod.
func (f *FakeClient) PodSpec(name string) (k8sclient.PodSpec, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.Pods[name]
	if !ok {
		return k8sclient.PodSpec{}, false
	}
	return rec.spec, true
}

// PodsSnapshot returns a deep copy of the recorded Pods for
// fixture-free assertions.
func (f *FakeClient) PodsSnapshot() []k8sclient.PodState {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]k8sclient.PodState, 0, len(f.Pods))
	for _, rec := range f.Pods {
		out = append(out, k8sclient.PodState{
			Ref:          rec.ref,
			Status:       rec.status,
			Image:        rec.spec.Image,
			Labels:       cloneLabels(rec.spec.Labels),
			RestartCount: rec.restarts,
			StartedAt:    rec.created,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref.Name < out[j].Ref.Name })
	return out
}

// Compile-time interface check.
var _ k8sclient.Client = (*FakeClient)(nil)

// ErrNotFound is returned when a requested Pod is absent.
var ErrNotFound = errors.New("k8sclient: pod not found")

func cloneLabels(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func labelsContainAll(have, want map[string]string) bool {
	for k, v := range want {
		if got, ok := have[k]; !ok || got != v {
			return false
		}
	}
	return true
}
