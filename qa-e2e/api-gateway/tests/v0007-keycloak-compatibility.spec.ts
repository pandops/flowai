import {
  createHash,
  createPublicKey,
  generateKeyPairSync,
  randomBytes,
  verify,
} from "node:crypto";
import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import { expect, test } from "@playwright/test";
import {
  keycloakImage,
  startKeycloakContainer,
  type KeycloakContainer,
} from "../fixtures/keycloak_container.js";
import { startGatewayContainer } from "../fixtures/gateway_container.js";
import { startPostgresContainer } from "../fixtures/postgres_container.js";
import { startStateRegistryContainer } from "../fixtures/state_registry_container.js";
import {
  AdminJWTProviderFixture,
  ProviderNeutralOIDCFixture,
  ReadOnlyDatabaseAssertions,
} from "../fixtures/harness.js";

let keycloak: KeycloakContainer;
const workingTokenKeys = generateKeyPairSync("rsa", { modulusLength: 2048 });
const workingTokenPrivateKeyBase64 = Buffer.from(
  workingTokenKeys.privateKey.export({ format: "pem", type: "pkcs8" }),
).toString("base64");
const workingTokenEnvironment = {
  API_GATEWAY_WORKING_TOKEN_PRIVATE_KEY_BASE64: workingTokenPrivateKeyBase64,
  API_GATEWAY_WORKING_TOKEN_KEY_ID: "flowai-working-e2e-1",
  API_GATEWAY_WORKING_TOKEN_ISSUER: "https://gateway.flowai.invalid",
  API_GATEWAY_WORKING_TOKEN_AUDIENCE: "flowai-state-registry",
  API_GATEWAY_WORKING_TOKEN_TTL: "5m",
};

test.beforeAll(async () => {
  keycloak = await startKeycloakContainer();
});
test.afterAll(async () => {
  await keycloak?.close();
});

test("v0007.45 Keycloak 26.6.3 completes provider-neutral OIDC code flow with PKCE", async ({
  page,
}) => {
  expect(keycloakImage).toBe("quay.io/keycloak/keycloak:26.6.3");
  expect(keycloak.imageID).toMatch(/^(sha256:)?[a-f0-9]{64}$/);
  expect(keycloak.discovery.issuer).toBe(keycloak.issuer);

  const callback = await startCallbackServer();
  try {
    await keycloak.allowRedirectURI(callback.redirectURI);
    const state = randomBytes(24).toString("base64url");
    const nonce = randomBytes(24).toString("base64url");
    const verifier = randomBytes(48).toString("base64url");
    const challenge = createHash("sha256").update(verifier).digest("base64url");
    const authorize = new URL(keycloak.discovery.authorization_endpoint);
    authorize.search = new URLSearchParams({
      client_id: "flowai-api-gateway-e2e",
      redirect_uri: callback.redirectURI,
      response_type: "code",
      scope: "openid profile",
      state,
      nonce,
      code_challenge: challenge,
      code_challenge_method: "S256",
    }).toString();

    await page.goto(authorize.toString());
    await page.locator("#username").fill("operator-admin");
    await page.locator("#password").fill("flowai-e2e-password");
    await page.locator("#kc-login").click();
    const callbackURL = await callback.received;
    expect(callbackURL.searchParams.get("state")).toBe(state);
    const code = callbackURL.searchParams.get("code");
    expect(code).toBeTruthy();

    const tokenResponse = await fetch(keycloak.discovery.token_endpoint, {
      method: "POST",
      headers: { "content-type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({
        grant_type: "authorization_code",
        client_id: "flowai-api-gateway-e2e",
        redirect_uri: callback.redirectURI,
        code: code!,
        code_verifier: verifier,
      }),
    });
    expect(tokenResponse.status).toBe(200);
    const tokens = (await tokenResponse.json()) as {
      access_token: string;
      id_token: string;
      token_type: string;
    };
    expect(tokens.token_type.toLowerCase()).toBe("bearer");

    const { header, claims, signingInput, signature } = decodeJWT(
      tokens.id_token,
    );
    expect(header.alg).toBe("RS256");
    expect(claims.iss).toBe(keycloak.issuer);
    expect(claims.aud).toBe("flowai-api-gateway-e2e");
    expect(claims.nonce).toBe(nonce);
    expect(claims.sub).toEqual(expect.any(String));
    expect(claims.teams).toEqual(["oidc-team-alpha", "oidc-team-beta"]);
    expect(claims.realm_access?.roles).toContain("flowai-system-admin");
    expect(Number(claims.exp)).toBeGreaterThan(Math.floor(Date.now() / 1000));

    const jwks = (await (await fetch(keycloak.discovery.jwks_uri)).json()) as {
      keys: Array<JsonWebKey & { kid: string }>;
    };
    const jwk = jwks.keys.find((candidate) => candidate.kid === header.kid);
    expect(jwk).toBeTruthy();
    expect(
      verify(
        "RSA-SHA256",
        Buffer.from(signingInput),
        createPublicKey({ key: jwk!, format: "jwk" }),
        signature,
      ),
    ).toBe(true);

    const userInfoResponse = await fetch(keycloak.discovery.userinfo_endpoint, {
      headers: { authorization: `Bearer ${tokens.access_token}` },
    });
    expect(userInfoResponse.status).toBe(200);
    const userInfo = (await userInfoResponse.json()) as {
      sub: string;
      teams: string[];
    };
    expect(userInfo.sub).toBe(claims.sub);
    expect(userInfo.teams).toEqual(["oidc-team-alpha", "oidc-team-beta"]);
  } finally {
    await callback.close();
  }
});

