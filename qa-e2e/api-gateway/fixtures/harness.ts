import {
  createHash,
  createSign,
  generateKeyPairSync,
  randomBytes,
  randomUUID,
  type KeyObject,
} from "node:crypto";
import {
  createServer,
  type IncomingMessage,
  type ServerResponse,
} from "node:http";
import type { AddressInfo } from "node:net";
import { WebSocketServer, type WebSocket } from "ws";
import { Pool, type QueryResultRow } from "pg";

export type OIDCEndpoint =
  "discovery" | "authorize" | "token" | "userinfo" | "jwks";
export type OIDCFailure =
  { kind: "status"; status: number } | { kind: "timeout"; delayMs: number };

export interface OIDCClaims {
  issuer: string;
  subject: string;
  audience: string;
  teams: string[] | Array<Record<string, string>>;
  roles?: string[];
}

export type FixtureJWK = JsonWebKey & { kid: string; use: string; alg: string };

export interface ObservedRequest {
  method: string;
  url: string;
  headers: Record<string, string | string[] | undefined>;
  body: string;
}

export class ProviderNeutralOIDCFixture {
  readonly requests: ObservedRequest[] = [];
  readonly failures = new Map<OIDCEndpoint, OIDCFailure>();
  private server = createServer(
    (request, response) => void this.handle(request, response),
  );
  private claims: OIDCClaims;
  private readonly signingKey: KeyObject;
  readonly publicJWK: FixtureJWK;
  issuer = "";
  private nonce = "";

  constructor(claims?: Partial<OIDCClaims>) {
    const keyPair = generateKeyPairSync("rsa", { modulusLength: 2048 });
    this.signingKey = keyPair.privateKey;
    this.publicJWK = {
      ...keyPair.publicKey.export({ format: "jwk" }),
      kid: "fixture-oidc-rs256",
      use: "sig",
      alg: "RS256",
    };
    this.claims = {
      issuer: "",
      subject: claims?.subject ?? "operator-1",
      audience: claims?.audience ?? "flowai-api-gateway",
      teams: claims?.teams ?? ["oidc-team-alpha"],
      roles: claims?.roles ?? [],
    };
  }

  async start(): Promise<void> {
    await new Promise<void>((resolve, reject) => {
      this.server.once("error", reject);
      this.server.listen(0, "127.0.0.1", resolve);
    });
    const address = this.server.address() as AddressInfo;
    this.issuer = `http://127.0.0.1:${address.port}`;
    this.claims.issuer = this.issuer;
  }

  async stop(): Promise<void> {
    await new Promise<void>((resolve, reject) =>
      this.server.close((error) => (error ? reject(error) : resolve())),
    );
  }

  setFailure(endpoint: OIDCEndpoint, failure?: OIDCFailure): void {
    if (failure) this.failures.set(endpoint, failure);
    else this.failures.delete(endpoint);
  }

  setTeams(teams: OIDCClaims["teams"]): void {
    this.claims.teams = teams;
  }

  private async handle(
    request: IncomingMessage,
    response: ServerResponse,
  ): Promise<void> {
    const body = await readBody(request);
    this.requests.push({
      method: request.method ?? "",
      url: request.url ?? "",
      headers: request.headers,
      body,
    });
    const endpoint = endpointFor(request.url ?? "");
    const failure = this.failures.get(endpoint);
    if (failure?.kind === "timeout")
      await new Promise((resolve) => setTimeout(resolve, failure.delayMs));
    if (failure?.kind === "status")
      return json(response, failure.status, { error: "fixture_failure" });

    switch (endpoint) {
      case "discovery":
        return json(response, 200, {
          issuer: this.issuer,
          authorization_endpoint: `${this.issuer}/authorize`,
          token_endpoint: `${this.issuer}/token`,
          userinfo_endpoint: `${this.issuer}/userinfo`,
          jwks_uri: `${this.issuer}/jwks`,
        });
      case "authorize":
        {
          const query = new URL(request.url ?? "/", this.issuer).searchParams;
          this.nonce = query.get("nonce") ?? "";
          const callback = new URL(
            query.get("redirect_uri") ?? "",
            this.issuer,
          );
          callback.search = new URLSearchParams({
            code: "fixture-code",
            state: query.get("state") ?? "",
          }).toString();
          response.writeHead(302, { location: callback.toString() });
        }
        response.end();
        return;
      case "token": {
        const now = Math.floor(Date.now() / 1000);
        const payload = {
          iss: this.issuer,
          sub: this.claims.subject,
          aud: this.claims.audience,
          iat: now,
          exp: now + 300,
          nonce: this.nonce,
          teams: this.claims.teams,
          roles: this.claims.roles,
        };
        return json(response, 200, {
          access_token: "fixture-access",
          refresh_token: "fixture-refresh",
          token_type: "Bearer",
          expires_in: 300,
          id_token: signFixtureJWT(
            payload,
            this.signingKey,
            "fixture-oidc-rs256",
          ),
        });
      }
      case "userinfo":
        return json(response, 200, {
          sub: this.claims.subject,
          teams: this.claims.teams,
        });
      case "jwks":
        return json(response, 200, { keys: [this.publicJWK] });
    }
  }
}

