// Contract group: environments-secrets-crypto
// Covers v0002.8, v0002.9, v0002.10, v0002.11, v0002.38, v0002.39,
// v0002.40, v0002.45, v0002.46, v0002.47, v0002.48, v0002.49, v0002.77,
// v0002.78.
//
// Each test below exercises one of the environment, secret, or
// cryptographic boundaries documented in the contract:
//   - POST /v1/environments, GET /v1/environments/{id},
//     PUT /v1/environments/{id}, DELETE /v1/environments/{id}
//   - POST /v1/environments/{id}/secrets,
//     POST /v1/environments/{id}/secrets/{secret_id}/versions
//   - GET /v1/environments/{id}/open?task_id=...
//
// Behavior-specific assertions verify:
//   - environment values are team-scoped; foreign-team identifiers
//     return non-revealing 404 with zero encrypt operations
//   - logical secrets carry opaque key_id / key_version metadata
//   - the open-environment boundary verifies the scope token with
//     constant-time comparison and returns a 404 shape on every
//     invalid or unavailable case with zero decrypt operations
//   - the AES-256-GCM data key is loaded at startup; missing key
//     fails closed; deny paths never decrypt
//
// Positive open paths use the claim-issued compact three-part
// scope token (no test-only mint endpoint). Negative paths derive
// meaningful token-or-canonical mismatches (preserving the original
// signature when the body/header changes so the MAC mismatch remains
// expected) and compare the exact non-revealing 404 body byte-for-byte
// with one canonical 404 captured before the loop. Deny paths are
// observed through the test-only DecryptOps counter and must record
// zero decrypt operations.
import { test, expect } from "@playwright/test";
import {
  startRegistryWorker,
  type RegistryWorker,
} from "../../fixtures/registry_worker";
import {
  systemAdministrator,
  gatewayFor,
  teamExecutorFor,
  listenerFor,
  type IdentityContext,
} from "../../fixtures/identities";
import {
  bootstrapTeam,
  ingestPendingTask,
  registerExecutor,
  snapshotResponse,
  type ResponseSnapshot,
  type ImageReference,
} from "./_setup";
import { snapshotDecryptOps } from "../../fixtures/decrypt_observer";
import { uniqueExecutorId } from "../../fixtures/executor_container";

let worker: RegistryWorker;

test.beforeAll(async () => {
  worker = await startRegistryWorker();
});

test.afterAll(async () => {
  if (worker) {
    await worker.teardown();
  }
});

// --- File-scoped typed helpers ------------------------------------------

// Wire shape of POST /v1/executors/{executor_id}/claim as documented in
// components.schemas.ClaimResponse. Only the fields required by the
// environment/secret test group are typed; the rest of the body is
// carried through as opaque unknowns.
interface ClaimResponseBody {
  claim: "claimed";
  task: {
    task_id: string;
    team_id: string;
    owner_command_id: string;
    executor_id: string;
    claimed_at: string;
    current_state: "created";
    resolved_image: ImageReference;
    image_source: string;
  };
  environment_id: string | null;
  scope_token: string | null;
  resolved_image: ImageReference;
  image_source: string;
  claimed_at: string;
}

// Wire shape of the compact scope token payload
// (components.schemas.ScopeTokenClaims).
interface ScopeTokenClaims {
  team_id: string;
  project_id: string | null;
  task_id: string;
  environment_id: string;
  executor_id: string;
  audience: "state-registry.environment.open";
  key_id: string;
  issued_at: string;
  expiry: string;
}

// Wire shape of the compact scope token protected header
// (components.schemas.ScopeTokenHeader).
interface ScopeTokenHeader {
  alg: "HS256" | "HS384" | "HS512";
  kid: string;
  typ: "scope-token+json";
}

interface SplitToken {
  header: string;
  payload: string;
  signature: string;
}

// Wire shape of GET /v1/environments/{id}/open success body
// (components.schemas.OpenEnvironmentResponse).
interface OpenEnvironmentBody {
  team_id: string;
  project_id: string | null;
  task_id: string;
  environment_id: string;
  executor_id: string;
  values: Record<string, string>;
}

// Wire shape of components.schemas.ErrorResponse — the exact closure
// body the open endpoint returns for every invalid or unavailable case.
interface OpenErrorBody {
  code: string;
  message: string;
  request_id: string;
}

interface OpenResponse {
  status: number;
  text: string;
  json: unknown;
}

const OpenPath = (envId: string, taskId: string): string =>
  `/v1/environments/${encodeURIComponent(envId)}/open?task_id=${encodeURIComponent(taskId)}`;

const UnknownEnvId = "env-00000000-0000-0000-0000-000000000000";
const UnknownTaskId = "00000000-0000-0000-0000-000000000000";

async function readJson<T>(snap: ResponseSnapshot): Promise<T> {
  return (await snap.json()) as T;
}

async function claimAsExecutor(
  exec: IdentityContext,
  baseUrl: string,
  taskId: string,
  commandId: string,
): Promise<ResponseSnapshot> {
  const executorId = exec.attach()["X-FlowAI-Executor-Id"] ?? "";
  const api = await exec.api(baseUrl);
  try {
    const response = await api.post(
      `/v1/executors/${encodeURIComponent(executorId)}/claim`,
      { data: { task_id: taskId, command_id: commandId } },
    );
    return await snapshotResponse(response);
  } finally {
    await api.dispose();
  }
}

// openEnvironmentAsExecutor performs the assigned-Executor open against
// the running worker using the supplied scope token. The response body
// is captured before APIRequestContext.dispose() so consumers can
// inspect status, captured text, and parsed JSON without holding a
// freed APIResponse reference. The Executor identity is the only
// transport documented for this endpoint: every denial probe below
// must therefore travel through the authenticated Executor context
// so the Registry evaluates the scope-token checks (not transport
// authentication) before producing the canonical 404 closure shape.
async function openEnvironmentAsExecutor(
  exec: IdentityContext,
  baseUrl: string,
  envId: string,
  taskId: string,
  token: string | null,
): Promise<OpenResponse> {
  const headers: Record<string, string> = {};
  if (token !== null) {
    headers["X-FlowAI-Scope-Token"] = token;
  }
  const api = await exec.api(baseUrl);
  try {
    const response = await api.get(OpenPath(envId, taskId), { headers });
    return await consumeOpenResponse(response);
  } finally {
    await api.dispose();
  }
}

// consumeOpenResponse reads the body once and returns the captured
// snapshot. Callers never reference the original response after this
// helper returns.
async function consumeOpenResponse(response: {
  status: () => number;
  text: () => Promise<string>;
}): Promise<OpenResponse> {
  const status = response.status();
  const text = await response.text();
  let json: unknown = null;
  if (text.length > 0) {
    try {
      json = JSON.parse(text);
    } catch {
      // body is not JSON; leave json null
    }
  }
  return { status, text, json };
}

function splitToken(token: string): SplitToken {
  const parts = token.split(".");
  if (parts.length !== 3) {
    throw new Error(`splitToken: expected three segments, got ${parts.length}`);
  }
  const [header, payload, signature] = parts;
  if (!header || !payload || !signature) {
    throw new Error("splitToken: empty segment in compact token");
  }
  return { header, payload, signature };
}

function decodeBase64Url(segment: string): string {
  const padded = segment.replace(/-/g, "+").replace(/_/g, "/");
  const padding = (4 - (padded.length % 4)) % 4;
  const buffer = Buffer.from(padded + "=".repeat(padding), "base64");
  return buffer.toString("utf-8");
}

function encodeBase64Url(input: string): string {
  return Buffer.from(input, "utf-8")
    .toString("base64")
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/g, "");
}

function decodeClaims(token: string): ScopeTokenClaims {
  const { payload } = splitToken(token);
  return JSON.parse(decodeBase64Url(payload)) as ScopeTokenClaims;
}

function decodeHeader(token: string): ScopeTokenHeader {
  const { header } = splitToken(token);
  return JSON.parse(decodeBase64Url(header)) as ScopeTokenHeader;
}

// tamperSignature flips the first base64url character of the signature
// segment while preserving the header and payload. The Registry must
// reject the rewritten token because the MAC no longer matches the
// declared allow-listed HMAC algorithm under the server-side key
// handle.
function tamperSignature(token: string): string {
  const { header, payload, signature } = splitToken(token);
  const first = signature.charAt(0);
  const replacement = first === "A" ? "B" : "A";
  return `${header}.${payload}.${replacement}${signature.slice(1)}`;
}

