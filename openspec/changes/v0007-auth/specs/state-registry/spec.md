## MODIFIED Requirements

### Requirement: State Registry owns team registration through `/admin/teams`

The State Registry SHALL expose `POST /admin/teams` to create a team, accepting only an authenticated system-administrator identity. The request body SHALL include a REQUIRED unique `team_name`, a REQUIRED immutable unique `keycloak_group_id`, and a REQUIRED `default_image` opaque container image reference. The State Registry SHALL persist a generated immutable `team_id`, the display-only `team_name`, the immutable `keycloak_group_id`, the required `default_image`, and an `ingested_at` timestamp. After successful creation, it SHALL return the canonical team record including `team_id`, `team_name`, `keycloak_group_id`, and `default_image`. It SHALL NOT authorize by `team_name` or a Keycloak group display name and SHALL NOT provide team update or delete endpoints.

#### Scenario: Administrator creates a Keycloak-mapped team

- **WHEN** an authenticated system administrator submits a unique `team_name`, a unique stable `keycloak_group_id`, and a valid required `default_image`
- **THEN** State Registry creates and returns one immutable canonical team mapping

#### Scenario: Keycloak group is already mapped

- **WHEN** an authenticated system administrator submits a `keycloak_group_id` already assigned to another team
- **THEN** State Registry rejects the request without creating or changing a team

## ADDED Requirements

### Requirement: State Registry resolves Keycloak groups to canonical teams

The State Registry SHALL expose `POST /v1/auth/team-resolutions` as a read-only team-resolution API callable only by the authenticated trusted API Gateway service identity. The JSON request SHALL contain exactly `operator_id`, `request_id`, and `keycloak_group_ids`; `keycloak_group_ids` SHALL be a non-empty array of at most 200 canonical stable group identifiers obtained from Keycloak. State Registry SHALL reject malformed, empty, or oversized input before reading a team row and SHALL deduplicate valid identifiers before lookup. It SHALL match only active teams whose immutable `keycloak_group_id` appears in the request and SHALL return a deterministic `teams` list ordered by `(team_name ASC, team_id ASC)`, with each item containing exactly `team_id` and display-only `team_name`. Unknown groups SHALL produce no item and SHALL NOT reveal whether any other group or team exists. The endpoint SHALL perform no mutation and SHALL NOT accept a browser credential, browser-supplied identity context, group display name, `team_name`, or caller-selected `team_id` as authority.

#### Scenario: Canonical groups map to several teams

- **WHEN** trusted API Gateway submits canonical Keycloak group identifiers mapped to multiple active teams
- **THEN** State Registry returns exactly those teams as deterministic `{team_id, team_name}` entries

#### Scenario: Untrusted caller submits groups directly

- **WHEN** a browser, anonymous caller, or non-Gateway service calls the team-resolution API
- **THEN** State Registry rejects the request before reading a team row

#### Scenario: Request contains unknown and duplicate groups

- **WHEN** trusted API Gateway submits a bounded request containing duplicate and unknown canonical group identifiers
- **THEN** State Registry deduplicates known mappings, omits unknown mappings, and returns no duplicate team item
