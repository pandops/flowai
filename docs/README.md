# Documentation

[Project README](../README.md) · [Contributing](../CONTRIBUTING.md) · [MIT License](../LICENSE)

## Current documentation

| Topic                           | Entry point                                                                                                 |
| ------------------------------- | ----------------------------------------------------------------------------------------------------------- |
| Runtime services and boundaries | [Architecture overview](architecture/overview.md)                                                           |
| Normative contracts             | [Current OpenSpec specifications](../openspec/specs/)                                                       |
| Architecture decisions          | [ADR index](adr/README.md)                                                                                  |
| Architecture diagrams           | [Conventions](architecture/README.md) · [PlantUML sources](architecture/diagrams/)                          |
| OpenHands integration baseline  | [Agent SDK baseline](baselines/openhands-agent-sdk-baseline.md)                                             |
| State Registry                  | [Service guide](../svc/state-registry/README.md)                                                            |
| Docker OpenHands Executor       | [Service guide](../executor/docker_openhands/README.md)                                                     |
| Kubernetes OpenHands Executor   | [Service guide](../executor/k8s-openhands/README.md)                                                        |
| API Gateway and authentication  | [Gateway contract](../openspec/specs/api-gateway/spec.md) · [Auth contract](../openspec/specs/auth/spec.md) |
| Web UI                          | [Operator interface contract](../openspec/specs/web-ui/spec.md)                                             |
| End-to-end verification         | [E2E guide](../qa-e2e/README.md) · [Test-case definitions](../qa-e2e/test-cases/)                           |

## Planned changes

[Active change directories](../openspec/changes/) contain proposals and unfinished
work. Listing here does not mean a change is approved, implemented, or ready to
implement. Open the proposal for intent and status, and its directory for design,
spec deltas, tasks, diagrams, and test definitions where prepared.

| Change                                                                                                         | Planned scope                                       | Artifacts                                                               |
| -------------------------------------------------------------------------------------------------------------- | --------------------------------------------------- | ----------------------------------------------------------------------- |
| [v0008-use-openbao-transit](../openspec/changes/v0008-use-openbao-transit/proposal.md)                         | OpenBao Transit secret encryption and migration     | [Directory](../openspec/changes/v0008-use-openbao-transit/)             |
| [v0010-add-paused-termination-status](../openspec/changes/v0010-add-paused-termination-status/proposal.md)     | Paused task termination status                      | [Directory](../openspec/changes/v0010-add-paused-termination-status/)   |
| [v0011-add-hibernate-status](../openspec/changes/v0011-add-hibernate-status/proposal.md)                       | Task hibernation                                    | [Directory](../openspec/changes/v0011-add-hibernate-status/)            |
| [v0012-add-opentelemetry-observability](../openspec/changes/v0012-add-opentelemetry-observability/proposal.md) | OpenTelemetry observability                         | [Directory](../openspec/changes/v0012-add-opentelemetry-observability/) |
| [v0013-add-task-sources-interface](../openspec/changes/v0013-add-task-sources-interface/proposal.md)           | Task-source plugin interface                        | [Directory](../openspec/changes/v0013-add-task-sources-interface/)      |
| [v0014-add-jira-listener](../openspec/changes/v0014-add-jira-listener/proposal.md)                             | Jira task source                                    | [Directory](../openspec/changes/v0014-add-jira-listener/)               |
| [v0015-add-gitlab-webhook-listener](../openspec/changes/v0015-add-gitlab-webhook-listener/proposal.md)         | GitLab webhook task source                          | [Directory](../openspec/changes/v0015-add-gitlab-webhook-listener/)     |
| [v0016-add-cron-task-scheduler](../openspec/changes/v0016-add-cron-task-scheduler/proposal.md)                 | Recurring task scheduling                           | [Directory](../openspec/changes/v0016-add-cron-task-scheduler/)         |
| [v0018-convert-dashboard-to-kanban](../openspec/changes/v0018-convert-dashboard-to-kanban/proposal.md)         | Kanban task dashboard                               | [Directory](../openspec/changes/v0018-convert-dashboard-to-kanban/)     |
| [v0019-add-dynamic-task-continuation](../openspec/changes/v0019-add-dynamic-task-continuation/proposal.md)     | Dynamic task continuation                           | [Directory](../openspec/changes/v0019-add-dynamic-task-continuation/)   |
| [v0020-add-openhands-goal-execution](../openspec/changes/v0020-add-openhands-goal-execution/proposal.md)       | OpenHands goal execution                            | [Directory](../openspec/changes/v0020-add-openhands-goal-execution/)    |
| [v0021-add-terminal-session-export](../openspec/changes/v0021-add-terminal-session-export/proposal.md)         | Terminal session export and handoff                 | [Directory](../openspec/changes/v0021-add-terminal-session-export/)     |
| [v0022-integrate-litellm-gateway](../openspec/changes/v0022-integrate-litellm-gateway/proposal.md)             | LiteLLM gateway integration                         | [Directory](../openspec/changes/v0022-integrate-litellm-gateway/)       |
| [v0023-evaluate-ax-executor](../openspec/changes/v0023-evaluate-ax-executor/proposal.md)                       | Google AX evaluation (deferred; adoption undecided) | [Directory](../openspec/changes/v0023-evaluate-ax-executor/)            |