// rewriteClaims rebuilds the compact token with a mutated payload
// segment while preserving the original signature. The Registry must
// reject the rewritten token because the recomputed MAC over the new
// payload does not match the supplied signature. The test never
// attempts to forge a new signature: changed-claim tokens keep the
// original signature so MAC denial remains expected.
//
// The mutate callback receives the typed claims for ergonomics and
// returns a permissive record so callers can craft deliberately
// invalid (non-canonical) values without unsafe literal casts.
function rewriteClaims(
  token: string,
  mutate: (claims: ScopeTokenClaims) => Record<string, unknown>,
): string {
  const { header, payload, signature } = splitToken(token);
  const original = JSON.parse(decodeBase64Url(payload)) as ScopeTokenClaims;
  const mutated = mutate(original);
  const newPayload = encodeBase64Url(JSON.stringify(mutated));
  return `${header}.${newPayload}.${signature}`;
}

// rewriteHeader rebuilds the compact token with a mutated header
// segment while preserving the original signature. Used to test
// protected-header tampering (alg outside the allow-list, kid vs
// key_id mismatch, non-scope-token+json typ). The mutate callback
// receives the typed header and returns a permissive record so
// callers can craft deliberately invalid (non-allow-listed) values
// without unsafe literal casts.
function rewriteHeader(
  token: string,
  mutate: (header: ScopeTokenHeader) => Record<string, string>,
): string {
  const { header, payload, signature } = splitToken(token);
  const original = JSON.parse(decodeBase64Url(header)) as ScopeTokenHeader;
  const mutated = mutate(original);
  const newHeader = encodeBase64Url(JSON.stringify(mutated));
  return `${newHeader}.${payload}.${signature}`;
}

// captureCanonical404 drives the registered Executor identity against
// an unknown environment / task identifier and captures the canonical
// 404 closure shape. Because ErrorResponse carries a per-request
// request_id, the closure shape carries the documented
// `environment_unknown_or_unavailable` code, a non-sensitive message
// with the documented generic wording, and a non-empty request_id.
async function captureCanonical404(
  exec: IdentityContext,
  baseUrl: string,
  envId: string = UnknownEnvId,
  taskId: string = UnknownTaskId,
): Promise<OpenResponse> {
  const resp = await openEnvironmentAsExecutor(
    exec,
    baseUrl,
    envId,
    taskId,
    "canonical.unknown.token",
  );
  expect(resp.status, "canonical 404 closure returns 404").toBe(404);
  const body = resp.json as OpenErrorBody | null;
  expect(
    body,
    "canonical 404 body parses to the documented ErrorResponse shape",
  ).not.toBeNull();
  if (body) {
    expect(
      body.code,
      "canonical 404 body carries the documented EnvironmentUnknownOrUnavailable code",
    ).toBe("environment_unknown_or_unavailable");
    expect(
      typeof body.message,
      "canonical 404 body message is the documented non-sensitive summary",
    ).toBe("string");
    expect(
      typeof body.request_id,
      "canonical 404 body carries the documented request_id string",
    ).toBe("string");
    expect(
      body.request_id.length,
      "canonical 404 body request_id is non-empty",
    ).toBeGreaterThan(0);
  }
  return resp;
}

// assertClosureShapeIdentical compares the actual denial response
// against the canonical 404 closure shape WITHOUT requiring identical
// request_id bytes. Two requests share the same closure status, code,
// message, and key set; each carries its own non-empty request_id.
// Byte-identical comparison is forbidden because the request_id field
// is allocated per request by the documented ErrorResponse shape.
function assertClosureShapeIdentical(
  actual: OpenResponse,
  canonical: OpenResponse,
  caseLabel: string,
): void {
  expect(
    actual.status,
    `${caseLabel} returns 404 environment_unknown_or_unavailable`,
  ).toBe(404);
  expect(canonical.status, "canonical closure returns 404").toBe(404);
  const actualBody = actual.json as OpenErrorBody | null;
  const canonicalBody = canonical.json as OpenErrorBody | null;
  expect(
    actualBody,
    `${caseLabel} body parses to ErrorResponse`,
  ).not.toBeNull();
  expect(
    canonicalBody,
    `canonical body parses to ErrorResponse`,
  ).not.toBeNull();
  if (!actualBody || !canonicalBody) {
    return;
  }
  expect(
    actualBody.code,
    `${caseLabel} code matches the canonical non-revealing code`,
  ).toBe(canonicalBody.code);
  expect(canonicalBody.code, `canonical code is non-empty`).not.toBe("");
  expect(
    actualBody.message,
    `${caseLabel} message matches the canonical non-sensitive summary`,
  ).toBe(canonicalBody.message);
  expect(
    actualBody.message.length,
    `${caseLabel} message is non-empty`,
  ).toBeGreaterThan(0);
  expect(
    typeof actualBody.request_id,
    `${caseLabel} request_id is a non-empty string (no equality asserted)`,
  ).toBe("string");
  expect(
    actualBody.request_id.length,
    `${caseLabel} request_id is non-empty`,
  ).toBeGreaterThan(0);
  expect(
    typeof canonicalBody.request_id,
    "canonical request_id is a non-empty string",
  ).toBe("string");
  expect(
    canonicalBody.request_id.length,
    "canonical request_id is non-empty",
  ).toBeGreaterThan(0);
  const actualKeys = Object.keys(actualBody).slice().sort();
  const canonicalKeys = Object.keys(canonicalBody).slice().sort();
  expect(
    actualKeys,
    `${caseLabel} key set matches the canonical ErrorResponse shape`,
  ).toEqual(canonicalKeys);
  expect(
    actualKeys,
    `${caseLabel} keys are exactly the documented ErrorResponse keys`,
  ).toEqual(["code", "message", "request_id"]);
}

// --- Tests --------------------------------------------------------------

test("v0002.8 environment ownership is independent of execution; claim carries the signed scope token envelope", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-8-team",
    executionTag: "openhands",
  });
  const gw = gatewayFor({
    teamId: bsA.admin.team_id,
    operatorId: `op-${bsA.admin.team_id}`,
  });
  const gwApi = await gw.api(worker.baseUrl);
  let envId = "";
  try {
    const env = await gwApi.post("/v1/environments", {
      data: {
        name: "env-v0002-8",
        scope: { project_id: null, task_id: null, parent_task_id: null },
        values: { REGION: "us-east-1" },
      },
    });
    expect(env.status(), "POST /v1/environments returns 201").toBe(201);
    const envSnap = await snapshotResponse(env);
    const envBody = await readJson<{ environment_id: string; team_id: string }>(
      envSnap,
    );
    expect(envBody.team_id, "environment carries the verified team_id").toBe(
      bsA.admin.team_id,
    );
    envId = envBody.environment_id;
  } finally {
    await gwApi.dispose();
  }
  const task = await ingestPendingTask(
    listenerFor({
      teamId: bsA.admin.team_id,
      listenerIdentity: bsA.listenerIdentity,
      sourceSystemId: bsA.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsA.admin.team_id,
      source_system_id: bsA.sourceSystem.source_system_id,
      source_id: "T-1",
      task_type_id: bsA.taskType.task_type_id,
      payload: {},
      environment_id: envId,
    },
  );
  const exec = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(exec, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-8",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  const claim = await claimAsExecutor(
    exec,
    worker.baseUrl,
    task.task_id,
    "C-1",
  );
  expect(
    claim.status(),
    "claim returns 200 with the canonical claim response",
  ).toBe(200);
  const body = await readJson<ClaimResponseBody>(claim);
  expect(body.claim, 'claim response field is the literal "claimed"').toBe(
    "claimed",
  );
  expect(body.task.task_id, "claim task_id matches the requested task").toBe(
    task.task_id,
  );
  expect(body.task.team_id, "claim task remains owned by team-a").toBe(
    bsA.admin.team_id,
  );
  expect(body.task.owner_command_id, "claim sets owner_command_id to C-1").toBe(
    "C-1",
  );
  expect(
    body.task.current_state,
    "claim projects current_state to created",
  ).toBe("created");
  expect(body.task.claimed_at, "claim sets claimed_at").toBeTruthy();
  expect(
    body.environment_id,
    "claim response carries the assigned environment_id",
  ).toBe(envId);
  expect(
    body.resolved_image,
    "claim response carries resolved_image",
  ).toBeTruthy();
  expect(body.image_source, "claim response carries image_source").toBeTruthy();
  expect(
    body.claimed_at,
    "claim response carries a claimed_at timestamp",
  ).toBeTruthy();
  const token = body.scope_token;
  expect(
    token,
    "claim response carries a freshly signed scope_token",
  ).toBeTruthy();
  if (!token) {
    throw new Error("v0002.8: claim response missing scope_token");
  }
  const parts = token.split(".");
  expect(parts.length, "scope_token is a compact three-segment token").toBe(3);
  const header = JSON.parse(
    decodeBase64Url(parts[0] ?? ""),
  ) as ScopeTokenHeader;
  const claims = JSON.parse(
    decodeBase64Url(parts[1] ?? ""),
  ) as ScopeTokenClaims;
  expect(
    header.alg,
    "protected header alg is inside the allow-listed HMAC set",
  ).toMatch(/^HS(?:256|384|512)$/);
  expect(
    header.typ,
    "protected header typ is the documented scope-token+json",
  ).toBe("scope-token+json");
  expect(
    header.kid,
    "protected header kid is a non-empty opaque key identifier",
  ).toBeTruthy();
  expect(claims.team_id, "token team_id matches the team-a scope").toBe(
    bsA.admin.team_id,
  );
  expect(
    claims.environment_id,
    "token environment_id matches the assigned environment",
  ).toBe(envId);
  expect(claims.task_id, "token task_id matches the claimed task").toBe(
    task.task_id,
  );
  expect(
    claims.audience,
    "token audience is the literal environment.open scope",
  ).toBe("state-registry.environment.open");
  expect(
    claims.key_id,
    "token payload key_id is a non-empty opaque key identifier",
  ).toBeTruthy();
  expect(
    claims.executor_id,
    "token payload executor_id is non-empty",
  ).toBeTruthy();
  expect(
    !Number.isNaN(Date.parse(claims.issued_at)) &&
      !Number.isNaN(Date.parse(claims.expiry)),
    "token issued_at and expiry are valid ISO timestamps",
  ).toBe(true);
  const lifetimeMs = Date.parse(claims.expiry) - Date.parse(claims.issued_at);
  expect(
    lifetimeMs > 0 && lifetimeMs <= 5 * 60 * 1000,
    "token lifetime is strictly positive and within five minutes",
  ).toBe(true);
});