test("v0007.45 production Gateway starts login against Keycloak 26.6.3 through discovery and PKCE", async () => {
  const postgres = await startPostgresContainer();
  const gateway = await startGatewayContainer(
    {
      ...workingTokenEnvironment,
      API_GATEWAY_OIDC_ISSUER: keycloak.issuer,
      API_GATEWAY_OIDC_CLIENT_ID: "flowai-api-gateway-e2e",
      API_GATEWAY_OIDC_REDIRECT_URI: "${GATEWAY_BASE_URL}/auth/v1/callback",
      API_GATEWAY_OIDC_TEAM_CLAIM_SOURCE: "id_token",
      API_GATEWAY_OIDC_TEAM_CLAIM_ADAPTER: "string_array",
      API_GATEWAY_OIDC_TEAM_CLAIM_POINTER: "/teams",
      API_GATEWAY_OIDC_TEAM_CLAIM_SOURCE: "id_token",
      API_GATEWAY_OIDC_ENDPOINT_TIMEOUT: "5s",
      API_GATEWAY_MEMBERSHIP_MAX_AGE: "0s",
      API_GATEWAY_POSTGRES_URL: postgres.connectionString,
      API_GATEWAY_SESSION_KEY_HEX: "7a".repeat(32),
      API_GATEWAY_WEB_UI_URL: "${GATEWAY_BASE_URL}/after-login",
    },
    { hostNetwork: true },
  );
  try {
    await keycloak.allowRedirectURI(`${gateway.baseUrl}/auth/v1/callback`);
    const response = await fetch(`${gateway.baseUrl}/auth/v1/login`, {
      redirect: "manual",
    });
    expect(response.status).toBe(302);
    const location = new URL(response.headers.get("location")!);
    expect(location.origin + location.pathname).toBe(
      keycloak.discovery.authorization_endpoint,
    );
    expect(location.searchParams.get("client_id")).toBe(
      "flowai-api-gateway-e2e",
    );
    expect(location.searchParams.get("response_type")).toBe("code");
    expect(location.searchParams.get("scope")?.split(" ")).toContain("openid");
    expect(location.searchParams.get("state")).toBeTruthy();
    expect(location.searchParams.get("nonce")).toBeTruthy();
    expect(location.searchParams.get("code_challenge_method")).toBe("S256");
    expect(location.searchParams.get("code_challenge")).toBeTruthy();
  } finally {
    await gateway.close();
    await postgres.close();
  }
});

test("v0007.23 production Gateway uses configured Keycloak UserInfo without ID-token fallback", async ({
  page,
}) => {
  const postgres = await startPostgresContainer();
  const gateway = await startGatewayContainer(
    {
      ...workingTokenEnvironment,
      API_GATEWAY_OIDC_ISSUER: keycloak.issuer,
      API_GATEWAY_OIDC_CLIENT_ID: "flowai-api-gateway-e2e",
      API_GATEWAY_OIDC_REDIRECT_URI: "${GATEWAY_BASE_URL}/auth/v1/callback",
      API_GATEWAY_OIDC_TEAM_CLAIM_ADAPTER: "string_array",
      API_GATEWAY_OIDC_TEAM_CLAIM_POINTER: "/teams",
      API_GATEWAY_OIDC_TEAM_CLAIM_SOURCE: "userinfo",
      API_GATEWAY_OIDC_ENDPOINT_TIMEOUT: "5s",
      API_GATEWAY_POSTGRES_URL: postgres.connectionString,
      API_GATEWAY_SESSION_KEY_HEX: "7c".repeat(32),
      API_GATEWAY_WEB_UI_URL: "${GATEWAY_BASE_URL}/after-login",
    },
    { hostNetwork: true },
  );
  try {
    await keycloak.allowRedirectURI(`${gateway.baseUrl}/auth/v1/callback`);
    await page.goto(`${gateway.baseUrl}/auth/v1/login`);
    await page.locator("#username").fill("operator-admin");
    await page.locator("#password").fill("flowai-e2e-password");
    const callbackResponsePromise = page.waitForResponse((response) =>
      response.url().startsWith(`${gateway.baseUrl}/auth/v1/callback?`),
    );
    await page.locator("#kc-login").click();
    const callbackResponse = await callbackResponsePromise;
    await page.waitForURL(`${gateway.baseUrl}/after-login`);
    const setCookie = (await callbackResponse.headersArray()).find(
      (header) => header.name.toLowerCase() === "set-cookie",
    )?.value;
    const sessionID = /flowai_session=([^;]+)/.exec(setCookie ?? "")?.[1];
    expect(sessionID).toBeTruthy();
    const teams = await page.request.get(`${gateway.baseUrl}/auth/v1/teams`, {
      headers: { cookie: `flowai_session=${sessionID}` },
    });
    expect(teams.status()).toBe(200);
    expect(await teams.json()).toEqual({ teams: [] });
    const database = new ReadOnlyDatabaseAssertions(postgres.connectionString);
    try {
      expect(
        await database.exactRows(
          `SELECT oidc_team_id FROM api_gateway.membership_observations ORDER BY oidc_team_id`,
        ),
      ).toEqual([
        { oidc_team_id: "oidc-team-alpha" },
        { oidc_team_id: "oidc-team-beta" },
      ]);
    } finally {
      await database.close();
    }
  } finally {
    await gateway.close();
    await postgres.close();
  }
});

