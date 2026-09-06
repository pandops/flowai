## ADDED Requirements

### Requirement: Web UI manages only safe FlowAI target references

Web UI SHALL let a same-team operator list, register, revise, enable, disable, and select safe LiteLLM target references through API Gateway and State Registry. It SHALL distinguish `model` and `a2a_agent`, show the immutable public alias and safe capabilities, and prevent incompatible agent-runtime/target selections. It SHALL NOT request, accept, display, or retain provider credentials, LiteLLM administrator credentials, master keys, or virtual keys.

#### Scenario: Operator registers a safe target reference

- **WHEN** a team operator submits a known LiteLLM alias, target kind, and display metadata
- **THEN** Web UI displays the canonical same-team target returned by State Registry without any credential value

#### Scenario: Operator selects an incompatible target

- **WHEN** a task type's agent runtime does not support the selected target kind or protocol capability
- **THEN** Web UI prevents submission and identifies the incompatible safe capability fields

### Requirement: Web UI keeps LiteLLM administration separate

Web UI SHALL NOT embed or proxy the LiteLLM Admin UI. If deployment configuration exposes an administrator link, it SHALL open the separately configured HTTPS origin and SHALL make clear that a separate LiteLLM administrator login is required.

#### Scenario: Operator follows the administrator link

- **WHEN** an authorized user activates the configured LiteLLM administration link
- **THEN** the browser opens the separate administrator origin and the FlowAI session is not forwarded as LiteLLM authentication
