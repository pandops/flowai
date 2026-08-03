# ADR: Use admin-only team, source-system, and task-type registration

### Status

Accepted

### Context

Every durable record in State Registry carries an immutable `team_id`. Until now, teams were created implicitly through the first listener or Executor that referenced a `team_id` value. That model conflates identity creation with runtime use, allows callers to choose a `team_id`, and leaves source systems and task types without a registration path entirely. Image resolution at claim time depends on `default_image` being set on each referenced team AND on each task type and source system the claim references; without required `default_image` at registration, the Registry would need to fall back to Executor local configuration, which violates the principle that image selection belongs to the Registry.

The platform needs a single, exclusive path to mint immutable team-owned platform identities — teams, source systems, and task types — that no listener, Executor, or Gateway may invoke.

### Decision

State Registry SHALL expose three admin endpoints under `/admin/*`, each accepting only an authenticated system-administrator identity:

- `POST /admin/teams`: required unique `team_name` (display-only, never authorization), required opaque `default_image`, returns generated immutable `team_id`. The `team_name` field is unique across teams; the `default_image` is required and cannot be cleared later. Operators SHALL NOT clear or change `default_image` through any admin or operator path; the only way to change the team's `default_image` is to register a new team.

- `POST /admin/source-systems`: required `team_id` (which SHALL reference an existing team), required listener identity reference, immutable server-assigned `source_system_id`, optional `default_image`. The source system SHALL belong to exactly one team; cross-team reattachment SHALL be rejected without persistence. The opaque listener identity reference SHALL be constrained by a database-enforced global uniqueness index so one listener principal maps to exactly one `(team_id, source_system_id)` pair; an attempt to register a new source system with the same `listener_identity` value under the same team, a different team, or any other variation SHALL be rejected without mutation, so one principal cannot be reused across teams or re-registered under the same team.

- `POST /admin/task-types`: required `team_id` (which SHALL reference an existing team), immutable server-assigned `task_type_id`, required `execution_tag` (the immutable registered tag Executors must declare to be eligible for tasks of this type), optional `default_image`.

The Registry SHALL reject all `/admin/*` calls without an authenticated system-administrator identity. The Registry SHALL NOT describe the mechanism for that authentication beyond requiring the identity. The Registry SHALL reject listener or Executor registrations that supply a `team_id` not referencing an existing team. The Registry SHALL NOT allow Executor or listener bodies to create or update teams, source systems, or task types; an Executor registration is never a substitute for `POST /admin/teams`. The image returned to a claimed task uses the documented four-level precedence (`tasks.image` -> `task_types.default_image` -> `source_systems.default_image` -> `teams.default_image`) and SHALL always resolve because the team default is required at registration.

### Consequences

Positive consequences:

- One exclusive path creates every team-owned platform identity; no runtime service can mint an unknown team.
- Required `default_image` at team registration guarantees image resolution at claim, removing Executor local-image fallback semantics.
- Listener dedupe uses immutable `source_system_id` rather than free-form `source`; the cross-team reuse wording is corrected so a source-system identity belongs to one team.
- System-administrator authentication is required but its mechanism is outside this contract, decoupling admin onboarding from the Registry's contract surface.

Negative consequences:

- Operational onboarding must include provisioning a system-administrator identity and creating teams, source systems, and task types before any Executor or listener can use the platform. This added step blocks typical first-run flows until admin setup completes.
- Team `default_image` is immutable through operator and admin paths; corrections require a new team, which loses history tied to the old `team_id`.
- The contract deliberately refuses to specify how the system-administrator identity is authenticated, leaving operational decisions to deployment.
- Image resolution relies on the team default; removing or clearing that value post-creation is impossible.

## More Information

Supersedes: `v0002-state-registry`
