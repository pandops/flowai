# ADR: Resolve task image through a four-level precedence with required team default

### Status

Accepted

### Context

Operators currently configure the runtime image per Executor process, which means the choice of image lives outside the canonical State Registry. This is hard to audit, hard to replay, and forces operators to deploy one Executor per image. Operators need both team-wide defaults and per-task, per-task-type, and per-source-system overrides so that a single team can have a team-default image, a task-type default that reflects typical workloads of that type, a per-source-system default that reflects typical tasks from that source, and a one-off per-task override for an unusual job. Image strings are not team-owned resources, and image equality across teams carries no authority.

The four-level precedence is `tasks.image` -> `task_types.default_image` -> `source_systems.default_image` -> `teams.default_image`. Because `teams.default_image` is required at registration, resolution always succeeds; the Executor never maintains or needs a local fallback image.

The platform needs image resolution to be deterministic, recorded in the same transaction as claim, replayable across restarts, and consistent between what State Registry returns and what the assigned Executor actually starts. Image references must not influence authorization, scheduling, priority, or capacity, and must not leak between teams.

### Considered Options

#### Resolve in State Registry at claim with four-level precedence, persist on the canonical task row, eliminate Executor local fallback

State Registry stores an OPTIONAL `image` override per `tasks` row, an OPTIONAL `default_image` per `task_types` row, an OPTIONAL `default_image` per `source_systems` row, and a REQUIRED `default_image` per `teams` row set at `POST /admin/teams`. On atomic claim, State Registry computes the effective image using the precedence listed above, persists `tasks.resolved_image` and `tasks.image_source` in the same transaction that appends the first `created` event, and includes `resolved_image` and `image_source` in the claim response. The Executor uses `resolved_image` verbatim and SHALL NOT consult any other source.

Advantages:

- Image selection lives in the same canonical service that owns tasks, assignments, and audit.
- `tasks.resolved_image` is durable across restarts and is the single source of truth for what the Executor runs.
- The claim response carries the authoritative image, removing any chance of Executor and Registry disagreement.
- Image references never affect authorization, scheduling, priority, or capacity; they participate only in deterministic resolution and the audit trail.
- Cross-team leakage is impossible because resolution runs inside the same team-scoped transaction that already authorizes the claim.
- Image strings carry no team authority because they are not team-owned resources; two teams MAY reference the same image without granting each other authority.
- Removing Executor local fallback removes a class of integration tests and config-drift failures.

Disadvantages:

- Operators must keep `teams.default_image` accurate for typical workloads because the team default is required and immutable through operator paths.
- Four image-bearing columns (`tasks.image`, `task_types.default_image`, `source_systems.default_image`, `tasks.resolved_image`, plus `teams.default_image`) add to schema complexity.
- Operators cannot selectively override only future claims through the admin path because `default_image` is immutable after registration; corrections require a new team.

#### Resolve entirely in the Executor from a separate image service

A dedicated image-configuration service would expose per-team defaults and per-task overrides; the Executor would query it at claim time or alongside discovery.

Rejected because it would split image configuration across two services, reintroduce a network call on the hot path of every claim, and put authoritative image selection outside the canonical audit and event log.

#### Hard-code the image in the listener payload only

Listeners submit `image` per task and the Executor uses that value at run time, with no team-wide default.

Rejected because it removes the operator-wide defaults that teams need, forces every listener to know the image, and prevents the team-default / task-type-default / source-system-default layering.

#### Keep three-level precedence with optional team default and Executor local fallback

Earlier drafts allowed optional `teams.default_image` and Executor local fallback when no override applied.

Rejected because a missing team default would force Executors to ship local fallback images, which violates the principle that image selection belongs to the Registry. With required `teams.default_image`, that fallback is unnecessary.

### Decision

State Registry SHALL be the authoritative resolver of task image at claim time. The image sources are opaque container image references consisting of a registry repository path and an immutable digest; the Registry SHALL NOT resolve, pull, verify, mutate, or interpret the digest. Image resolution SHALL use this precedence on atomic claim:

1. The canonical task's stored `image` override if non-NULL.
2. Otherwise, the referenced task type's `default_image` if non-NULL.
3. Otherwise, the referenced source system's `default_image` if non-NULL.
4. Otherwise, the parent team's stored `default_image` (always present because it is required at registration).

State Registry SHALL persist the resolved image as `tasks.resolved_image` and the source as `tasks.image_source` in the same transaction that appends the first lifecycle event `created`. The image_source SHALL be one of `task_override`, `task_type_default`, `source_system_default`, or `team_default`. The Registry SHALL include `resolved_image` and `image_source` in the claim response; the Executor SHALL use `resolved_image` verbatim and SHALL NOT substitute a local image or consult any other source. Image strings are not team-owned resources: the same image MAY be referenced across teams without granting or broadening authority, and image equality SHALL NOT establish tenant ownership.

Operators SHALL set `teams.default_image` through `POST /admin/teams`; the value SHALL be REQUIRED at registration and SHALL NOT be cleared or edited through any admin or operator path. Listeners MAY include `image` on `TaskIngestionRequest`; once set on the canonical task, `image` SHALL be immutable for the task's lifetime, so a later listener retry of the same `(team_id, source_system_id, source_id)` SHALL NOT replace or clear it. `tasks.resolved_image` SHALL be set atomically at claim and SHALL be immutable from claim onward; later edits to `teams.default_image` (only possible by registering a new team) SHALL affect future task claims only and SHALL NOT change any claimed task's `resolved_image`.

Image references SHALL NOT participate in tenant authorization. They SHALL NOT be used to select, prioritize, schedule, or reject work; they SHALL NOT gate discovery, claim, or lifecycle-event acceptance; and they SHALL NOT alter Executor capacity observations. State Registry SHALL accept any opaque digest string on `tasks.image`, `task_types.default_image`, `source_systems.default_image`, and `teams.default_image` (subject to documented format constraints in the OpenAPI schema) and SHALL NOT validate, parse, or otherwise interpret it.

A new C4/sequence diagram `state-registry-image-resolution-sequence.puml` SHALL document the four-level resolution flow and the handoff of `resolved_image` and `image_source` to the claiming Executor.

### Consequences

Positive consequences:

- Image configuration is part of the canonical platform surface, with audit entries for every accepted image write and for every image resolved at claim.
- The claim response carries the authoritative image, so the Executor and the Registry never disagree about what to run.
- `tasks.resolved_image` and `tasks.image_source` are durable across restarts and are the single source of truth for replay.
- Team-wide, task-type-wide, source-system-wide, and per-task scopes coexist without privilege escalation; image data is opaque, not team-owned, and never used to grant access.
- Removing Executor local fallback simplifies Executor configuration and removes a class of integration tests.
- Required `teams.default_image` makes resolution always succeed.

Negative consequences:

- Five image-bearing columns (`tasks.image`, `tasks.resolved_image`, `task_types.default_image`, `source_systems.default_image`, `teams.default_image`) add to schema and surface to the Registry.
- Operators cannot modify `teams.default_image` after registration; corrections require a new team, which loses history tied to the old `team_id`.
- Image references carry no integrity check beyond the registry digest itself; State Registry does not validate that the digest is well-formed, that the registry exists, or that the image is pullable. Operators retain responsibility for those checks at their container registry.
- Application code MUST NOT interpret image equality as team ownership; the four-level precedence and the no-cross-team-authority rule both require explicit, careful application logic.

## More Information

Supersedes: `v0002-state-registry`