export class StateRegistryRequestSpy {
  readonly requests: ObservedRequest[] = [];
  record(request: ObservedRequest): void {
    this.requests.push(request);
  }
  last(): ObservedRequest {
    const value = this.requests.at(-1);
    if (!value) throw new Error("no State Registry request observed");
    return value;
  }
}

export class WebSocketEventPublisher {
  private server = new WebSocketServer({ port: 0 });
  readonly clients = new Set<WebSocket>();
  constructor() {
    this.server.on("connection", (client) => {
      this.clients.add(client);
      client.on("close", () => this.clients.delete(client));
    });
  }
  address(): string {
    const address = this.server.address() as AddressInfo;
    return `ws://127.0.0.1:${address.port}`;
  }
  publish(value: unknown): void {
    const frame = JSON.stringify(value);
    for (const client of this.clients) client.send(frame);
  }
  async close(): Promise<void> {
    for (const client of this.clients) client.terminate();
    await new Promise<void>((resolve, reject) =>
      this.server.close((error) => (error ? reject(error) : resolve())),
    );
  }
}

export const postgresIsolation = {
  gateway: {
    schema: "api_gateway",
    runtimeRole: "api_gateway_runtime",
    migrationRole: "api_gateway_migrator",
    readOnlyRole: "api_gateway_e2e_reader",
  },
  stateRegistry: {
    schema: "state_registry",
    runtimeRole: "state_registry_runtime",
    migrationRole: "state_registry_migrator",
    readOnlyRole: "state_registry_e2e_reader",
  },
} as const;

export class ReadOnlyDatabaseAssertions {
  private readonly pool: Pool;
  constructor(connectionString: string) {
    this.pool = new Pool({ connectionString, max: 2 });
  }
  async exactRows<T extends QueryResultRow>(
    query: string,
    values: readonly unknown[] = [],
  ): Promise<T[]> {
    if (!/^\s*(SELECT|WITH)\b/i.test(query))
      throw new Error("read-only assertion client accepts only SELECT or WITH");
    const result = await this.pool.query<T>(query, [...values]);
    return result.rows;
  }
  async close(): Promise<void> {
    await this.pool.end();
  }
}

export interface SessionFixture {
  sessionID: string;
  operatorID: string;
  issuer: string;
  subject: string;
  observedAt: string;
}

export function sessionFixture(
  overrides: Partial<SessionFixture> = {},
): SessionFixture {
  return {
    sessionID: overrides.sessionID ?? opaqueSessionID(),
    operatorID: overrides.operatorID ?? "operator-1",
    issuer: overrides.issuer ?? "https://issuer.invalid",
    subject: overrides.subject ?? "subject-1",
    observedAt: overrides.observedAt ?? new Date().toISOString(),
  };
}

export function adminJWTFixture(
  issuer: string,
  audience: string,
  roles: string[],
): { token: string; jwk: FixtureJWK } {
  const pair = generateKeyPairSync("rsa", { modulusLength: 2048 });
  const now = Math.floor(Date.now() / 1000);
  const token = signFixtureJWT(
    {
      iss: issuer,
      sub: "admin-1",
      aud: audience,
      iat: now,
      exp: now + 300,
      roles,
    },
    pair.privateKey,
    "fixture-admin-rs256",
  );
  return {
    token,
    jwk: {
      ...pair.publicKey.export({ format: "jwk" }),
      kid: "fixture-admin-rs256",
      use: "sig",
      alg: "RS256",
    },
  };
}

export class AdminJWTProviderFixture {
  readonly requests: ObservedRequest[] = [];
  private readonly keys = new Map<
    string,
    { privateKey: KeyObject; jwk: FixtureJWK }
  >();
  private publishedKids: string[] = [];
  private failure?: OIDCFailure;
  private server = createServer(
    (request, response) => void this.handle(request, response),
  );
  issuer = "";