test("v0007.45 production Gateway completes the Keycloak callback into an encrypted server session", async ({
  page,
}) => {
  const postgres = await startPostgresContainer();
  const canonicalTeams = await startCanonicalTeamServer();
  const gateway = await startGatewayContainer(
    {
      ...workingTokenEnvironment,
      API_GATEWAY_OIDC_ISSUER: keycloak.issuer,
      API_GATEWAY_OIDC_CLIENT_ID: "flowai-api-gateway-e2e",
      API_GATEWAY_OIDC_REDIRECT_URI: "${GATEWAY_BASE_URL}/auth/v1/callback",
      API_GATEWAY_OIDC_TEAM_CLAIM_ADAPTER: "string_array",
      API_GATEWAY_OIDC_TEAM_CLAIM_POINTER: "/teams",
      API_GATEWAY_OIDC_TEAM_CLAIM_SOURCE: "id_token",
      API_GATEWAY_OIDC_ENDPOINT_TIMEOUT: "5s",
      API_GATEWAY_MEMBERSHIP_MAX_AGE: "0s",
      API_GATEWAY_POSTGRES_URL: postgres.connectionString,
      API_GATEWAY_SESSION_KEY_HEX: "7b".repeat(32),
      API_GATEWAY_WEB_UI_URL: "${GATEWAY_BASE_URL}/after-login",
      API_GATEWAY_STATE_REGISTRY_URL: canonicalTeams.baseURL,
      API_GATEWAY_ADMIN_ISSUER: keycloak.issuer,
      API_GATEWAY_ADMIN_AUDIENCE: "flowai-api-gateway-admin",
      API_GATEWAY_ADMIN_JWKS_URL: keycloak.discovery.jwks_uri,
      API_GATEWAY_ADMIN_ROLE_CLAIM_POINTER: "/realm_access/roles",
      API_GATEWAY_ADMIN_JWKS_TIMEOUT: "5s",
      API_GATEWAY_ADMIN_TOKEN_MAX_AGE: "5m",
    },
    { hostNetwork: true },
  );
  try {
    await keycloak.allowRedirectURI(`${gateway.baseUrl}/auth/v1/callback`);
    const adminToken = await keycloakAdminToken();
    for (const [oidcTeamID, teamID] of [
      ["oidc-team-alpha", "team-alpha"],
      ["oidc-team-beta", "team-beta"],
    ]) {
      const mappingResponse = await fetch(
        `${gateway.baseUrl}/admin/v1/oidc-team-mappings`,
        {
          method: "POST",
          headers: {
            authorization: `Bearer ${adminToken}`,
            "content-type": "application/json",
          },
          body: JSON.stringify({
            issuer: keycloak.issuer,
            oidc_team_id: oidcTeamID,
            team_id: teamID,
          }),
        },
      );
      expect(mappingResponse.status).toBe(201);
    }
    const retry = await fetch(
      `${gateway.baseUrl}/admin/v1/oidc-team-mappings`,
      {
        method: "POST",
        headers: {
          authorization: `Bearer ${adminToken}`,
          "content-type": "application/json",
        },
        body: JSON.stringify({
          issuer: keycloak.issuer,
          oidc_team_id: "oidc-team-alpha",
          team_id: "team-alpha",
        }),
      },
    );
    expect(retry.status).toBe(200);
    await page.goto(`${gateway.baseUrl}/auth/v1/login`);
    await page.locator("#username").fill("operator-admin");
    await page.locator("#password").fill("flowai-e2e-password");
    const callbackResponsePromise = page.waitForResponse((response) =>
      response.url().startsWith(`${gateway.baseUrl}/auth/v1/callback?`),
    );
    await page.locator("#kc-login").click();
    const callbackResponse = await callbackResponsePromise;
    await page.waitForURL(`${gateway.baseUrl}/after-login`);
    const setCookie = (await callbackResponse.headersArray()).find(
      (header) => header.name.toLowerCase() === "set-cookie",
    )?.value;
    expect(setCookie).toContain("flowai_session=");
    expect(setCookie).toContain("HttpOnly");
    expect(setCookie).toContain("Secure");
    expect(setCookie).toContain("SameSite=Lax");
    const sessionID = /flowai_session=([^;]+)/.exec(setCookie ?? "")?.[1];
    expect(sessionID).toBeTruthy();
    const teamsResponse = await page.request.get(
      `${gateway.baseUrl}/auth/v1/teams`,
      { headers: { cookie: `flowai_session=${sessionID}` } },
    );
    expect(teamsResponse.status()).toBe(200);
    expect(await teamsResponse.json()).toEqual({
      teams: [
        { team_id: "team-alpha", team_name: "Alpha Team", archived_at: null },
        { team_id: "team-beta", team_name: "Beta Team", archived_at: null },
      ],
    });
    expect(canonicalTeams.requests).toEqual([
      "team-alpha",
      "team-beta",
      "team-alpha",
      "team-alpha",
      "team-beta",
    ]);
    const workingTokens: Array<Record<string, any>> = [];
    for (const teamID of ["team-alpha", "team-beta"]) {
      const response = await page.request.post(
        `${gateway.baseUrl}/auth/v1/token`,
        {
          headers: { cookie: `flowai_session=${sessionID}` },
          data: { team_id: teamID },
        },
      );
      expect(response.status()).toBe(200);
      const body = (await response.json()) as {
        access_token: string;
        token_type: string;
        expires_in: number;
      };
      expect(body.token_type).toBe("Bearer");
      expect(body.expires_in).toBe(300);
      const decoded = decodeJWT(body.access_token);
      expect(decoded.header).toMatchObject({
        alg: "RS256",
        kid: "flowai-working-e2e-1",
      });
      expect(decoded.claims).toMatchObject({
        iss: "https://gateway.flowai.invalid",
        aud: "flowai-state-registry",
        team_id: teamID,
      });
      expect(decoded.claims.operator_id).toBe(decoded.claims.sub);
      expect(
        verify(
          "RSA-SHA256",
          Buffer.from(decoded.signingInput),
          workingTokenKeys.publicKey,
          decoded.signature,
        ),
      ).toBe(true);
      workingTokens.push(decoded.claims);
    }
    expect(workingTokens[0].jti).not.toBe(workingTokens[1].jti);
    expect(workingTokens[0].team_id).not.toBe(workingTokens[1].team_id);
    const database = new ReadOnlyDatabaseAssertions(postgres.connectionString);
    try {
      expect(
        await database.exactRows(
          `SELECT issuer, oidc_team_id, team_id FROM api_gateway.oidc_team_mappings ORDER BY oidc_team_id`,
        ),
      ).toEqual([
        {
          issuer: keycloak.issuer,
          oidc_team_id: "oidc-team-alpha",
          team_id: "team-alpha",
        },
        {
          issuer: keycloak.issuer,
          oidc_team_id: "oidc-team-beta",
          team_id: "team-beta",
        },
      ]);
      expect(
        await database.exactRows(
          `SELECT oidc_team_id FROM api_gateway.membership_observations ORDER BY oidc_team_id`,
        ),
      ).toEqual([
        { oidc_team_id: "oidc-team-alpha" },
        { oidc_team_id: "oidc-team-beta" },
      ]);
      expect(
        await database.exactRows(
          `SELECT team_id FROM api_gateway.derived_operator_team_access ORDER BY team_id`,
        ),
      ).toEqual([{ team_id: "team-alpha" }, { team_id: "team-beta" }]);
      const sessionRows = await database.exactRows<{
        encrypted_size: number;
        plaintext_marker: boolean;
      }>(
        `SELECT octet_length(encrypted_provider_state)::int AS encrypted_size, position('access_token'::bytea IN encrypted_provider_state) > 0 AS plaintext_marker FROM api_gateway.oidc_sessions`,
      );
      expect(sessionRows).toHaveLength(1);
      expect(sessionRows[0].encrypted_size).toBeGreaterThan(32);
      expect(sessionRows[0].plaintext_marker).toBe(false);
      expect(
        await database.exactRows(
          `SELECT count(*)::int AS count, count(DISTINCT encode(jti_hash, 'hex'))::int AS distinct_count FROM api_gateway.working_token_records`,
        ),
      ).toEqual([{ count: 2, distinct_count: 2 }]);

      await keycloak.setOperatorGroups(["oidc-team-alpha"]);
      const revoked = await page.request.post(
        `${gateway.baseUrl}/auth/v1/token`,
        {
          headers: { cookie: `flowai_session=${sessionID}` },
          data: { team_id: "team-beta" },
        },
      );
      expect(revoked.status()).toBe(403);
      expect(await revoked.json()).toMatchObject({
        code: "team_not_accessible",
      });
      expect(
        await database.exactRows(
          `SELECT oidc_team_id FROM api_gateway.membership_observations ORDER BY oidc_team_id`,
        ),
      ).toEqual([{ oidc_team_id: "oidc-team-alpha" }]);
      expect(
        await database.exactRows(
          `SELECT team_id FROM api_gateway.derived_operator_team_access ORDER BY team_id`,
        ),
      ).toEqual([{ team_id: "team-alpha" }]);
      expect(
        await database.exactRows(
          `SELECT count(*)::int AS count FROM api_gateway.working_token_records`,
        ),
      ).toEqual([{ count: 2 }]);
    } finally {
      await database.close();
    }
  } finally {
    await keycloak
      .setOperatorGroups(["oidc-team-alpha", "oidc-team-beta"])
      .catch(() => undefined);
    await gateway.close();
    await postgres.close();
    await canonicalTeams.close();
  }
});

