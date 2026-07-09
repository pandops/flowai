## MODIFIED Requirements

### Requirement: Docker Executor participates in Router scheduling

After Router exists, the Docker Executor SHALL register with the Router, receive an Executor identifier, report availability, pull matching tasks from the Router wait list, post task status, and honor configured capacity.

#### Scenario: Docker Executor starts after Router exists

- **WHEN** a Docker Executor process starts after Router is implemented
- **THEN** it registers executor type `docker`, routing target, capacity, running child count, and metadata with the Router

#### Scenario: Docker Executor pulls work after Router exists

- **WHEN** the Docker Executor has available capacity after Router is implemented
- **THEN** it requests work from the Router wait list instead of the mocked task server
