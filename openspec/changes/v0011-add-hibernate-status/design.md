## Context

OpenHands can recover a conversation from its persisted session directory.
Hibernate must stop execution immediately, preserve that directory outside the
runtime, and release compute resources. It never retains a live container or Pod
for direct continuation. Executors and API Gateway must still talk only to State
Registry.

## Goals / Non-Goals

**Goals:**

- Make `hibernate` mean a complete, integrity-checked session archive is durable
  and the original runtime has been released.
- Allow an operator or system administrator to hibernate eligible work.
- Let an authorized user start a later continuation run with a new query.

**Non-Goals:**

- Retain a live runtime for direct continuation.
- Resume the original task row or retain its runtime resources.
- Expose archives to browsers/Gateway or add a FlowAI storage service.

## Decisions

1. Hibernate is accepted for eligible `running` work. The Executor immediately
   stops agent execution and begins session preservation.
2. State Registry owns archive metadata and a private separate blob-storage
   adapter. Executors upload/download only through authenticated Registry APIs,
   preserving the connection matrix.
3. The hibernate control remains `in_progress` while the snapshot uploads. The
   task projects to terminal `hibernate` only after archive bytes, versioned
   manifest, byte count, digest, conversation identifier, and locator commit.
   Storage failure projects terminal `failed`; cleanup and capacity release occur
   in either outcome.
4. Later run is an authorized action with a non-empty query and idempotency key.
   Registry creates a new `pending` task with immutable
   `hibernated_from_task_id`; the original remains `hibernate`.
5. On claim, Registry gives the assigned Executor a short-lived archive handle.
   The Executor verifies and safely extracts into a fresh workspace, restores
   the preserved conversation, then submits the new query.
6. Team operators act only on same-team tasks. System administrators may
   hibernate or start later runs across teams through the authenticated admin
   path. Both are fully audited without archive contents.

## Risks / Trade-offs

- [Archive contains secrets] → Encrypt storage and transport, authorize every
  transfer canonically, and exclude archive data/locators from logs and APIs.
- [Process dies during upload] → Use resumable/abortable uploads; never project
  hibernate until the complete digest-verified object commits.
- [Unsafe archive paths] → Reject traversal, links, devices, unsupported versions,
  and configured size/count violations before extraction.
- [Later-run duplicates] → Scope idempotency to actor, team, hibernated task, and
  key; create exactly one continuation task.

## Migration Plan

1. Deploy Registry schema, authorization, private storage adapter, and disabled
   hibernate/later-run routes.
2. Deploy Executors with stop/snapshot/upload and restore support.
3. Enable hibernate, then enable Web UI/Gateway later-run actions.
4. Roll back action exposure first; retain committed archives and metadata until
   an explicit retention decision removes them.

## Open Questions

None.