test("v0007.30 v0007.33 v0007.38 enforce live admin role age and JWKS outage semantics", async () => {
  const identity = await startKeycloakContainer();
  const postgres = await startPostgresContainer();
  const canonical = await startCanonicalTeamServer();
  let gateway: Awaited<ReturnType<typeof startGatewayContainer>> | undefined;
  let agedGateway:
    Awaited<ReturnType<typeof startGatewayContainer>> | undefined;
  const environment = {
    ...workingTokenEnvironment,
    API_GATEWAY_OIDC_ISSUER: identity.issuer,
    API_GATEWAY_OIDC_CLIENT_ID: "flowai-api-gateway-e2e",
    API_GATEWAY_OIDC_REDIRECT_URI: "${GATEWAY_BASE_URL}/auth/v1/callback",
    API_GATEWAY_OIDC_TEAM_CLAIM_ADAPTER: "string_array",
    API_GATEWAY_OIDC_TEAM_CLAIM_POINTER: "/teams",
    API_GATEWAY_OIDC_TEAM_CLAIM_SOURCE: "id_token",
    API_GATEWAY_OIDC_ENDPOINT_TIMEOUT: "5s",
    API_GATEWAY_POSTGRES_URL: postgres.connectionString,
    API_GATEWAY_SESSION_KEY_HEX: "5a".repeat(32),
    API_GATEWAY_WEB_UI_URL: "${GATEWAY_BASE_URL}/",
    API_GATEWAY_STATE_REGISTRY_URL: canonical.baseURL,
    API_GATEWAY_ADMIN_ISSUER: identity.issuer,
    API_GATEWAY_ADMIN_AUDIENCE: "flowai-api-gateway-admin",
    API_GATEWAY_ADMIN_JWKS_URL: identity.discovery.jwks_uri,
    API_GATEWAY_ADMIN_ROLE_CLAIM_POINTER: "/realm_access/roles",
    API_GATEWAY_ADMIN_JWKS_TIMEOUT: "500ms",
    API_GATEWAY_ADMIN_TOKEN_MAX_AGE: "5m",
  };
  try {
    gateway = await startGatewayContainer(environment, { hostNetwork: true });
    const post = async (token: string, oidcTeamID: string, teamID: string) =>
      fetch(`${gateway!.baseUrl}/admin/v1/oidc-team-mappings`, {
        method: "POST",
        headers: {
          authorization: `Bearer ${token}`,
          "content-type": "application/json",
        },
        body: JSON.stringify({
          issuer: identity.issuer,
          oidc_team_id: oidcTeamID,
          team_id: teamID,
        }),
      });
    const valid = await adminTokenFrom(identity.discovery.token_endpoint);
    const validResponse = await post(valid, "oidc-team-alpha", "team-alpha");
    expect(validResponse.status, await validResponse.text()).toBe(201);
    await identity.setOperatorAdminRole(false);
    const removed = await adminTokenFrom(identity.discovery.token_endpoint);
    const denied = await post(removed, "oidc-team-beta", "team-beta");
    expect(denied.status).toBe(403);
    expect(((await denied.json()) as { code: string }).code).toBe(
      "insufficient_admin_role",
    );
    await identity.setOperatorAdminRole(true);
    const restored = await adminTokenFrom(identity.discovery.token_endpoint);
    expect((await post(restored, "oidc-team-beta", "team-beta")).status).toBe(
      201,
    );

    agedGateway = await startGatewayContainer(
      {
        ...environment,
        API_GATEWAY_ADMIN_TOKEN_MAX_AGE: "1ms",
        API_GATEWAY_ADMIN_CLOCK_SKEW: "0s",
      },
      { hostNetwork: true },
    );
    await new Promise((resolve) => setTimeout(resolve, 1_200));
    const aged = await fetch(
      `${agedGateway.baseUrl}/admin/v1/oidc-team-mappings`,
      {
        method: "POST",
        headers: {
          authorization: `Bearer ${restored}`,
          "content-type": "application/json",
        },
        body: JSON.stringify({
          issuer: identity.issuer,
          oidc_team_id: "aged",
          team_id: "team-alpha",
        }),
      },
    );
    expect(aged.status).toBe(401);
    expect(((await aged.json()) as { code: string }).code).toBe(
      "invalid_admin_token",
    );
    await agedGateway.close();
    agedGateway = undefined;

    await identity.close();
    expect((await post(restored, "oidc-team-beta", "team-beta")).status).toBe(
      200,
    );
    const unknownKid = replaceJWTKid(restored, "unknown-after-outage");
    const unavailable = await post(unknownKid, "unknown", "team-alpha");
    expect(unavailable.status).toBe(502);
    expect(((await unavailable.json()) as { code: string }).code).toBe(
      "admin_identity_provider_unavailable",
    );
  } finally {
    await agedGateway?.close();
    await gateway?.close();
    await canonical.close();
    await postgres.close();
    await identity.close().catch(() => undefined);
  }
});