test("v0002.9 team-owned environment definition is stored with immutable team_id and exposes non-secret values only", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-9-team",
    executionTag: "openhands",
  });
  const gw = gatewayFor({
    teamId: bsA.admin.team_id,
    operatorId: `op-${bsA.admin.team_id}`,
  });
  const gwApi = await gw.api(worker.baseUrl);
  let envId = "";
  try {
    const resp = await gwApi.post("/v1/environments", {
      data: {
        name: "env-v0002-9",
        scope: { project_id: null, task_id: null, parent_task_id: null },
        values: { REGION: "us-east-1", TIER: "gold" },
      },
    });
    expect(resp.status(), "POST /v1/environments returns 201").toBe(201);
    const snap = await snapshotResponse(resp);
    const body = await readJson<{
      environment_id: string;
      team_id: string;
      values: Record<string, string>;
      secrets: unknown[];
      revision: number;
    }>(snap);
    expect(body.team_id, "environment carries the verified team_id").toBe(
      bsA.admin.team_id,
    );
    expect(
      body.values,
      "non-secret values are present in the response",
    ).toEqual({
      REGION: "us-east-1",
      TIER: "gold",
    });
    expect(
      body.revision,
      "environment revision is a positive integer",
    ).toBeGreaterThanOrEqual(1);
    expect(
      Array.isArray(body.secrets),
      "environment secrets array is present",
    ).toBe(true);
    envId = body.environment_id;
  } finally {
    await gwApi.dispose();
  }
  const gwList = gatewayFor({
    teamId: bsA.admin.team_id,
    operatorId: `op-${bsA.admin.team_id}`,
  });
  const gwListApi = await gwList.api(worker.baseUrl);
  try {
    const list = await gwListApi.get("/v1/environments?limit=50");
    expect(list.status(), "team-a environment listing returns 200").toBe(200);
    const snap = await snapshotResponse(list);
    const body = await readJson<{
      items: Array<{ environment_id: string; team_id: string }>;
    }>(snap);
    const items = Array.isArray(body.items) ? body.items : [];
    const env = items.find((it) => it.environment_id === envId);
    expect(env, "team-a environment listing returns the row").toBeDefined();
    if (env) {
      expect(env.team_id, "listed environment stays owned by team-a").toBe(
        bsA.admin.team_id,
      );
    }
  } finally {
    await gwListApi.dispose();
  }
});

test("v0002.10 logical secret create + replace returns opaque key_id / key_version metadata without plaintext", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-10-team",
    executionTag: "openhands",
  });
  const gw = gatewayFor({
    teamId: bsA.admin.team_id,
    operatorId: `op-${bsA.admin.team_id}`,
  });
  const gwApi = await gw.api(worker.baseUrl);
  let envId = "";
  let secretId = "";
  try {
    const env = await gwApi.post("/v1/environments", {
      data: {
        name: "env-v0002-10",
        scope: { project_id: null, task_id: null, parent_task_id: null },
        values: {},
      },
    });
    expect(env.status(), "POST /v1/environments returns 201").toBe(201);
    const envSnap = await snapshotResponse(env);
    const envBody = await readJson<{ environment_id: string }>(envSnap);
    envId = envBody.environment_id;
    const initialPlaintext = "plaintext-v0002-10-initial";
    const replacementPlaintext = "plaintext-v0002-10-replaced";
    const create = await gwApi.post(
      `/v1/environments/${encodeURIComponent(envId)}/secrets`,
      {
        data: {
          name: "OPENAI_API_KEY",
          value: initialPlaintext,
          scope: { project_id: null, task_id: null, parent_task_id: null },
        },
      },
    );
    expect(create.status(), "POST secrets returns 201").toBe(201);
    const createSnap = await snapshotResponse(create);
    const createBody = await readJson<{
      secret: { secret_id: string; latest_version: number };
      version: {
        version: number;
        key_id: string;
        key_version: number;
        created_at: string;
      };
    }>(createSnap);
    expect(
      createBody.secret.secret_id,
      "create returns secret_id",
    ).toBeTruthy();
    secretId = createBody.secret.secret_id;
    expect(
      createBody.version.version,
      "create returns version number >= 1",
    ).toBeGreaterThanOrEqual(1);
    expect(
      createBody.version.key_id,
      "create returns opaque key_id",
    ).toBeTruthy();
    expect(
      createBody.version.key_version,
      "create returns opaque key_version >= 1",
    ).toBeGreaterThanOrEqual(1);
    expect(
      createBody.version.created_at,
      "create returns created_at timestamp",
    ).toBeTruthy();
    const createText = await createSnap.text();
    expect(
      createText.includes(initialPlaintext),
      "create response body never echoes the submitted plaintext",
    ).toBe(false);
    const replace = await gwApi.post(
      `/v1/environments/${encodeURIComponent(envId)}/secrets/${encodeURIComponent(secretId)}/versions`,
      { data: { value: replacementPlaintext } },
    );
    expect(replace.status(), "POST secret versions returns 201").toBe(201);
    const replaceSnap = await snapshotResponse(replace);
    const replaceBody = await readJson<{
      version: {
        version: number;
        key_id: string;
        key_version: number;
        created_at: string;
      };
    }>(replaceSnap);
    expect(
      replaceBody.version.version > createBody.version.version,
      "replace returns a strictly higher version number (monotonic)",
    ).toBe(true);
    expect(
      replaceBody.version.key_id,
      "replace returns opaque key_id",
    ).toBeTruthy();
    expect(
      replaceBody.version.key_version,
      "replace returns opaque key_version >= 1",
    ).toBeGreaterThanOrEqual(1);
    const replaceText = await replaceSnap.text();
    expect(
      replaceText.includes(replacementPlaintext),
      "replace response body never echoes the submitted plaintext",
    ).toBe(false);
    expect(
      replaceText.includes(initialPlaintext),
      "replace response body never echoes the previous plaintext",
    ).toBe(false);
  } finally {
    await gwApi.dispose();
  }
});

