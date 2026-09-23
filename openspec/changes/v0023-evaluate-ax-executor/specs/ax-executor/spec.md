## ADDED Requirements

### Requirement: Experimental AX integration preserves FlowAI task authority

An experimental AX-backed Executor SHALL obtain a successful State Registry
claim before submitting execution to AX. It SHALL preserve immutable task,
team, and owner command identifiers and use the Registry-resolved image without
substitution. State Registry SHALL remain authoritative for FIFO assignment,
canonical lifecycle, controls, and scoped environment access. The adapter SHALL
NOT equate sandbox readiness with successful agent completion. Its service
directory, runtime/tool wire identifier, AX protocol, recovery mapping, and
result bridge SHALL be selected during detailed design before implementation.
This proposed requirement does not authorize migration of existing Executors.

#### Scenario: Claim is rejected

- **WHEN** State Registry rejects an experimental Executor's task claim
- **THEN** the Executor submits no execution for that claim to AX

#### Scenario: Claimed task reaches a ready sandbox

- **WHEN** AX reports sandbox readiness for successfully claimed work
- **THEN** the adapter preserves the claim's task/team/command identity and resolved image and does not report `finished` solely because the sandbox is ready

### Requirement: AX adoption requires demonstrated lifecycle compatibility

The AX evaluation SHALL pin upstream revisions and verify the runner image
contract, OpenHands result reporting, submission reconciliation, team isolation,
secret handling, cleanup, and recovery through container-based cross-service
E2E tests before recommending adoption. Suspension and workspace restoration
SHALL NOT be treated as proof of OpenHands conversation continuation. Detailed
E2E definitions and lifecycle mappings SHALL be prepared before implementation.
The evaluation MAY conclude that AX is unsuitable and retain existing Executors.

#### Scenario: Restored workspace has no demonstrated conversation continuation

- **WHEN** a suspended task's files are restored but continuation of its OpenHands conversation has not been verified
- **THEN** the evaluation records conversation recovery as unproven and does not claim transparent task resume

#### Scenario: Evaluation finds an unresolved contract mismatch

- **WHEN** the experiment cannot preserve a required FlowAI ownership or lifecycle guarantee
- **THEN** the evaluation records the mismatch and does not recommend replacing existing Executors with that implementation