test("v0007.38 validates the complete Gateway administrator JWT runtime matrix", async () => {
  const admin = new AdminJWTProviderFixture();
  await admin.start();
  admin.addKey("admin-b");
  const oidc = new ProviderNeutralOIDCFixture();
  await oidc.start();
  const postgres = await startPostgresContainer();
  const canonical = await startCanonicalTeamServer();
  const database = new ReadOnlyDatabaseAssertions(postgres.connectionString);
  let gateway: Awaited<ReturnType<typeof startGatewayContainer>> | undefined;
  try {
    gateway = await startGatewayContainer(
      {
        ...workingTokenEnvironment,
        API_GATEWAY_OIDC_ISSUER: oidc.issuer,
        API_GATEWAY_OIDC_CLIENT_ID: "flowai-api-gateway-e2e",
        API_GATEWAY_OIDC_REDIRECT_URI: "${GATEWAY_BASE_URL}/auth/v1/callback",
        API_GATEWAY_OIDC_TEAM_CLAIM_ADAPTER: "string_array",
        API_GATEWAY_OIDC_TEAM_CLAIM_POINTER: "/teams",
        API_GATEWAY_OIDC_TEAM_CLAIM_SOURCE: "id_token",
        API_GATEWAY_OIDC_ENDPOINT_TIMEOUT: "5s",
        API_GATEWAY_POSTGRES_URL: postgres.connectionString,
        API_GATEWAY_SESSION_KEY_HEX: "7c".repeat(32),
        API_GATEWAY_WEB_UI_URL: "${GATEWAY_BASE_URL}/",
        API_GATEWAY_STATE_REGISTRY_URL: canonical.baseURL,
        API_GATEWAY_ADMIN_ISSUER: admin.issuer,
        API_GATEWAY_ADMIN_AUDIENCE: "flowai-api-gateway-admin",
        API_GATEWAY_ADMIN_JWKS_URL: `${admin.issuer}/jwks`,
        API_GATEWAY_ADMIN_ROLE_CLAIM_POINTER: "/realm_access/roles",
        API_GATEWAY_ADMIN_JWKS_TIMEOUT: "200ms",
        API_GATEWAY_ADMIN_TOKEN_MAX_AGE: "1m",
      },
      { hostNetwork: true },
    );
    const post = (token: string, external: string, teamID = "team-alpha") =>
      fetch(`${gateway!.baseUrl}/admin/v1/oidc-team-mappings`, {
        method: "POST",
        headers: {
          authorization: `Bearer ${token}`,
          "content-type": "application/json",
        },
        body: JSON.stringify({
          issuer: oidc.issuer,
          oidc_team_id: external,
          team_id: teamID,
        }),
      });
    const valid = admin.token();
    expect((await post(valid, "valid-a")).status).toBe(201);
    const now = Math.floor(Date.now() / 1000);
    const rejected = [
      admin.token({ issuer: `${admin.issuer}/wrong` }),
      admin.token({ audience: "wrong" }),
      admin.token({ alg: "RS512" }),
      admin.token({ signWithKid: "admin-b" }),
      admin.token({ kid: "unknown", signWithKid: "admin-a" }),
      admin.token({ iat: null }),
      admin.token({ iat: now - 120 }),
      admin.token({ iat: now - 120, exp: now - 120 }),
    ];
    const requestsBefore = canonical.requests.length;
    for (const [index, token] of rejected.entries()) {
      const response = await post(token, `rejected-${index}`);
      expect(response.status, await response.clone().text()).toBe(401);
      expect(((await response.json()) as { code: string }).code).toBe(
        "invalid_admin_token",
      );
    }
    for (const roles of [[], ["flowai-system-admin-extra"]]) {
      const response = await post(
        admin.token({ roles }),
        `role-${randomBytes(4).toString("hex")}`,
      );
      expect(response.status).toBe(403);
      expect(((await response.json()) as { code: string }).code).toBe(
        "insufficient_admin_role",
      );
    }
    const malformedRole = await post(
      admin.token({ roles: "flowai-system-admin" }),
      "malformed-role",
    );
    expect(malformedRole.status).toBe(401);
    expect(((await malformedRole.json()) as { code: string }).code).toBe(
      "invalid_admin_token",
    );
    expect(canonical.requests).toHaveLength(requestsBefore);
    admin.publish("admin-a", "admin-b");
    expect(
      (await post(admin.token({ kid: "admin-b" }), "valid-b", "team-beta"))
        .status,
    ).toBe(201);
    admin.setFailure({ kind: "status", status: 503 });
    expect((await post(valid, "valid-a")).status).toBe(200);
    const beforeOutage = canonical.requests.length;
    const unavailable = await post(
      admin.token({ kid: "missing-outage", signWithKid: "admin-a" }),
      "outage",
    );
    expect(unavailable.status).toBe(502);
    expect(((await unavailable.json()) as { code: string }).code).toBe(
      "admin_identity_provider_unavailable",
    );
    admin.setFailure({ kind: "timeout", delayMs: 600 });
    const timeout = await post(
      admin.token({ kid: "missing-timeout", signWithKid: "admin-a" }),
      "timeout",
    );
    expect(timeout.status).toBe(502);
    expect(((await timeout.json()) as { code: string }).code).toBe(
      "admin_identity_provider_unavailable",
    );
    expect(canonical.requests).toHaveLength(beforeOutage);
    expect(
      await database.exactRows(
        "SELECT oidc_team_id, team_id FROM api_gateway.oidc_team_mappings ORDER BY oidc_team_id",
      ),
    ).toEqual([
      { oidc_team_id: "valid-a", team_id: "team-alpha" },
      { oidc_team_id: "valid-b", team_id: "team-beta" },
    ]);
  } finally {
    await gateway?.close();
    await database.close();
    await postgres.close();
    await oidc.stop();
    await admin.stop();
  }
});