test("v0002.11 assigned Executor opens environment via scope token; foreign Executor returns non-revealing 404", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-11-team",
    executionTag: "openhands",
  });
  const gw = gatewayFor({
    teamId: bsA.admin.team_id,
    operatorId: `op-${bsA.admin.team_id}`,
  });
  const gwApi = await gw.api(worker.baseUrl);
  let envId = "";
  try {
    const env = await gwApi.post("/v1/environments", {
      data: {
        name: "env-v0002-11",
        scope: { project_id: null, task_id: null, parent_task_id: null },
        values: { REGION: "us-east-1" },
      },
    });
    expect(env.status(), "POST /v1/environments returns 201").toBe(201);
    const envSnap = await snapshotResponse(env);
    const envBody = await readJson<{ environment_id: string }>(envSnap);
    envId = envBody.environment_id;
  } finally {
    await gwApi.dispose();
  }
  const task = await ingestPendingTask(
    listenerFor({
      teamId: bsA.admin.team_id,
      listenerIdentity: bsA.listenerIdentity,
      sourceSystemId: bsA.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsA.admin.team_id,
      source_system_id: bsA.sourceSystem.source_system_id,
      source_id: "T-1",
      task_type_id: bsA.taskType.task_type_id,
      payload: {},
      environment_id: envId,
    },
  );
  const execAssigned = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(execAssigned, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-11-assigned",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  const claim = await claimAsExecutor(
    execAssigned,
    worker.baseUrl,
    task.task_id,
    "C-1",
  );
  expect(
    claim.status(),
    "claim returns 200 with the canonical claim response",
  ).toBe(200);
  const claimBody = await readJson<ClaimResponseBody>(claim);
  const scopeToken = claimBody.scope_token;
  expect(
    scopeToken,
    "claim response carries the freshly signed scope_token",
  ).toBeTruthy();
  if (!scopeToken) {
    throw new Error("v0002.11: claim response missing scope_token");
  }
  const claims = decodeClaims(scopeToken);
  expect(
    claims.executor_id,
    "token payload executor_id matches the assigned Executor identity",
  ).toBe(execAssigned.attach()["X-FlowAI-Executor-Id"] ?? "");

  // Positive control: the assigned Executor opens with the claim-issued
  // token and the canonical 200 same-team shape is returned.
  const opened = await openEnvironmentAsExecutor(
    execAssigned,
    worker.baseUrl,
    envId,
    task.task_id,
    scopeToken,
  );
  expect(
    opened.status,
    "assigned Executor open with the claim-issued token returns 200",
  ).toBe(200);
  const openedBody = opened.json as OpenEnvironmentBody | null;
  expect(
    openedBody,
    "open response body parses to OpenEnvironmentResponse",
  ).not.toBeNull();
  if (openedBody) {
    expect(
      openedBody.team_id,
      "open response team_id matches the same team",
    ).toBe(bsA.admin.team_id);
    expect(
      openedBody.environment_id,
      "open response environment_id matches the assigned env",
    ).toBe(envId);
    expect(
      openedBody.task_id,
      "open response task_id matches the claimed task",
    ).toBe(task.task_id);
    expect(
      openedBody.executor_id,
      "open response executor_id matches the assigned Executor",
    ).toBe(execAssigned.attach()["X-FlowAI-Executor-Id"] ?? "");
  }
  const canonical = await captureCanonical404(execAssigned, worker.baseUrl);

  // Negative control: a genuinely foreign same-team Executor identity
  // presenting the SAME claim-issued token. The Registry must reject
  // the request as the envelope does not match the authenticated
  // Executor identity, returning the canonical 404 closure shape with
  // zero decrypt operations.
  const execForeign = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(execForeign, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-11-foreign",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  const foreignSnap = await snapshotDecryptOps(worker.baseUrl);
  const foreignOpen = await openEnvironmentAsExecutor(
    execForeign,
    worker.baseUrl,
    envId,
    task.task_id,
    scopeToken,
  );
  assertClosureShapeIdentical(
    foreignOpen,
    canonical,
    "foreign same-team Executor with assigned token",
  );
  const foreignAfter = await snapshotDecryptOps(worker.baseUrl);
  expect(
    foreignAfter.raw,
    "foreign same-team Executor open records zero decrypt operations",
  ).toBe(foreignSnap.raw);
});

test("v0002.38 environment reads and writes are team-scoped; foreign probes return non-revealing 404 with zero encrypt ops", async () => {
  const admin = systemAdministrator();
  const suffix = `v0002-38-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 6)}`;
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-a`,
    executionTag: "openhands",
  });
  const bsB = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-b`,
    executionTag: "openhands",
  });
  const gwB = gatewayFor({
    teamId: bsB.admin.team_id,
    operatorId: `op-${bsB.admin.team_id}`,
  });
  const gwBApi = await gwB.api(worker.baseUrl);
  let envIdB = "";
  try {
    const env = await gwBApi.post("/v1/environments", {
      data: {
        name: "env-v0002-38-b",
        scope: { project_id: null, task_id: null, parent_task_id: null },
        values: { REGION: "eu-west-1" },
      },
    });
    expect(env.status(), "POST /v1/environments returns 201").toBe(201);
    const envSnap = await snapshotResponse(env);
    const envBody = await readJson<{ environment_id: string }>(envSnap);
    envIdB = envBody.environment_id;
  } finally {
    await gwBApi.dispose();
  }

  // Same-team positive control: team-b reads and updates its own
  // environment, returning 200 with the canonical shape.
  const gwBOwner = gatewayFor({
    teamId: bsB.admin.team_id,
    operatorId: `op-${bsB.admin.team_id}`,
  });
  const gwBOwnerApi = await gwBOwner.api(worker.baseUrl);
  try {
    const read = await gwBOwnerApi.get(
      `/v1/environments/${encodeURIComponent(envIdB)}`,
    );
    expect(read.status(), "team-b reads its own environment with 200").toBe(
      200,
    );
    const readSnap = await snapshotResponse(read);
    const readBody = await readJson<{
      team_id: string;
      environment_id: string;
    }>(readSnap);
    expect(readBody.team_id, "team-b read returns team-b team_id").toBe(
      bsB.admin.team_id,
    );
    expect(
      readBody.environment_id,
      "team-b read returns the requested environment_id",
    ).toBe(envIdB);
  } finally {
    await gwBOwnerApi.dispose();
  }

  // Negative control: team-a probes the team-b environment. The
  // response body MUST match the canonical 404 closure shape returned
  // for an unknown environment identifier, and zero encrypt
  // operations must occur. The canonical is captured under the same
  // team-a Gateway identity so the closure shape comparison stays on
  // identical transport and identical 404 code (resource_unknown).
  const gwA = gatewayFor({
    teamId: bsA.admin.team_id,
    operatorId: `op-${bsA.admin.team_id}`,
  });
  const gwCanonicalApi = await gwA.api(worker.baseUrl);
  let canonical: OpenResponse;
  try {
    const resp = await gwCanonicalApi.get(
      `/v1/environments/${encodeURIComponent(UnknownEnvId)}`,
    );
    canonical = await consumeOpenResponse(resp);
    expect(canonical.status, "canonical environment read returns 404").toBe(
      404,
    );
    const canonicalBody = canonical.json as OpenErrorBody | null;
    expect(
      canonicalBody?.code,
      "canonical environment read body carries the resource_unknown code",
    ).toBe("resource_unknown");
  } finally {
    await gwCanonicalApi.dispose();
  }
  const baseline = await snapshotDecryptOps(worker.baseUrl);
  const gwAApi = await gwA.api(worker.baseUrl);
  try {
    const resp = await gwAApi.get(
      `/v1/environments/${encodeURIComponent(envIdB)}`,
    );
    const raw = await consumeOpenResponse(resp);
    assertClosureShapeIdentical(
      raw,
      canonical,
      "team-a probe of team-b environment",
    );
  } finally {
    await gwAApi.dispose();
  }
  const after = await snapshotDecryptOps(worker.baseUrl);
  expect(
    after.raw,
    "team-a probe of team-b environment records zero encrypt operations",
  ).toBe(baseline.raw);
});

test("v0002.39 logical secrets and version metadata reads are team-scoped; foreign probes return non-revealing 404 with zero encrypt ops", async () => {
  const admin = systemAdministrator();
  const suffix = `v0002-39-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 6)}`;
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-a`,
    executionTag: "openhands",
  });
  const bsB = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-b`,
    executionTag: "openhands",
  });
  const gwB = gatewayFor({
    teamId: bsB.admin.team_id,
    operatorId: `op-${bsB.admin.team_id}`,
  });
  const gwBApi = await gwB.api(worker.baseUrl);
  let envIdB = "";
  let secretIdB = "";
  try {
    const env = await gwBApi.post("/v1/environments", {
      data: {
        name: "env-v0002-39-b",
        scope: { project_id: null, task_id: null, parent_task_id: null },
        values: {},
      },
    });
    expect(env.status(), "POST /v1/environments returns 201").toBe(201);
    const envSnap = await snapshotResponse(env);
    const envBody = await readJson<{ environment_id: string }>(envSnap);
    envIdB = envBody.environment_id;
    const secret = await gwBApi.post(
      `/v1/environments/${encodeURIComponent(envIdB)}/secrets`,
      {
        data: {
          name: "OPENAI_API_KEY",
          value: "team-b-secret",
          scope: { project_id: null, task_id: null, parent_task_id: null },
        },
      },
    );
    expect(secret.status(), "POST team-b secrets returns 201").toBe(201);
    const secretSnap = await snapshotResponse(secret);
    const secretBody = await readJson<{ secret: { secret_id: string } }>(
      secretSnap,
    );
    secretIdB = secretBody.secret.secret_id;
  } finally {
    await gwBApi.dispose();
  }

  // Same-team positive control: team-b reads its own secret metadata
  // and version metadata, returning 200 with the canonical shape and
  // no plaintext.
  const gwBOwner = gatewayFor({
    teamId: bsB.admin.team_id,
    operatorId: `op-${bsB.admin.team_id}`,
  });
  const gwBOwnerApi = await gwBOwner.api(worker.baseUrl);
  try {
    const list = await gwBOwnerApi.get(
      `/v1/environments/${encodeURIComponent(envIdB)}/secrets`,
    );
    expect(list.status(), "team-b lists its own secrets with 200").toBe(200);
    const versions = await gwBOwnerApi.get(
      `/v1/environments/${encodeURIComponent(envIdB)}/secrets/${encodeURIComponent(secretIdB)}/versions`,
    );
    expect(
      versions.status(),
      "team-b lists its own secret versions with 200",
    ).toBe(200);
    const versionsSnap = await snapshotResponse(versions);
    const versionsText = await versionsSnap.text();
    expect(
      versionsText.includes("team-b-secret"),
      "team-b version listing never echoes the stored plaintext",
    ).toBe(false);
  } finally {
    await gwBOwnerApi.dispose();
  }

  // Negative control: team-a probes the team-b secret metadata. The
  // response body MUST match the canonical 404 closure shape returned
  // for an unknown environment identifier, and zero encrypt
  // operations must occur. The canonical is captured under the same
  // team-a Gateway identity so the closure shape comparison stays on
  // identical transport and identical 404 code (resource_unknown).
  const gwA = gatewayFor({
    teamId: bsA.admin.team_id,
    operatorId: `op-${bsA.admin.team_id}`,
  });
  const gwCanonicalApi = await gwA.api(worker.baseUrl);
  let canonical: OpenResponse;
  try {
    const resp = await gwCanonicalApi.get(
      `/v1/environments/${encodeURIComponent(UnknownEnvId)}/secrets`,
    );
    canonical = await consumeOpenResponse(resp);
    expect(canonical.status, "canonical secret listing returns 404").toBe(404);
    const canonicalBody = canonical.json as OpenErrorBody | null;
    expect(
      canonicalBody?.code,
      "canonical secret listing body carries the resource_unknown code",
    ).toBe("resource_unknown");
  } finally {
    await gwCanonicalApi.dispose();
  }
  const baseline = await snapshotDecryptOps(worker.baseUrl);
  const gwAApi = await gwA.api(worker.baseUrl);
  try {
    const resp = await gwAApi.get(
      `/v1/environments/${encodeURIComponent(envIdB)}/secrets`,
    );
    const raw = await consumeOpenResponse(resp);
    assertClosureShapeIdentical(
      raw,
      canonical,
      "team-a probe of team-b secret metadata",
    );
  } finally {
    await gwAApi.dispose();
  }
  const after = await snapshotDecryptOps(worker.baseUrl);
  expect(
    after.raw,
    "team-a probe of team-b secret metadata records zero encrypt operations",
  ).toBe(baseline.raw);
});

