# K8s OpenHands Executor

`executor_k8s_openhands` is the v0005 concrete K8s worker. State Registry
owns all canonical platform state.

## Runtime flow

1. Validate the backend HTTP State Registry URL and immutable scope inputs.
2. Open the bbolt recovery cache at `<cache_dir>/executor.db` with versioned
   metadata, assignments, and event_outbox buckets.
3. Acquire the non-blocking exclusive OS file lock on
   `<cache_dir>/executor.lock`; lock contention fails closed without
   Registry or runtime mutation.
4. Register exactly one immutable scope (`team` or `system`), one
   `authorized_tag`, observed `max_capacity`, observed `running_count`,
   runtime metadata. Re-registration is idempotent under the same UUID.
5. Discover eligible `pending` tasks via team-scoped or system-wide FIFO
   discovery.
6. Claim the oldest eligible `pending` task with a stable `command_id`;
   persist the claim intent in `assignments` before the HTTP call.
7. Use the claim's `resolved_image` verbatim; no local fallback exists.
8. Emit the `running` event AFTER `200 claimed` and BEFORE creating the
   Kubernetes Pod.
9. Create the Pod carrying the documented v0005 label set
   (`flowai.executor_id`, `flowai.team_id`, `flowai.task_id`,
   `flowai.command_id`, `flowai.executor_scope`,
   `flowai.resolved_image_source`, `flowai.runtime=k8s`).
10. Open the task environment via the State Registry-issued compact
    three-part HMAC scope token; never log plaintext.
11. Append exactly one terminal `finished` or `failed` event from the
    OpenHands conversation terminal status; honour
    `finished_cleanup_delay` / `failed_cleanup_delay` after 202; delete
    the Pod idempotently.
12. Read pending controls only for assigned tasks in the bound team;
    poll the State Registry for control records.

The Executor never contacts a mocked task server, Router, API Gateway, Web
UI, or separate Env Registry.

## Cache contract

- `<cache_dir>/executor.db` carries three versioned bbolt buckets:
  `metadata` (schema + executor_id), `assignments` (per-task recovery),
  `event_outbox` (delivery state for pending/accepted events).
- `<cache_dir>/executor.lock` is a non-blocking exclusive OS file lock
  held for the process lifetime.
- An unknown newer schema version, open failure, or corrupt database
  fails closed; the Executor reports unhealthy without claiming work
  or mutating Pods.
- Environment or secret plaintext is never stored; cache and logs hold
  only identifiers and opaque envelopes.

## Backend transport

After v0009 the backend transport is plain HTTP. External HTTPS
terminates at the Ingress. The deployment network policy owns the caller
boundary; the Executor's HTTP client never constructs a `tls.Config`.
Legacy backend TLS material fields (`state_registry_tls_client_cert`,
`state_registry_tls_client_key`, `state_registry_tls_server_ca`) are
accepted for staged configuration cleanup and never read.

## Restart reconciliation

On every restart the Executor:

1. Takes the OS file lock on the same cache volume.
2. Re-registers with the same `team_id`, `authorized_tag`, and
   cached `executor_id` via `PUT /v1/executors/{executor_id}`.
3. Loads claimed non-terminal tasks exclusively from the bbolt
   `assignments` bucket.
4. Matches each cached `owner_command_id` to the existing Pod's
   `flowai.command_id` label to identify which in-flight Pod to
   continue observing.
5. Reconnects to the cached OpenHands conversation at its current
   progress.
6. Retries pending outbox events with their original `event_id`.
7. Emits no duplicate `running` event and never creates a duplicate
   Pod for an already-claimed task.

The Executor never asks the Registry for an assignments list; the cache
is the only durable source of restart reconciliation.

Local probes are `GET /v1/livez` and `GET /v1/readyz`.

## Build and run

```bash
GOROOT=/usr/local/go /usr/local/go/bin/go run ./executor/k8s-openhands/cmd/executor_k8s_openhands \
  -config executor/k8s-openhands/configs/executor_k8s_openhands.yaml
GOROOT=/usr/local/go /usr/local/go/bin/go test ./executor/k8s-openhands/...
```

In-cluster: a single-replica Deployment under the
`executor-k8s-openhands` ServiceAccount, mounted with a PVC at
`<cache_dir>`, no overlapping Pod replacement strategy, and the
documented label set on every task Pod.