test("v0007.25 concurrent public mapping registration is atomic in both uniqueness dimensions", async ({
  page,
}) => {
  const identity = await startKeycloakContainer();
  const postgres = await startPostgresContainer();
  const registry = await startStateRegistryContainer(
    postgres.connectionString,
    {
      issuer: identity.issuer,
      audience: "flowai-api-gateway-admin",
      jwksURL: identity.discovery.jwks_uri,
    },
  );
  let gateway: Awaited<ReturnType<typeof startGatewayContainer>> | undefined;
  const database = new ReadOnlyDatabaseAssertions(postgres.connectionString);
  try {
    const token = await adminTokenFrom(identity.discovery.token_endpoint);
    const headers = {
      authorization: `Bearer ${token}`,
      "content-type": "application/json",
    };
    const teams: string[] = [];
    for (const [index, hex] of ["1", "2", "3", "4"].entries()) {
      const response = await fetch(`${registry.baseURL}/admin/teams`, {
        method: "POST",
        headers,
        body: JSON.stringify({
          team_name: `Mapping Race ${index + 1}`,
          default_image: `registry.example/mapping/race@sha256:${hex.repeat(64)}`,
        }),
      });
      expect(response.status).toBe(201);
      teams.push(((await response.json()) as { team_id: string }).team_id);
    }
    gateway = await startGatewayContainer(
      {
        ...workingTokenEnvironment,
        API_GATEWAY_OIDC_ISSUER: identity.issuer,
        API_GATEWAY_OIDC_CLIENT_ID: "flowai-api-gateway-e2e",
        API_GATEWAY_OIDC_REDIRECT_URI: "${GATEWAY_BASE_URL}/auth/v1/callback",
        API_GATEWAY_OIDC_TEAM_CLAIM_ADAPTER: "string_array",
        API_GATEWAY_OIDC_TEAM_CLAIM_POINTER: "/teams",
        API_GATEWAY_OIDC_TEAM_CLAIM_SOURCE: "id_token",
        API_GATEWAY_OIDC_ENDPOINT_TIMEOUT: "5s",
        API_GATEWAY_POSTGRES_URL: postgres.connectionString,
        API_GATEWAY_SESSION_KEY_HEX: "8a".repeat(32),
        API_GATEWAY_WEB_UI_URL: "${GATEWAY_BASE_URL}/",
        API_GATEWAY_STATE_REGISTRY_URL: registry.baseURL,
        API_GATEWAY_ADMIN_ISSUER: identity.issuer,
        API_GATEWAY_ADMIN_AUDIENCE: "flowai-api-gateway-admin",
        API_GATEWAY_ADMIN_JWKS_URL: identity.discovery.jwks_uri,
        API_GATEWAY_ADMIN_ROLE_CLAIM_POINTER: "/realm_access/roles",
        API_GATEWAY_ADMIN_JWKS_TIMEOUT: "5s",
        API_GATEWAY_ADMIN_TOKEN_MAX_AGE: "5m",
      },
      { hostNetwork: true },
    );
    const register = (external: string, teamID: string) =>
      fetch(`${gateway!.baseUrl}/admin/v1/oidc-team-mappings`, {
        method: "POST",
        headers,
        body: JSON.stringify({
          issuer: identity.issuer,
          oidc_team_id: external,
          team_id: teamID,
        }),
      });
    const identical = await Promise.all(
      Array.from({ length: 4 }, () => register("same-external", teams[0])),
    );
    expect(identical.map((response) => response.status).sort()).toEqual([
      200, 200, 200, 201,
    ]);
    const externalConflict = await Promise.all([
      register("oidc-team-alpha", teams[1]),
      register("oidc-team-alpha", teams[2]),
    ]);
    expect(externalConflict.map((response) => response.status).sort()).toEqual([
      201, 409,
    ]);
    const externalWinner = (await externalConflict
      .find((response) => response.status === 201)!
      .json()) as { team_id: string };
    const canonicalConflict = await Promise.all([
      register("oidc-team-beta", teams[3]),
      register("canonical-b", teams[3]),
    ]);
    expect(canonicalConflict.map((response) => response.status).sort()).toEqual(
      [201, 409],
    );
    const canonicalWinner = (await canonicalConflict
      .find((response) => response.status === 201)!
      .json()) as { oidc_team_id: string; team_id: string };
    expect(
      await database.exactRows(
        `SELECT oidc_team_id,team_id FROM api_gateway.oidc_team_mappings ORDER BY oidc_team_id`,
      ),
    ).toEqual(
      [
        { oidc_team_id: canonicalWinner.oidc_team_id, team_id: teams[3] },
        { oidc_team_id: "oidc-team-alpha", team_id: externalWinner.team_id },
        { oidc_team_id: "same-external", team_id: teams[0] },
      ].sort((a, b) => a.oidc_team_id.localeCompare(b.oidc_team_id)),
    );
    expect(
      await database.exactRows(
        `SELECT conname FROM pg_constraint WHERE conrelid='api_gateway.oidc_team_mappings'::regclass AND contype='u' ORDER BY conname`,
      ),
    ).toEqual([
      { conname: "oidc_team_mappings_external_unique" },
      { conname: "oidc_team_mappings_team_unique" },
    ]);
    expect(
      await database.exactRows(`SELECT count(*)::int AS count FROM teams`),
    ).toEqual([{ count: 4 }]);
    await identity.allowRedirectURI(`${gateway.baseUrl}/auth/v1/callback`);
    await page.goto(`${gateway.baseUrl}/auth/v1/login`);
    await page.locator("#username").fill("operator-admin");
    await page.locator("#password").fill("flowai-e2e-password");
    const callback = page.waitForResponse((response) =>
      response.url().startsWith(`${gateway!.baseUrl}/auth/v1/callback?`),
    );
    await page.locator("#kc-login").click();
    const callbackResponse = await callback;
    const cookie =
      (await callbackResponse.headersArray()).find(
        (header) => header.name.toLowerCase() === "set-cookie",
      )?.value ?? "";
    const session = /flowai_session=([^;]+)/.exec(cookie)?.[1];
    expect(session).toBeTruthy();
    const resolved = (await (
      await fetch(`${gateway.baseUrl}/auth/v1/teams`, {
        headers: { cookie: `flowai_session=${session}` },
      })
    ).json()) as { teams: Array<{ team_id: string }> };
    const expected = [
      externalWinner.team_id,
      ...(canonicalWinner.oidc_team_id === "oidc-team-beta" ? [teams[3]] : []),
    ].sort();
    expect(resolved.teams.map((team) => team.team_id).sort()).toEqual(expected);
  } finally {
    await database.close();
    await gateway?.close();
    await registry.close();
    await postgres.close();
    await identity.close();
  }
});

