// Shared bootstrap helpers for the 81 contract tests under tests/contracts/.
// These helpers expose the small subset of admin, listener, and Executor
// HTTP calls required to seed canonical state across many contract
// scenarios. Every helper wraps one supported external HTTP boundary
// (POST /admin/teams, POST /admin/source-systems, POST /admin/task-types,
// POST /v1/tasks, PUT /v1/executors/{id}) and returns the JSON body the
// server produced. Tests still perform the behavior-specific assertions
// inline; the helpers do not swallow or short-circuit any contract
// expectation.
//
// Each helper accepts an already-built Playwright APIRequestContext
// from `identity.api(baseUrl)` so the request carries the documented
// identity headers. No helper copies headers manually.
//
// The bootstrap is intentionally minimal: tests that need additional
// setup (e.g. environment rows, secret versions) call admin endpoints
// directly with their own identity context.
import type { APIRequestContext, APIResponse } from '@playwright/test';
import type { IdentityContext } from '../../fixtures/identities';

// ImageReference is the wire shape the OpenAPI components.schemas.ImageReference
// object documents. State Registry JSON fields that reference an image
// (teams.default_image, source_systems.default_image,
// task_types.default_image, tasks.image, tasks.resolved_image, and the
// ClaimResponse.resolved_image envelope) carry one of these objects or
// null. The repository pattern matches
// `^[A-Za-z0-9][A-Za-z0-9._/-]*(:[A-Za-z0-9._-]+)?$`; the digest is
// exactly `sha256:` followed by 64 hex characters.
export interface ImageReference {
  repository: string;
  digest: string;
}

const RepositoryPattern = /^[A-Za-z0-9][A-Za-z0-9._/-]*(:[A-Za-z0-9._-]+)?$/;
const DigestPattern = /^sha256:[A-Fa-f0-9]{64}$/;
const HexCharPattern = /^[A-Fa-f0-9]$/;

// imageReference returns a deterministic ImageReference whose digest is
// `sha256:` followed by 64 copies of `digestHexChar`. The default
// 'a' produces a valid 64-hex digest. Throws when the repository or
// hex char would violate the OpenAPI shape so failures surface at
// construction time.
export function imageReference(repository: string, digestHexChar: string = 'a'): ImageReference {
  if (!RepositoryPattern.test(repository)) {
    throw new Error(
      `imageReference: repository ${JSON.stringify(repository)} does not match OpenAPI ` +
        'pattern ^[A-Za-z0-9][A-Za-z0-9._/-]*(:[A-Za-z0-9._-]+)?$',
    );
  }
  if (!HexCharPattern.test(digestHexChar)) {
    throw new Error(
      `imageReference: digestHexChar ${JSON.stringify(digestHexChar)} is not a single ` +
        'hex character [A-Fa-f0-9]',
    );
  }
  return {
    repository,
    digest: `sha256:${digestHexChar.repeat(64)}`,
  };
}

// dockerPullString renders an ImageReference as `<repository>@<digest>`
// for Docker daemon / container runtime comparisons. Tests use it at
// the Docker metadata boundary only — never for State Registry wire
// fields, which always carry the structured object.
export function dockerPullString(image: ImageReference): string {
  if (!DigestPattern.test(image.digest)) {
    throw new Error(
      `dockerPullString: digest ${JSON.stringify(image.digest)} is not sha256:<64hex>`,
    );
  }
  return `${image.repository}@${image.digest}`;
}

export interface AdminTeam {
  team_id: string;
  team_name: string;
  default_image: ImageReference;
  created_at: string;
  updated_at: string;
}

export interface AdminSourceSystem {
  source_system_id: string;
  team_id: string;
  listener_identity: string;
  default_image: ImageReference | null;
  created_at: string;
  updated_at: string;
}

export interface AdminTaskType {
  task_type_id: string;
  team_id: string;
  execution_tag: string;
  default_image: ImageReference | null;
  created_at: string;
  updated_at: string;
}

export interface AdminTeamCreateBody {
  team_name: string;
  default_image: ImageReference;
}

export interface AdminSourceSystemCreateBody {
  team_id: string;
  listener_identity: string;
  default_image?: ImageReference | null;
}

export interface AdminTaskTypeCreateBody {
  team_id: string;
  execution_tag: string;
  default_image?: ImageReference | null;
}

export interface TaskIngestionBody {
  team_id: string;
  source_system_id: string;
  source_id: string;
  task_type_id: string;
  payload: Record<string, unknown>;
  image?: ImageReference | null;
  project_id?: string | null;
  environment_id?: string | null;
}

export interface TaskResource {
  task_id: string;
  team_id: string;
  source_system_id: string;
  source_id: string;
  task_type_id: string;
  required_tag: string;
  payload: Record<string, unknown>;
  current_state: string;
  owner_command_id: string | null;
  executor_id: string | null;
  project_id: string | null;
  environment_id: string | null;
  image: ImageReference | null;
  resolved_image: ImageReference | null;
  image_source: string | null;
  ingested_at: string;
  claimed_at: string | null;
}

export interface ExecutorRegistrationBody {
  scope: 'team' | 'system';
  team_id: string | null;
  executor_type: string;
  identity: string;
  authorized_tag: string;
  max_capacity: number;
  running_count: number;
  runtime_metadata: Record<string, unknown>;
}

export interface ExecutorResource {
  executor_id: string;
  scope: 'team' | 'system';
  team_id: string | null;
  executor_type: string;
  identity: string;
  authorized_tag: string;
  max_capacity: number;
  running_count: number;
  runtime_metadata: Record<string, unknown>;
  registered_at: string;
  updated_at: string;
}

