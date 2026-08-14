## ADDED Requirements

### Requirement: Platform provides a standalone GitLab Webhook Listener service

The platform SHALL provide a self-contained task-source plugin service under `svc/gitlab-webhook-listener/` with a service-owned manifest declaring plugin type `task_source_gitlab_webhook`. Every instance SHALL implement the versioned task-source plugin contract and register its immutable plugin/team/source-system identity and safe capabilities/filter summary with State Registry before accepting task-producing deliveries. It SHALL receive authenticated webhook deliveries from configured GitLab sources, map eligible events to pre-registered FlowAI task types, and persist them through State Registry before acknowledging successful delivery. GitLab versions, supported event families, webhook authentication, payload mappings, limits, and retry status codes SHALL be selected in a later design phase. The service SHALL NOT share code with other services, ingest before successful plugin registration, poll or mutate GitLab, execute tasks, or create teams, source systems, or task types.

#### Scenario: GitLab webhook becomes a durable FlowAI task

- **WHEN** a configured GitLab source sends an authentic eligible webhook event that maps to a pre-registered FlowAI task type
- **THEN** GitLab Webhook Listener persists one team-owned canonical task through State Registry before returning successful acknowledgement

#### Scenario: GitLab webhook plugin instance starts

- **WHEN** a configured GitLab Webhook Listener instance starts with a supported plugin manifest and valid team/source-system binding
- **THEN** it registers as `task_source_gitlab_webhook` in State Registry before accepting task-producing webhook deliveries