async function keycloakAdminToken(): Promise<string> {
  const response = await fetch(keycloak.discovery.token_endpoint, {
    method: "POST",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      grant_type: "password",
      client_id: "flowai-admin-e2e",
      username: "operator-admin",
      password: "flowai-e2e-password",
    }),
  });
  expect(response.status).toBe(200);
  const body = (await response.json()) as { access_token: string };
  return body.access_token;
}

async function adminTokenFrom(endpoint: string): Promise<string> {
  const response = await fetch(endpoint, {
    method: "POST",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      grant_type: "password",
      client_id: "flowai-admin-e2e",
      username: "operator-admin",
      password: "flowai-e2e-password",
    }),
  });
  expect(response.status).toBe(200);
  return ((await response.json()) as { access_token: string }).access_token;
}
function replaceJWTKid(token: string, kid: string): string {
  const parts = token.split(".");
  const header = JSON.parse(
    Buffer.from(parts[0], "base64url").toString("utf8"),
  ) as Record<string, unknown>;
  header.kid = kid;
  return `${Buffer.from(JSON.stringify(header)).toString("base64url")}.${parts[1]}.${parts[2]}`;
}

async function startCanonicalTeamServer(): Promise<{
  baseURL: string;
  requests: string[];
  close: () => Promise<void>;
}> {
  const requests: string[] = [];
  const teams: Record<string, string> = {
    "team-alpha": "Alpha Team",
    "team-beta": "Beta Team",
  };
  const server = createServer((request, response) => {
    const match = /^\/internal\/v1\/teams\/([^/]+)$/.exec(request.url ?? "");
    const teamID = match?.[1] ?? "";
    if (!teams[teamID]) {
      response.writeHead(404);
      response.end();
      return;
    }
    requests.push(teamID);
    response.writeHead(200, { "content-type": "application/json" });
    response.end(
      JSON.stringify({
        team_id: teamID,
        team_name: teams[teamID],
        archived_at: null,
      }),
    );
  });
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const port = (server.address() as AddressInfo).port;
  return {
    baseURL: `http://127.0.0.1:${port}`,
    requests,
    close: async () => {
      await new Promise<void>((resolve, reject) =>
        server.close((error) => (error ? reject(error) : resolve())),
      );
    },
  };
}