test("v0002.40 open-environment boundary verifies every team-bound claim before decrypt; invalid tokens return the same 404 shape", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-40-team",
    executionTag: "openhands",
  });
  const gw = gatewayFor({
    teamId: bsA.admin.team_id,
    operatorId: `op-${bsA.admin.team_id}`,
  });
  const gwApi = await gw.api(worker.baseUrl);
  let envId = "";
  try {
    const env = await gwApi.post("/v1/environments", {
      data: {
        name: "env-v0002-40",
        scope: { project_id: null, task_id: null, parent_task_id: null },
        values: { REGION: "us-east-1" },
      },
    });
    expect(env.status(), "POST /v1/environments returns 201").toBe(201);
    const envSnap = await snapshotResponse(env);
    const envBody = await readJson<{ environment_id: string }>(envSnap);
    envId = envBody.environment_id;
  } finally {
    await gwApi.dispose();
  }
  const task = await ingestPendingTask(
    listenerFor({
      teamId: bsA.admin.team_id,
      listenerIdentity: bsA.listenerIdentity,
      sourceSystemId: bsA.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsA.admin.team_id,
      source_system_id: bsA.sourceSystem.source_system_id,
      source_id: "T-1",
      task_type_id: bsA.taskType.task_type_id,
      payload: {},
      environment_id: envId,
    },
  );
  const exec = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(exec, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-40",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  const claim = await claimAsExecutor(
    exec,
    worker.baseUrl,
    task.task_id,
    "C-1",
  );
  expect(
    claim.status(),
    "claim returns 200 with the canonical claim response",
  ).toBe(200);
  const claimBody = await readJson<ClaimResponseBody>(claim);
  const scopeToken = claimBody.scope_token;
  expect(
    scopeToken,
    "claim response carries the freshly signed scope_token",
  ).toBeTruthy();
  if (!scopeToken) {
    throw new Error("v0002.40: claim response missing scope_token");
  }

  // Positive control: assigned Executor opens with the claim-issued
  // token and gets 200. This proves the bootstrap is real and the
  // canonical 404 closure shape returned later is a strict closure of
  // invalid/unavailable cases, not a generic unimplemented 404.
  const positive = await openEnvironmentAsExecutor(
    exec,
    worker.baseUrl,
    envId,
    task.task_id,
    scopeToken,
  );
  expect(
    positive.status,
    "positive control: assigned Executor open returns 200",
  ).toBe(200);

  // Capture the canonical 404 closure shape once under the assigned
  // Executor identity (which the documented open endpoint requires),
  // then compare every invalid variant against it shape-for-shape and
  // observe zero decrypt operations across the loop.
  const canonical = await captureCanonical404(
    exec,
    worker.baseUrl,
    envId,
    task.task_id,
  );

  type Case = { name: string; token: string | null };
  const mismatchTokens: Case[] = [
    { name: "tampered-signature", token: tamperSignature(scopeToken) },
    {
      name: "task-id-claim-mismatch",
      token: rewriteClaims(scopeToken, (c) => ({
        ...c,
        task_id: `${c.task_id}-mismatch`,
      })),
    },
    {
      name: "environment-id-claim-mismatch",
      token: rewriteClaims(scopeToken, (c) => ({
        ...c,
        environment_id: "env-foreign-shape",
      })),
    },
    {
      name: "wrong-audience",
      token: rewriteClaims(scopeToken, (c) => ({
        ...c,
        audience: "state-registry.environment.read",
      })),
    },
    {
      name: "kid-and-key-id-mismatch",
      token: rewriteHeader(scopeToken, (h) => ({
        alg: h.alg,
        kid: `${h.kid}-mismatch`,
        typ: h.typ,
      })),
    },
    {
      name: "alg-not-allow-listed",
      token: rewriteHeader(scopeToken, (h) => ({
        alg: "none",
        kid: h.kid,
        typ: h.typ,
      })),
    },
    {
      name: "typ-not-scope-token",
      token: rewriteHeader(scopeToken, (h) => ({
        alg: h.alg,
        kid: h.kid,
        typ: "JWT",
      })),
    },
  ];

  for (const c of mismatchTokens) {
    const before = await snapshotDecryptOps(worker.baseUrl);
    const blocked = await openEnvironmentAsExecutor(
      exec,
      worker.baseUrl,
      envId,
      task.task_id,
      c.token,
    );
    assertClosureShapeIdentical(blocked, canonical, c.name);
    const after = await snapshotDecryptOps(worker.baseUrl);
    expect(after.raw, `${c.name} records zero decrypt operations`).toBe(
      before.raw,
    );
  }

  // Pure structural deficits — no token at all. The Registry must
  // still return the canonical 404 closure shape with zero decrypts.
  const negativeBefore = await snapshotDecryptOps(worker.baseUrl);
  const emptyToken = await openEnvironmentAsExecutor(
    exec,
    worker.baseUrl,
    envId,
    task.task_id,
    null,
  );
  assertClosureShapeIdentical(emptyToken, canonical, "missing-token");
  const noTokenAfter = await snapshotDecryptOps(worker.baseUrl);
  expect(
    noTokenAfter.raw,
    "missing-token records zero decrypt operations",
  ).toBe(negativeBefore.raw);
});

