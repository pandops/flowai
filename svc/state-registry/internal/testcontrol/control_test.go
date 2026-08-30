package testcontrol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOneShotBarrierLifecycle(t *testing.T) {
	coordinator := New(time.Second)
	server := httptest.NewServer(Handler(coordinator, "secret"))
	defer server.Close()
	request := func(method, path, body, token string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	if response := request("POST", "/test-control/v1/barriers", `{"name":"team_archive_after_lock"}`, "wrong"); response.StatusCode != 401 {
		t.Fatalf("wrong token status=%d", response.StatusCode)
	}
	armed := request("POST", "/test-control/v1/barriers", `{"name":"team_archive_after_lock"}`, "secret")
	if armed.StatusCode != 201 {
		t.Fatalf("arm status=%d", armed.StatusCode)
	}
	var barrier Barrier
	if err := jsonNewDecoder(armed).Decode(&barrier); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { coordinator.Wait(context.Background(), barrier.Name); close(done) }()
	deadline := time.Now().Add(time.Second)
	for {
		current, ok := coordinator.Get(context.Background(), barrier.ID, 0)
		if ok && current.State == "reached" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("barrier did not reach")
		}
		time.Sleep(time.Millisecond)
	}
	released := request("POST", "/test-control/v1/barriers/"+barrier.ID+"/release", "", "secret")
	if released.StatusCode != 200 {
		t.Fatalf("release status=%d", released.StatusCode)
	}
	<-done
	if response := request("DELETE", "/test-control/v1/barriers/"+barrier.ID, "", "secret"); response.StatusCode != 204 {
		t.Fatalf("delete status=%d", response.StatusCode)
	}
	if response := request("GET", "/test-control/v1/barriers/"+barrier.ID, "", "secret"); response.StatusCode != 404 {
		t.Fatalf("reused status=%d", response.StatusCode)
	}
}

func jsonNewDecoder(response *http.Response) *json.Decoder { return json.NewDecoder(response.Body) }