## Archived changes

[The archive](../openspec/changes/archive/) preserves historical changes. Some
were implemented and later extended; others were superseded without implementation.
Current contracts and code take precedence over historical proposals. The two
`v0009` entries are distinct historical changes and retain their original IDs.

| Archived change                                                                                                                          | Scope                                              | Disposition                                                                                                                                              |
| ---------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [2026-07-12-v0001-executor-docker](../openspec/changes/archive/2026-07-12-v0001-executor-docker/proposal.md)                             | Docker OpenHands Executor bootstrap                | Implemented bootstrap; read current Executor contract for subsequent changes; [artifacts](../openspec/changes/archive/2026-07-12-v0001-executor-docker/) |
| [2026-07-14-v0003-env-registry](../openspec/changes/archive/2026-07-14-v0003-env-registry/proposal.md)                                   | Standalone Env Registry                            | Superseded without implementation; responsibilities moved to State Registry; [artifacts](../openspec/changes/archive/2026-07-14-v0003-env-registry/)     |
| [2026-08-02-v0002-state-registry](../openspec/changes/archive/2026-08-02-v0002-state-registry/proposal.md)                               | Durable State Registry                             | Implemented; subsequent auth/team changes are recorded in v0007; [artifacts](../openspec/changes/archive/2026-08-02-v0002-state-registry/)               |
| [2026-08-02-v0009-enforce-commit-quality](../openspec/changes/archive/2026-08-02-v0009-enforce-commit-quality/proposal.md)               | Commit formatting and lint checks                  | Implemented developer tooling; [artifacts](../openspec/changes/archive/2026-08-02-v0009-enforce-commit-quality/)                                         |
| [2026-08-08-v0005-executor-k8s](../openspec/changes/archive/2026-08-08-v0005-executor-k8s/proposal.md)                                   | Kubernetes OpenHands Executor                      | Implemented; [artifacts](../openspec/changes/archive/2026-08-08-v0005-executor-k8s/)                                                                     |
| [2026-08-13-v0009-remove-backend-mtls](../openspec/changes/archive/2026-08-13-v0009-remove-backend-mtls/proposal.md)                     | Remove backend mTLS                                | Implemented transport change; [artifacts](../openspec/changes/archive/2026-08-13-v0009-remove-backend-mtls/)                                             |
| [2026-08-15-v0006-web-ui](../openspec/changes/archive/2026-08-15-v0006-web-ui/proposal.md)                                               | Operator Web UI                                    | Implemented bootstrap; authentication added by v0007; [artifacts](../openspec/changes/archive/2026-08-15-v0006-web-ui/)                                  |
| [2026-08-16-v0017-containerize-runtime-services](../openspec/changes/archive/2026-08-16-v0017-containerize-runtime-services/proposal.md) | Container-native runtime services                  | Implemented; [artifacts](../openspec/changes/archive/2026-08-16-v0017-containerize-runtime-services/)                                                    |
| [2026-08-30-v0007-auth](../openspec/changes/archive/2026-08-30-v0007-auth/proposal.md)                                                   | Provider-neutral authentication and team lifecycle | Implemented and synced; [artifacts](../openspec/changes/archive/2026-08-30-v0007-auth/)                                                                  |

## Keeping the index current

Add a planned-change link when a proposal is recorded. When it is archived,
move its entry to the archive table and describe whether it was delivered or
superseded. Update current documentation only for accepted, implemented behavior;
keep proposed specs and diagrams with their change until sync.