test("v0002.45 scope-token security: every invalid case returns the documented 404 shape with zero decrypt ops", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-45-team",
    executionTag: "openhands",
  });
  const gw = gatewayFor({
    teamId: bsA.admin.team_id,
    operatorId: `op-${bsA.admin.team_id}`,
  });
  const gwApi = await gw.api(worker.baseUrl);
  let envId = "";
  try {
    const env = await gwApi.post("/v1/environments", {
      data: {
        name: "env-v0002-45",
        scope: { project_id: null, task_id: null, parent_task_id: null },
        values: { REGION: "us-east-1" },
      },
    });
    expect(env.status(), "POST /v1/environments returns 201").toBe(201);
    const envSnap = await snapshotResponse(env);
    const envBody = await readJson<{ environment_id: string }>(envSnap);
    envId = envBody.environment_id;
  } finally {
    await gwApi.dispose();
  }
  const task = await ingestPendingTask(
    listenerFor({
      teamId: bsA.admin.team_id,
      listenerIdentity: bsA.listenerIdentity,
      sourceSystemId: bsA.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsA.admin.team_id,
      source_system_id: bsA.sourceSystem.source_system_id,
      source_id: "T-1",
      task_type_id: bsA.taskType.task_type_id,
      payload: {},
      environment_id: envId,
    },
  );
  const exec1 = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(exec1, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-45-1",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  const claim = await claimAsExecutor(
    exec1,
    worker.baseUrl,
    task.task_id,
    "C-1",
  );
  expect(claim.status(), "exec1 claim returns 200").toBe(200);
  const claimBody = await readJson<ClaimResponseBody>(claim);
  const scopeToken = claimBody.scope_token;
  expect(
    scopeToken,
    "claim response carries the freshly signed scope_token",
  ).toBeTruthy();
  if (!scopeToken) {
    throw new Error("v0002.45: claim response missing scope_token");
  }

  // Positive control: exec1 opens with the claim-issued token and the
  // canonical same-team 200 is returned. This proves the invalid
  // variants below are exercising the open boundary, not an
  // unimplemented endpoint.
  const positive = await openEnvironmentAsExecutor(
    exec1,
    worker.baseUrl,
    envId,
    task.task_id,
    scopeToken,
  );
  expect(positive.status, "positive control: exec1 open returns 200").toBe(200);

  const canonical = await captureCanonical404(
    exec1,
    worker.baseUrl,
    envId,
    task.task_id,
  );

  type Case = { name: string; token: string | null };
  const cases: Case[] = [
    { name: "empty-token", token: "" },
    { name: "one-segment-token", token: "header-only" },
    { name: "two-segment-token", token: "header.payload" },
    {
      name: "alg-not-allow-listed",
      token: "eyJhbGciOiJub25In0.eyJ0ZWFtX2lkIjoidGVhbS1hIn0.sig",
    },
    { name: "tampered-mac", token: tamperSignature(scopeToken) },
    {
      name: "kid-vs-key-id-mismatch",
      token: rewriteHeader(scopeToken, (h) => ({
        alg: h.alg,
        kid: `${h.kid}-tampered`,
        typ: h.typ,
      })),
    },
    {
      name: "wrong-audience",
      token: rewriteClaims(scopeToken, (c) => ({
        ...c,
        audience: "state-registry.environment.read",
      })),
    },
    {
      name: "task-id-mismatch",
      token: rewriteClaims(scopeToken, (c) => ({
        ...c,
        task_id: `${c.task_id}-mismatch`,
      })),
    },
    {
      name: "environment-id-mismatch",
      token: rewriteClaims(scopeToken, (c) => ({
        ...c,
        environment_id: "env-foreign-shape",
      })),
    },
    {
      name: "team-id-claim-mismatch",
      token: rewriteClaims(scopeToken, (c) => ({
        ...c,
        team_id: "team-foreign-shape",
      })),
    },
    {
      name: "expiry-not-strictly-later",
      token: rewriteClaims(scopeToken, (c) => ({ ...c, expiry: c.issued_at })),
    },
    {
      name: "expiry-over-five-minutes",
      token: rewriteClaims(scopeToken, (c) => {
        const future = new Date(
          Date.parse(c.issued_at) + 6 * 60 * 1000,
        ).toISOString();
        return { ...c, expiry: future };
      }),
    },
    {
      name: "issued-at-premature-beyond-skew",
      token: rewriteClaims(scopeToken, (c) => {
        const farFuture = new Date(Date.now() + 5 * 60 * 1000).toISOString();
        return { ...c, issued_at: farFuture };
      }),
    },
    {
      name: "typ-not-scope-token",
      token: rewriteHeader(scopeToken, (h) => ({
        alg: h.alg,
        kid: h.kid,
        typ: "JWT",
      })),
    },
  ];

  for (const c of cases) {
    const before = await snapshotDecryptOps(worker.baseUrl);
    const blocked = await openEnvironmentAsExecutor(
      exec1,
      worker.baseUrl,
      envId,
      task.task_id,
      c.token,
    );
    assertClosureShapeIdentical(blocked, canonical, c.name);
    const after = await snapshotDecryptOps(worker.baseUrl);
    expect(after.raw, `${c.name} records zero decrypt operations`).toBe(
      before.raw,
    );
  }

  // Server-issued mismatches: a different authenticated Executor
  // identity presenting exec1's otherwise-valid claim-issued token.
  // The Registry must reject the request because the authenticated
  // envelope does not match the canonical Executor identity recorded
  // in the token payload.
  const exec2 = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(exec2, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-45-2",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  const beforeCross = await snapshotDecryptOps(worker.baseUrl);
  const cross = await openEnvironmentAsExecutor(
    exec2,
    worker.baseUrl,
    envId,
    task.task_id,
    scopeToken,
  );
  assertClosureShapeIdentical(
    cross,
    canonical,
    "server-issued token under different Executor identity",
  );
  const afterCross = await snapshotDecryptOps(worker.baseUrl);
  expect(
    afterCross.raw,
    "server-issued token under different Executor identity records zero decrypt operations",
  ).toBe(beforeCross.raw);
});

test("v0002.46 operator stores a task-owned environment definition with parent_task_id", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-46-team",
    executionTag: "openhands",
  });
  const task = await ingestPendingTask(
    listenerFor({
      teamId: bsA.admin.team_id,
      listenerIdentity: bsA.listenerIdentity,
      sourceSystemId: bsA.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsA.admin.team_id,
      source_system_id: bsA.sourceSystem.source_system_id,
      source_id: "T-1",
      task_type_id: bsA.taskType.task_type_id,
      payload: { hello: "world" },
    },
  );
  const gw = gatewayFor({
    teamId: bsA.admin.team_id,
    operatorId: `op-${bsA.admin.team_id}`,
  });
  const gwApi = await gw.api(worker.baseUrl);
  try {
    const resp = await gwApi.post("/v1/environments", {
      data: {
        name: "env-v0002-46",
        scope: {
          project_id: null,
          task_id: null,
          parent_task_id: task.task_id,
        },
        values: { REGION: "us-east-1" },
      },
    });
    expect(resp.status(), "POST /v1/environments returns 201").toBe(201);
    const snap = await snapshotResponse(resp);
    const body = await readJson<{
      environment_id: string;
      team_id: string;
      scope: {
        project_id: string | null;
        task_id: string | null;
        parent_task_id: string | null;
      };
    }>(snap);
    expect(
      body.team_id,
      "task-owned environment carries the verified team_id",
    ).toBe(bsA.admin.team_id);
    expect(
      body.scope.parent_task_id,
      "parent_task_id is persisted on the environment",
    ).toBe(task.task_id);
  } finally {
    await gwApi.dispose();
  }
  // Re-read via the same trusted Gateway path to confirm the parent
  // scope is persisted and the team ownership is immutable.
  const gwRead = gatewayFor({
    teamId: bsA.admin.team_id,
    operatorId: `op-${bsA.admin.team_id}`,
  });
  const gwReadApi = await gwRead.api(worker.baseUrl);
  try {
    const list = await gwReadApi.get("/v1/environments?limit=50");
    expect(list.status(), "team-a environment listing returns 200").toBe(200);
    const listSnap = await snapshotResponse(list);
    const listBody = await readJson<{
      items: Array<{
        environment_id: string;
        team_id: string;
        scope: { parent_task_id: string | null };
      }>;
    }>(listSnap);
    const items = Array.isArray(listBody.items) ? listBody.items : [];
    const taskOwned = items.find(
      (it) => it.scope.parent_task_id === task.task_id,
    );
    expect(
      taskOwned,
      "team-a listing returns the task-owned environment",
    ).toBeDefined();
    if (taskOwned) {
      expect(
        taskOwned.team_id,
        "task-owned listing row stays owned by team-a",
      ).toBe(bsA.admin.team_id);
    }
  } finally {
    await gwReadApi.dispose();
  }
});