function decodeJWT(token: string): {
  header: Record<string, string>;
  claims: Record<string, any>;
  signingInput: string;
  signature: Buffer;
} {
  const parts = token.split(".");
  if (parts.length !== 3) throw new Error("ID Token is not a compact JWT");
  return {
    header: JSON.parse(Buffer.from(parts[0], "base64url").toString("utf8")),
    claims: JSON.parse(Buffer.from(parts[1], "base64url").toString("utf8")),
    signingInput: `${parts[0]}.${parts[1]}`,
    signature: Buffer.from(parts[2], "base64url"),
  };
}

async function startCallbackServer(): Promise<{
  redirectURI: string;
  received: Promise<URL>;
  close: () => Promise<void>;
}> {
  let resolveURL!: (url: URL) => void;
  const received = new Promise<URL>((resolve) => {
    resolveURL = resolve;
  });
  const server = createServer((request, response) => {
    const address = server.address() as AddressInfo;
    resolveURL(new URL(request.url ?? "/", `http://127.0.0.1:${address.port}`));
    response.writeHead(200, { "content-type": "text/html; charset=utf-8" });
    response.end("<!doctype html><title>OIDC callback received</title>");
  });
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address() as AddressInfo;
  return {
    redirectURI: `http://127.0.0.1:${address.port}/callback`,
    received,
    close: async () => {
      await new Promise<void>((resolve, reject) =>
        server.close((error) => (error ? reject(error) : resolve())),
      );
    },
  };
}
