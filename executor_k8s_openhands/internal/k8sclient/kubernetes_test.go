package k8sclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestKubernetesClientCreatePod(t *testing.T) {
	t.Parallel()

	client := testClient(t, func(req *http.Request) *http.Response {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", req.Method)
		}
		if req.URL.Path != "/api/v1/namespaces/tasks/pods" {
			t.Fatalf("path = %s", req.URL.Path)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization = %q", got)
		}
		var pod podObject
		if err := json.NewDecoder(req.Body).Decode(&pod); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if pod.Spec.ServiceAccountName != "task-runner" {
			t.Fatalf("service account = %q", pod.Spec.ServiceAccountName)
		}
		container := pod.Spec.Containers[0]
		if container.Image != "registry.example/agent@sha256:abc" {
			t.Fatalf("image = %q", container.Image)
		}
		if container.ImagePullPolicy != "IfNotPresent" {
			t.Fatalf("pull policy = %q", container.ImagePullPolicy)
		}
		return jsonResponse(http.StatusCreated, `{"metadata":{"name":"task-1","namespace":"tasks","uid":"uid-1"},"spec":{"containers":[]}}`)
	})

	ref, err := client.CreatePod(context.Background(), PodSpec{
		Name: "task-1", Namespace: "tasks", Image: "registry.example/agent@sha256:abc",
		ImagePullPolicy: "if-not-present", ServiceAccountName: "task-runner",
		Labels: map[string]string{LabelTaskID: "task-1"}, Env: map[string]string{"B": "2", "A": "1"},
	})
	if err != nil {
		t.Fatalf("CreatePod: %v", err)
	}
	if *ref != (PodRef{Name: "task-1", Namespace: "tasks", UID: "uid-1"}) {
		t.Fatalf("ref = %#v", ref)
	}
}

func TestKubernetesClientGetPodMapsStatus(t *testing.T) {
	t.Parallel()

	client := testClient(t, func(req *http.Request) *http.Response {
		return jsonResponse(http.StatusOK, `{
          "metadata":{"name":"task-1","namespace":"tasks","uid":"uid-1","labels":{"flowai.task_id":"task-1"}},
          "spec":{"containers":[{"name":"agent","image":"agent:v1"}]},
          "status":{"phase":"Running","startTime":"2026-08-07T00:00:00Z","containerStatuses":[{"name":"agent","ready":true,"restartCount":2,"image":"agent:v1","state":{"running":{}}}]}
        }`)
	})

	pod, err := client.GetPod(context.Background(), "tasks", "task-1")
	if err != nil {
		t.Fatalf("GetPod: %v", err)
	}
	if pod.Status != PodStatusRunning || pod.RestartCount != 2 || pod.Image != "agent:v1" {
		t.Fatalf("pod state = %#v", pod)
	}
	if len(pod.ContainerStatuses) != 1 || pod.ContainerStatuses[0].State != "running" {
		t.Fatalf("container statuses = %#v", pod.ContainerStatuses)
	}
	wantStarted := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	if !pod.StartedAt.Equal(wantStarted) {
		t.Fatalf("started at = %s", pod.StartedAt)
	}
}

func TestKubernetesClientListMatchingPodsUsesStableSelector(t *testing.T) {
	t.Parallel()

	client := testClient(t, func(req *http.Request) *http.Response {
		if got := req.URL.Query().Get("labelSelector"); got != "flowai.executor_id=exec-1,flowai.runtime=k8s" {
			t.Fatalf("selector = %q", got)
		}
		return jsonResponse(http.StatusOK, `{"items":[]}`)
	})

	pods, err := client.ListMatchingPods(context.Background(), "tasks", map[string]string{
		LabelRuntime: "k8s", LabelExecutorID: "exec-1",
	})
	if err != nil {
		t.Fatalf("ListMatchingPods: %v", err)
	}
	if len(pods) != 0 {
		t.Fatalf("pods = %#v", pods)
	}
}

func TestKubernetesClientDeletePodIsIdempotent(t *testing.T) {
	t.Parallel()

	client := testClient(t, func(req *http.Request) *http.Response {
		return jsonResponse(http.StatusNotFound, `{"message":"not found"}`)
	})
	if err := client.DeletePod(context.Background(), "tasks", "missing"); err != nil {
		t.Fatalf("DeletePod: %v", err)
	}
}

func TestKubernetesClientHealthy(t *testing.T) {
	t.Parallel()

	client := testClient(t, func(req *http.Request) *http.Response {
		if req.URL.Path != "/healthz" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		return jsonResponse(http.StatusOK, "ok")
	})
	if !client.Healthy(context.Background()) {
		t.Fatal("Healthy = false")
	}
}

func testClient(t *testing.T, handler func(*http.Request) *http.Response) *KubernetesClient {
	t.Helper()
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return handler(req), nil
	})}
	client, err := NewKubernetesClient("https://kubernetes.test", "test-token", httpClient)
	if err != nil {
		t.Fatalf("NewKubernetesClient: %v", err)
	}
	return client
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