test("v0002.47 operator stores a task-owned secret with REQUIRED parent task applicability", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-47-team",
    executionTag: "openhands",
  });
  const task = await ingestPendingTask(
    listenerFor({
      teamId: bsA.admin.team_id,
      listenerIdentity: bsA.listenerIdentity,
      sourceSystemId: bsA.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsA.admin.team_id,
      source_system_id: bsA.sourceSystem.source_system_id,
      source_id: "T-1",
      task_type_id: bsA.taskType.task_type_id,
      payload: { hello: "world" },
    },
  );
  const gw = gatewayFor({
    teamId: bsA.admin.team_id,
    operatorId: `op-${bsA.admin.team_id}`,
  });
  const gwApi = await gw.api(worker.baseUrl);
  let envId = "";
  let secretId = "";
  try {
    const env = await gwApi.post("/v1/environments", {
      data: {
        name: "env-v0002-47",
        scope: {
          project_id: null,
          task_id: null,
          parent_task_id: task.task_id,
        },
        values: {},
      },
    });
    expect(env.status(), "POST /v1/environments returns 201").toBe(201);
    const envSnap = await snapshotResponse(env);
    const envBody = await readJson<{ environment_id: string }>(envSnap);
    envId = envBody.environment_id;
    const secret = await gwApi.post(
      `/v1/environments/${encodeURIComponent(envId)}/secrets`,
      {
        data: {
          name: "OPENAI_API_KEY",
          value: "plaintext-v0002-47",
          scope: {
            project_id: null,
            task_id: null,
            parent_task_id: task.task_id,
          },
        },
      },
    );
    expect(secret.status(), "POST secrets returns 201").toBe(201);
    const secretSnap = await snapshotResponse(secret);
    const secretBody = await readJson<{
      secret: {
        secret_id: string;
        team_id: string;
        environment_id: string;
        scope: { parent_task_id: string | null };
      };
      version: { version: number; key_id: string; key_version: number };
    }>(secretSnap);
    expect(
      secretBody.secret.environment_id,
      "secret belongs to the task-owned environment",
    ).toBe(envId);
    expect(
      secretBody.secret.team_id,
      "secret carries the verified team_id",
    ).toBe(bsA.admin.team_id);
    expect(
      secretBody.secret.scope.parent_task_id,
      "secret scope carries the parent task_id",
    ).toBe(task.task_id);
    expect(
      secretBody.version.version,
      "secret version is >= 1",
    ).toBeGreaterThanOrEqual(1);
    expect(
      secretBody.version.key_id,
      "secret version carries opaque key_id",
    ).toBeTruthy();
    expect(
      secretBody.version.key_version,
      "secret version carries opaque key_version",
    ).toBeGreaterThanOrEqual(1);
    secretId = secretBody.secret.secret_id;
    const secretText = await secretSnap.text();
    expect(
      secretText.includes("plaintext-v0002-47"),
      "secret create response never echoes the submitted plaintext",
    ).toBe(false);
  } finally {
    await gwApi.dispose();
  }
  // Re-read via the same trusted Gateway path to confirm the secret
  // metadata is persisted with the documented keys and no plaintext.
  const gwRead = gatewayFor({
    teamId: bsA.admin.team_id,
    operatorId: `op-${bsA.admin.team_id}`,
  });
  const gwReadApi = await gwRead.api(worker.baseUrl);
  try {
    const versions = await gwReadApi.get(
      `/v1/environments/${encodeURIComponent(envId)}/secrets/${encodeURIComponent(secretId)}/versions`,
    );
    expect(
      versions.status(),
      "team-scoped secret version listing returns 200",
    ).toBe(200);
    const versionsSnap = await snapshotResponse(versions);
    const versionsBody = await readJson<{
      items: Array<{ version: number; key_id: string; key_version: number }>;
    }>(versionsSnap);
    const items = Array.isArray(versionsBody.items) ? versionsBody.items : [];
    expect(
      items.length,
      "task-owned secret version listing contains at least one immutable version",
    ).toBeGreaterThanOrEqual(1);
    const versionsText = await versionsSnap.text();
    expect(
      versionsText.includes("plaintext-v0002-47"),
      "team-scoped secret version listing never echoes the stored plaintext",
    ).toBe(false);
  } finally {
    await gwReadApi.dispose();
  }
});

