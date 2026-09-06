## ADDED Requirements

### Requirement: Assigned agent workloads use the pinned LiteLLM target

After claim, a concrete Executor SHALL obtain the task's authorized scoped environment from State Registry and configure only the assigned agent workload with the returned LiteLLM data-plane URL, exact public alias, target kind, and runtime key. The Executor SHALL NOT persist or log the key, return it through probes or events, or give it to another workload. The workload SHALL route the selected model or A2A operation through LiteLLM and SHALL NOT fall back to provider-direct credentials or another alias.

#### Scenario: Assigned workload receives runtime configuration

- **WHEN** the assigned Executor opens the environment for a claimed task with a LiteLLM target
- **THEN** it starts only that task's agent workload with the pinned URL, alias, kind, and key while redacting the key from all Executor output

#### Scenario: LiteLLM call fails

- **WHEN** the assigned workload receives an authentication, unavailable-target, provider, or timeout error from LiteLLM
- **THEN** the Executor reports a sanitized failed task event and does not retry through a provider-direct endpoint or another alias