  async start(): Promise<void> {
    await new Promise<void>((resolve, reject) => {
      this.server.once("error", reject);
      this.server.listen(0, "127.0.0.1", resolve);
    });
    this.issuer = `http://127.0.0.1:${(this.server.address() as AddressInfo).port}`;
    this.addKey("admin-a");
    this.publishedKids = ["admin-a"];
  }

  addKey(kid: string): void {
    const pair = generateKeyPairSync("rsa", { modulusLength: 2048 });
    this.keys.set(kid, {
      privateKey: pair.privateKey,
      jwk: {
        ...pair.publicKey.export({ format: "jwk" }),
        kid,
        use: "sig",
        alg: "RS256",
      },
    });
  }

  publish(...kids: string[]): void {
    this.publishedKids = kids;
  }
  setFailure(failure?: OIDCFailure): void {
    this.failure = failure;
  }

  token(
    overrides: {
      kid?: string;
      issuer?: string;
      audience?: string;
      subject?: string;
      iat?: number | null;
      exp?: number;
      roles?: unknown;
      alg?: string;
      signWithKid?: string;
    } = {},
  ): string {
    const now = Math.floor(Date.now() / 1000);
    const kid = overrides.kid ?? "admin-a";
    const signing = this.keys.get(overrides.signWithKid ?? kid);
    if (!signing)
      throw new Error(`unknown signing key ${overrides.signWithKid ?? kid}`);
    const payload: Record<string, unknown> = {
      iss: overrides.issuer ?? this.issuer,
      sub: overrides.subject ?? "admin-1",
      aud: overrides.audience ?? "flowai-api-gateway-admin",
      exp: overrides.exp ?? now + 300,
      realm_access: { roles: overrides.roles ?? ["flowai-system-admin"] },
    };
    if (overrides.iat !== null) payload.iat = overrides.iat ?? now;
    return signFixtureJWT(
      payload,
      signing.privateKey,
      kid,
      overrides.alg ?? "RS256",
    );
  }

  async stop(): Promise<void> {
    await new Promise<void>((resolve, reject) =>
      this.server.close((error) => (error ? reject(error) : resolve())),
    );
  }

  private async handle(
    request: IncomingMessage,
    response: ServerResponse,
  ): Promise<void> {
    this.requests.push({
      method: request.method ?? "",
      url: request.url ?? "",
      headers: request.headers,
      body: await readBody(request),
    });
    if (this.failure?.kind === "timeout")
      await new Promise((resolve) =>
        setTimeout(resolve, this.failure!.delayMs),
      );
    if (this.failure?.kind === "status")
      return json(response, this.failure.status, { error: "fixture_failure" });
    return json(response, 200, {
      keys: this.publishedKids
        .map((kid) => this.keys.get(kid)?.jwk)
        .filter(Boolean),
    });
  }
}

export function pkcePair(): { verifier: string; challenge: string } {
  const verifier = randomBytes(32).toString("base64url");
  const challenge = createHash("sha256").update(verifier).digest("base64url");
  return { verifier, challenge };
}

export function opaqueSessionID(): string {
  return randomUUID();
}

function endpointFor(url: string): OIDCEndpoint {
  if (url.startsWith("/.well-known/openid-configuration")) return "discovery";
  const path = new URL(url, "http://fixture.invalid").pathname.slice(
    1,
  ) as OIDCEndpoint;
  if (["authorize", "token", "userinfo", "jwks"].includes(path)) return path;
  throw new Error(`unexpected OIDC fixture path: ${url}`);
}

function signFixtureJWT(
  payload: object,
  key: KeyObject,
  kid: string,
  alg = "RS256",
): string {
  const header = Buffer.from(JSON.stringify({ alg, typ: "JWT", kid })).toString(
    "base64url",
  );
  const body = Buffer.from(JSON.stringify(payload)).toString("base64url");
  const input = `${header}.${body}`;
  const signer = createSign("RSA-SHA256");
  signer.update(input);
  signer.end();
  return `${input}.${signer.sign(key).toString("base64url")}`;
}

async function readBody(request: IncomingMessage): Promise<string> {
  const chunks: Buffer[] = [];
  for await (const chunk of request) chunks.push(Buffer.from(chunk));
  return Buffer.concat(chunks).toString("utf8");
}

function json(response: ServerResponse, status: number, value: unknown): void {
  response.writeHead(status, { "content-type": "application/json" });
  response.end(JSON.stringify(value));
}