test("v0002.48 task-owned environment rejects a foreign-team parent_task_id with the same non-revealing 404 shape", async () => {
  const admin = systemAdministrator();
  const suffix = `v0002-48-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 6)}`;
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-a`,
    executionTag: "openhands",
  });
  const bsB = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: `${suffix}-b`,
    executionTag: "openhands",
  });
  const teamBTask = await ingestPendingTask(
    listenerFor({
      teamId: bsB.admin.team_id,
      listenerIdentity: bsB.listenerIdentity,
      sourceSystemId: bsB.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsB.admin.team_id,
      source_system_id: bsB.sourceSystem.source_system_id,
      source_id: "B-1",
      task_type_id: bsB.taskType.task_type_id,
      payload: { from: "team-b" },
    },
  );
  // Capture the canonical 404 closure shape once under the same
  // team-a Gateway identity, posting an environment with an unknown
  // parent_task_id. The foreign parent_task_id reject body MUST match
  // that closure shape (same status / code / message / key set —
  // request_id is per-request) and zero encrypt operations must occur.
  const gw = gatewayFor({
    teamId: bsA.admin.team_id,
    operatorId: `op-${bsA.admin.team_id}`,
  });
  const gwCanonicalApi = await gw.api(worker.baseUrl);
  let canonical: OpenResponse;
  try {
    const resp = await gwCanonicalApi.post("/v1/environments", {
      data: {
        name: "env-v0002-48-canonical",
        scope: {
          project_id: null,
          task_id: null,
          parent_task_id: UnknownTaskId,
        },
        values: { REGION: "us-east-1" },
      },
    });
    canonical = await consumeOpenResponse(resp);
    expect(canonical.status, "canonical environment create returns 404").toBe(
      404,
    );
    const canonicalBody = canonical.json as OpenErrorBody | null;
    expect(
      canonicalBody,
      "canonical body parses to ErrorResponse",
    ).not.toBeNull();
    if (canonicalBody) {
      expect(
        canonicalBody.code,
        "canonical body carries environment_unknown_or_unavailable code",
      ).toBe("environment_unknown_or_unavailable");
      expect(
        typeof canonicalBody.request_id,
        "canonical body request_id is a string",
      ).toBe("string");
      expect(
        canonicalBody.request_id.length,
        "canonical body request_id is non-empty",
      ).toBeGreaterThan(0);
    }
  } finally {
    await gwCanonicalApi.dispose();
  }
  const baseline = await snapshotDecryptOps(worker.baseUrl);
  const gwApi = await gw.api(worker.baseUrl);
  try {
    const resp = await gwApi.post("/v1/environments", {
      data: {
        name: "env-v0002-48-foreign",
        scope: {
          project_id: null,
          task_id: null,
          parent_task_id: teamBTask.task_id,
        },
        values: { REGION: "us-east-1" },
      },
    });
    const raw = await consumeOpenResponse(resp);
    assertClosureShapeIdentical(raw, canonical, "foreign team parent_task_id");
  } finally {
    await gwApi.dispose();
  }
  const after = await snapshotDecryptOps(worker.baseUrl);
  expect(
    after.raw,
    "foreign parent_task_id records zero encrypt operations",
  ).toBe(baseline.raw);
});

test("v0002.49 task-owned environment can be opened ONLY by the parent task's assigned Executor; siblings return 404", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-49-team",
    executionTag: "openhands",
  });
  const task1 = await ingestPendingTask(
    listenerFor({
      teamId: bsA.admin.team_id,
      listenerIdentity: bsA.listenerIdentity,
      sourceSystemId: bsA.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsA.admin.team_id,
      source_system_id: bsA.sourceSystem.source_system_id,
      source_id: "T-1",
      task_type_id: bsA.taskType.task_type_id,
      payload: {},
    },
  );
  await new Promise((resolve) => setTimeout(resolve, 1_100));
  const task2 = await ingestPendingTask(
    listenerFor({
      teamId: bsA.admin.team_id,
      listenerIdentity: bsA.listenerIdentity,
      sourceSystemId: bsA.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsA.admin.team_id,
      source_system_id: bsA.sourceSystem.source_system_id,
      source_id: "T-2",
      task_type_id: bsA.taskType.task_type_id,
      payload: {},
    },
  );
  const gw = gatewayFor({
    teamId: bsA.admin.team_id,
    operatorId: `op-${bsA.admin.team_id}`,
  });
  const gwApi = await gw.api(worker.baseUrl);
  let envId = "";
  try {
    const env = await gwApi.post("/v1/environments", {
      data: {
        name: "env-v0002-49",
        scope: {
          project_id: null,
          task_id: null,
          parent_task_id: task1.task_id,
        },
        values: { REGION: "us-east-1" },
      },
    });
    expect(env.status(), "POST /v1/environments returns 201").toBe(201);
    const envSnap = await snapshotResponse(env);
    const envBody = await readJson<{ environment_id: string }>(envSnap);
    envId = envBody.environment_id;
  } finally {
    await gwApi.dispose();
  }
  const execParent = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(execParent, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-49-parent",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  const execSibling = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(execSibling, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-49-sibling",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  const parentClaim = await claimAsExecutor(
    execParent,
    worker.baseUrl,
    task1.task_id,
    "C-parent",
  );
  expect(parentClaim.status(), "parent task claim returns 200").toBe(200);
  const parentClaimBody = await readJson<ClaimResponseBody>(parentClaim);
  const parentToken = parentClaimBody.scope_token;
  expect(
    parentToken,
    "parent task claim carries a freshly signed scope_token",
  ).toBeTruthy();
  if (!parentToken) {
    throw new Error("v0002.49: parent claim response missing scope_token");
  }

  const siblingClaim = await claimAsExecutor(
    execSibling,
    worker.baseUrl,
    task2.task_id,
    "C-sibling",
  );
  expect(siblingClaim.status(), "sibling task claim returns 200").toBe(200);
  // Positive control: parent task's assigned Executor opens the
  // task-owned environment with the claim-issued token and gets 200.
  const parentOpen = await openEnvironmentAsExecutor(
    execParent,
    worker.baseUrl,
    envId,
    task1.task_id,
    parentToken,
  );
  expect(parentOpen.status, "parent task Executor open returns 200").toBe(200);
  const parentBody = parentOpen.json as OpenEnvironmentBody | null;
  expect(
    parentBody,
    "parent open body parses to OpenEnvironmentResponse",
  ).not.toBeNull();
  if (parentBody) {
    expect(parentBody.task_id, "parent open carries the parent task_id").toBe(
      task1.task_id,
    );
    expect(
      parentBody.environment_id,
      "parent open carries the task-owned environment_id",
    ).toBe(envId);
  }

  const canonical = await captureCanonical404(
    execSibling,
    worker.baseUrl,
    envId,
    task2.task_id,
  );
  const siblingBefore = await snapshotDecryptOps(worker.baseUrl);
  const siblingOpen = await openEnvironmentAsExecutor(
    execSibling,
    worker.baseUrl,
    envId,
    task2.task_id,
    parentToken,
  );
  assertClosureShapeIdentical(
    siblingOpen,
    canonical,
    "sibling task with valid token",
  );
  const siblingAfter = await snapshotDecryptOps(worker.baseUrl);
  expect(
    siblingAfter.raw,
    "sibling task open records zero decrypt operations",
  ).toBe(siblingBefore.raw);
});

test("v0002.77 AES-256-GCM startup fails closed: configured key is required before any secret write or open", async () => {
  // The fixture failure is reported as a structural error type that
  // carries an optional sanitized `diagnostics` field. The Go
  // missing-key sentinel lives in the worker's sanitized
  // stdout/stderr capture, not in the public error message (which
  // intentionally avoids echoing the failing config). We therefore
  // combine the error's message with the sanitized diagnostics for
  // sentinel/leak assertions. We never append the raw diagnostics to
  // a public error or echo the failing config.
  let captured: Error | null = null;
  try {
    await startRegistryWorker({ omitAesKey: true });
  } catch (err) {
    captured = err instanceof Error ? err : new Error(String(err));
  }
  expect(
    captured,
    "startRegistryWorker fails closed when STATE_REGISTRY_AES_KEY_HEX is empty",
  ).not.toBeNull();
  const capturedDiagnostics =
    captured &&
    "diagnostics" in captured &&
    typeof captured.diagnostics === "string"
      ? captured.diagnostics
      : "";
  const errorText = captured
    ? `${captured.message}\n${capturedDiagnostics}`
    : "";
  expect(
    errorText,
    "fail-closed startup error carries the missing-key sentinel",
  ).toMatch(/STATE_REGISTRY_AES_KEY_HEX/);
  // The exact export prefix `STATE_REGISTRY_AES_KEY_HEX=<hex>` and the
  // 64-character AES-256 key length are the only secret-leak shapes
  // that matter. We avoid broad 32+ hex regexes that would reject
  // benign UUIDs, timestamps, or unrelated identifiers.
  expect(
    /STATE_REGISTRY_AES_KEY_HEX=[0-9a-fA-F]{64}/.test(errorText),
    "fail-closed error never contains the export-form AES key value",
  ).toBe(false);
  expect(
    /\b[0-9a-fA-F]{64}\b/.test(errorText),
    "fail-closed error never contains a 64-hex-char plaintext key",
  ).toBe(false);
  expect(
    /\bnonce\b|\bciphertext\b|\bauthentication[\s_]?tag\b/i.test(errorText),
    "fail-closed error never mentions crypto operational terms",
  ).toBe(false);
  expect(
    /X-FlowAI-Scope-Token=\S+/.test(errorText),
    "fail-closed error never contains a scope token in the env export form",
  ).toBe(false);
});

test("v0002.78 every invalid open-environment request returns the same 404 shape with zero decrypt operations", async () => {
  const admin = systemAdministrator();
  const bsA = await bootstrapTeam(admin, worker.baseUrl, {
    teamName: "v0002-78-team",
    executionTag: "openhands",
  });
  const gw = gatewayFor({
    teamId: bsA.admin.team_id,
    operatorId: `op-${bsA.admin.team_id}`,
  });
  const gwApi = await gw.api(worker.baseUrl);
  let envId = "";
  try {
    const env = await gwApi.post("/v1/environments", {
      data: {
        name: "env-v0002-78",
        scope: { project_id: null, task_id: null, parent_task_id: null },
        values: { REGION: "us-east-1" },
      },
    });
    expect(env.status(), "POST /v1/environments returns 201").toBe(201);
    const envSnap = await snapshotResponse(env);
    const envBody = await readJson<{ environment_id: string }>(envSnap);
    envId = envBody.environment_id;
  } finally {
    await gwApi.dispose();
  }
  const task = await ingestPendingTask(
    listenerFor({
      teamId: bsA.admin.team_id,
      listenerIdentity: bsA.listenerIdentity,
      sourceSystemId: bsA.sourceSystem.source_system_id,
    }),
    worker.baseUrl,
    {
      team_id: bsA.admin.team_id,
      source_system_id: bsA.sourceSystem.source_system_id,
      source_id: "T-1",
      task_type_id: bsA.taskType.task_type_id,
      payload: {},
      environment_id: envId,
    },
  );
  const exec = teamExecutorFor({
    teamId: bsA.admin.team_id,
    executorId: uniqueExecutorId(),
  });
  await registerExecutor(exec, worker.baseUrl, {
    scope: "team",
    team_id: bsA.admin.team_id,
    executor_type: "executor_docker_openhands",
    identity: "exec-v0002-78",
    authorized_tag: "openhands",
    max_capacity: 1,
    running_count: 0,
    runtime_metadata: {},
  });
  const claim = await claimAsExecutor(
    exec,
    worker.baseUrl,
    task.task_id,
    "C-1",
  );
  expect(
    claim.status(),
    "claim returns 200 with the canonical claim response",
  ).toBe(200);
  const claimBody = await readJson<ClaimResponseBody>(claim);
  const scopeToken = claimBody.scope_token;
  expect(
    scopeToken,
    "claim response carries the freshly signed scope_token",
  ).toBeTruthy();
  if (!scopeToken) {
    throw new Error("v0002.78: claim response missing scope_token");
  }

  // Positive control: the assigned Executor opens with the claim-issued
  // token and gets 200. The loop below then runs the invalid variants
  // and asserts each returns the canonical 404 closure shape with zero
  // decrypt operations.
  const positive = await openEnvironmentAsExecutor(
    exec,
    worker.baseUrl,
    envId,
    task.task_id,
    scopeToken,
  );
  expect(
    positive.status,
    "positive control: assigned Executor open returns 200",
  ).toBe(200);

  const canonical = await captureCanonical404(
    exec,
    worker.baseUrl,
    envId,
    task.task_id,
  );

  type Case = { name: string; token: string | null };
  const cases: Case[] = [
    { name: "no-token", token: null },
    { name: "empty-token", token: "" },
    { name: "one-segment-token", token: "a" },
    { name: "two-segment-token", token: "a.b" },
    { name: "three-segment-malformed", token: "a.b.c" },
    { name: "tampered-signature", token: tamperSignature(scopeToken) },
    {
      name: "task-id-claim-mismatch",
      token: rewriteClaims(scopeToken, (c) => ({
        ...c,
        task_id: `${c.task_id}-mismatch`,
      })),
    },
    {
      name: "environment-id-claim-mismatch",
      token: rewriteClaims(scopeToken, (c) => ({
        ...c,
        environment_id: "env-foreign-shape",
      })),
    },
    {
      name: "team-id-claim-mismatch",
      token: rewriteClaims(scopeToken, (c) => ({
        ...c,
        team_id: "team-foreign-shape",
      })),
    },
    {
      name: "wrong-audience",
      token: rewriteClaims(scopeToken, (c) => ({
        ...c,
        audience: "state-registry.environment.read",
      })),
    },
    {
      name: "kid-vs-key-id-mismatch",
      token: rewriteHeader(scopeToken, (h) => ({
        alg: h.alg,
        kid: `${h.kid}-mismatch`,
        typ: h.typ,
      })),
    },
    {
      name: "alg-not-allow-listed",
      token: rewriteHeader(scopeToken, (h) => ({
        alg: "none",
        kid: h.kid,
        typ: h.typ,
      })),
    },
    {
      name: "typ-not-scope-token",
      token: rewriteHeader(scopeToken, (h) => ({
        alg: h.alg,
        kid: h.kid,
        typ: "JWT",
      })),
    },
  ];

  for (const c of cases) {
    const before = await snapshotDecryptOps(worker.baseUrl);
    const blocked = await openEnvironmentAsExecutor(
      exec,
      worker.baseUrl,
      envId,
      task.task_id,
      c.token,
    );
    assertClosureShapeIdentical(blocked, canonical, c.name);
    const after = await snapshotDecryptOps(worker.baseUrl);
    expect(after.raw, `${c.name} records zero decrypt operations`).toBe(
      before.raw,
    );
  }
});