export interface ResponseSnapshot {
  status(): number;
  ok(): boolean;
  headers(): Readonly<Record<string, string>>;
  text(): Promise<string>;
  json(): Promise<unknown>;
}

// APIRequestContext.dispose() releases every response body retained by
// Playwright. Helpers that own a short-lived context must therefore copy the
// complete response before disposal instead of returning a live APIResponse.
export async function snapshotResponse(response: APIResponse): Promise<ResponseSnapshot> {
  const status = response.status();
  const ok = response.ok();
  const headers = Object.freeze({ ...response.headers() });
  const body = await response.text();

  return Object.freeze({
    status: () => status,
    ok: () => ok,
    headers: () => headers,
    text: async () => body,
    json: async () => JSON.parse(body) as unknown,
  });
}

export async function createAdminTeam(
  admin: APIRequestContext,
  body: AdminTeamCreateBody,
): Promise<AdminTeam> {
  const resp = await admin.post('/admin/teams', { data: body });
  if (resp.status() !== 201) {
    throw new Error(
      `createAdminTeam expected 201, received ${String(resp.status())}; body=${await resp.text()}`,
    );
  }
  return (await resp.json()) as AdminTeam;
}

export async function createAdminSourceSystem(
  admin: APIRequestContext,
  body: AdminSourceSystemCreateBody,
): Promise<AdminSourceSystem> {
  const resp = await admin.post('/admin/source-systems', { data: body });
  if (resp.status() !== 201) {
    throw new Error(
      `createAdminSourceSystem expected 201, received ${String(resp.status())}; body=${await resp.text()}`,
    );
  }
  return (await resp.json()) as AdminSourceSystem;
}

export async function createAdminTaskType(
  admin: APIRequestContext,
  body: AdminTaskTypeCreateBody,
): Promise<AdminTaskType> {
  const resp = await admin.post('/admin/task-types', { data: body });
  if (resp.status() !== 201) {
    throw new Error(
      `createAdminTaskType expected 201, received ${String(resp.status())}; body=${await resp.text()}`,
    );
  }
  return (await resp.json()) as AdminTaskType;
}

// The listener_identity value MUST be byte-equal to the value the
// listener sends in its `X-FlowAI-Listener-Identity` header so the
// Registry can authenticate the ingestion call. Likewise the
// source_system_id MUST be byte-equal to the value the listener sends
// in its `X-FlowAI-Source-System-Id` header. The helper accepts a
// `listenerIdentity` argument and produces the bootstrap as the
// Registry will accept it; the caller MUST then build a listener
// context with `listenerFor({ teamId, listenerIdentity, sourceSystemId })`
// so the wire headers match.
export async function bootstrapTeam(
  admin: IdentityContext,
  baseUrl: string,
  opts: {
    teamName?: string;
    defaultImage?: ImageReference;
    executionTag?: string;
  } = {},
): Promise<{
  admin: AdminTeam;
  sourceSystem: AdminSourceSystem;
  taskType: AdminTaskType;
  listenerIdentity: string;
}> {
  const suffix = `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
  const listenerIdentity = `L-${suffix}`;
  const adminApi = await admin.api(baseUrl);
  try {
    const teamName = opts.teamName ?? `team-${suffix}`;
    const defaultImage = opts.defaultImage ?? imageReference(`default-${suffix}`);
    const executionTag = opts.executionTag ?? 'openhands';
    const team = await createAdminTeam(adminApi, {
      team_name: teamName,
      default_image: defaultImage,
    });
    const sourceSystem = await createAdminSourceSystem(adminApi, {
      team_id: team.team_id,
      listener_identity: listenerIdentity,
      default_image: null,
    });
    const taskType = await createAdminTaskType(adminApi, {
      team_id: team.team_id,
      execution_tag: executionTag,
      default_image: null,
    });
    return { admin: team, sourceSystem, taskType, listenerIdentity };
  } finally {
    await adminApi.dispose();
  }
}

export async function ingestPendingTask(
  listener: IdentityContext,
  baseUrl: string,
  body: TaskIngestionBody,
): Promise<TaskResource> {
  const api = await listener.api(baseUrl);
  try {
    const resp = await api.post('/v1/tasks', { data: body });
    if (resp.status() !== 201) {
      throw new Error(
        `ingestPendingTask expected 201, received ${String(resp.status())}; body=${await resp.text()}`,
      );
    }
    return (await resp.json()) as TaskResource;
  } finally {
    await api.dispose();
  }
}

export async function retryPendingTask(
  listener: IdentityContext,
  baseUrl: string,
  body: TaskIngestionBody,
): Promise<TaskResource> {
  const api = await listener.api(baseUrl);
  try {
    const resp = await api.post('/v1/tasks', { data: body });
    if (resp.status() !== 200) {
      throw new Error(
        `retryPendingTask expected 200, received ${String(resp.status())}; body=${await resp.text()}`,
      );
    }
    return (await resp.json()) as TaskResource;
  } finally {
    await api.dispose();
  }
}

export async function registerExecutor(
  executor: IdentityContext,
  baseUrl: string,
  body: ExecutorRegistrationBody,
): Promise<ExecutorResource> {
  const executorId = executor.attach()['X-FlowAI-Executor-Id'];
  if (!executorId) {
    throw new Error('registerExecutor requires an executor identity with X-FlowAI-Executor-Id');
  }
  const api = await executor.api(baseUrl);
  try {
    const resp = await api.put(`/v1/executors/${encodeURIComponent(executorId)}`, { data: body });
    if (resp.status() !== 200) {
      throw new Error(
        `registerExecutor expected 200, received ${String(resp.status())}; body=${await resp.text()}`,
      );
    }
    return (await resp.json()) as ExecutorResource;
  } finally {
    await api.dispose();
  }
}
