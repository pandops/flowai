import { generateKeyPairSync } from "node:crypto";
import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import type { Page } from "@playwright/test";
import { WebSocketServer } from "ws";
import { startGatewayContainer } from "./gateway_container.js";
import { startKeycloakContainer } from "./keycloak_container.js";
import { startPostgresContainer } from "./postgres_container.js";

export async function startAuthenticatedGateway(
  page: Page,
  gatewayEnvironment: Record<string, string> = {},
) {
  const keycloak = await startKeycloakContainer();
  const postgres = await startPostgresContainer();
  const canonical = await canonicalTeamServer();
  const keys = generateKeyPairSync("rsa", { modulusLength: 2048 });
  const privateKey = Buffer.from(
    keys.privateKey.export({ format: "pem", type: "pkcs8" }),
  ).toString("base64");
  let gateway: Awaited<ReturnType<typeof startGatewayContainer>> | undefined;
  try {
    gateway = await startGatewayContainer(
      {
        API_GATEWAY_OIDC_ISSUER: keycloak.issuer,
        API_GATEWAY_OIDC_CLIENT_ID: "flowai-api-gateway-e2e",
        API_GATEWAY_OIDC_REDIRECT_URI: "${GATEWAY_BASE_URL}/auth/v1/callback",
        API_GATEWAY_OIDC_TEAM_CLAIM_ADAPTER: "string_array",
        API_GATEWAY_OIDC_TEAM_CLAIM_POINTER: "/teams",
        API_GATEWAY_OIDC_TEAM_CLAIM_SOURCE: "id_token",
        API_GATEWAY_OIDC_ENDPOINT_TIMEOUT: "5s",
        API_GATEWAY_POSTGRES_URL: postgres.connectionString,
        API_GATEWAY_SESSION_KEY_HEX: "6c".repeat(32),
        API_GATEWAY_WEB_UI_URL: "${GATEWAY_BASE_URL}/after-login",
        API_GATEWAY_STATE_REGISTRY_URL: canonical.baseURL,
        API_GATEWAY_ADMIN_ISSUER: keycloak.issuer,
        API_GATEWAY_ADMIN_AUDIENCE: "flowai-api-gateway-admin",
        API_GATEWAY_ADMIN_JWKS_URL: keycloak.discovery.jwks_uri,
        API_GATEWAY_ADMIN_ROLE_CLAIM_POINTER: "/realm_access/roles",
        API_GATEWAY_ADMIN_JWKS_TIMEOUT: "5s",
        API_GATEWAY_ADMIN_TOKEN_MAX_AGE: "5m",
        API_GATEWAY_WORKING_TOKEN_PRIVATE_KEY_BASE64: privateKey,
        API_GATEWAY_WORKING_TOKEN_KEY_ID: "flowai-working-e2e-1",
        API_GATEWAY_WORKING_TOKEN_ISSUER: "https://gateway.flowai.invalid",
        API_GATEWAY_WORKING_TOKEN_AUDIENCE: "flowai-state-registry",
        API_GATEWAY_WORKING_TOKEN_TTL: "5m",
        ...gatewayEnvironment,
      },
      { hostNetwork: true },
    );
    await keycloak.allowRedirectURI(`${gateway.baseUrl}/auth/v1/callback`);
    const adminResponse = await fetch(keycloak.discovery.token_endpoint, {
      method: "POST",
      headers: { "content-type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({
        grant_type: "password",
        client_id: "flowai-admin-e2e",
        username: "operator-admin",
        password: "flowai-e2e-password",
      }),
    });
    if (!adminResponse.ok)
      throw new Error(`admin token status ${adminResponse.status}`);
    const adminToken = (
      (await adminResponse.json()) as { access_token: string }
    ).access_token;
    for (const [oidcTeamID, teamID] of [
      ["oidc-team-alpha", "team-alpha"],
      ["oidc-team-beta", "team-beta"],
    ]) {
      const response = await fetch(
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
      if (response.status !== 201)
        throw new Error(
          `mapping status ${response.status}: ${await response.text()}`,
        );
    }
    await page.goto(`${gateway.baseUrl}/auth/v1/login`);
    await page.locator("#username").fill("operator-admin");
    await page.locator("#password").fill("flowai-e2e-password");
    const callback = page.waitForResponse((response) =>
      response.url().startsWith(`${gateway!.baseUrl}/auth/v1/callback?`),
    );
    await page.locator("#kc-login").click();
    const callbackResponse = await callback;
    await page.waitForURL(`${gateway.baseUrl}/after-login`);
    const setCookie =
      (await callbackResponse.headersArray()).find(
        (header) => header.name.toLowerCase() === "set-cookie",
      )?.value ?? "";
    const sessionID = /flowai_session=([^;]+)/.exec(setCookie)?.[1];
    if (!sessionID) throw new Error("Gateway callback set no session cookie");
    const activeGateway = gateway;
    return {
      baseURL: activeGateway.baseUrl,
      sessionID,
      publicKey: keys.publicKey,
      postgresURL: postgres.connectionString,
      downstreamRequests: canonical.requests,
      stopOIDC: async () => {
        await keycloak.close();
      },
      issue: async (teamID: string) =>
        fetch(`${activeGateway.baseUrl}/auth/v1/token`, {
          method: "POST",
          headers: {
            cookie: `flowai_session=${sessionID}`,
            "content-type": "application/json",
          },
          body: JSON.stringify({ team_id: teamID }),
        }),
      close: async () => {
        await activeGateway.close();
        await canonical.close();
        await postgres.close();
        await keycloak.close();
      },
    };
  } catch (error) {
    await gateway?.close();
    await canonical.close();
    await postgres.close();
    await keycloak.close();
    throw error;
  }
}

async function canonicalTeamServer() {
  const teams: Record<string, string> = {
    "team-alpha": "Alpha Team",
    "team-beta": "Beta Team",
  };
  type HeaderMap = Record<string, string | string[] | undefined>;
  const requests: Array<{ method: string; url: string; headers: HeaderMap }> =
    [];
  const sockets = new WebSocketServer({ noServer: true });
  const server = createServer((request, response) => {
    if ((request.url ?? "").startsWith("/ui/v1/echo")) {
      requests.push({
        method: request.method ?? "",
        url: request.url ?? "",
        headers: request.headers,
      });
      response.writeHead(207, {
        "content-type": "application/json",
        "x-state-registry-result": "unchanged",
      });
      response.end(
        JSON.stringify({ source: "state-registry", query: request.url }),
      );
      return;
    }
    if ((request.url ?? "").startsWith("/ui/v1/denied")) {
      requests.push({
        method: request.method ?? "",
        url: request.url ?? "",
        headers: request.headers,
      });
      response.writeHead(404, {
        "content-type": "application/json",
        "x-state-registry-result": "denied",
      });
      response.end(
        JSON.stringify({
          code: "not_found",
          message: "resource is not visible",
        }),
      );
      return;
    }
    const teamID =
      /^\/internal\/v1\/teams\/([^/]+)$/.exec(request.url ?? "")?.[1] ?? "";
    if (!teams[teamID]) {
      response.writeHead(404);
      response.end();
      return;
    }
    response.writeHead(200, { "content-type": "application/json" });
    response.end(
      JSON.stringify({
        team_id: teamID,
        team_name: teams[teamID],
        archived_at: null,
      }),
    );
  });
  server.on("upgrade", (request, socket, head) => {
    if (!(request.url ?? "").startsWith("/ui/v1/stream")) {
      socket.destroy();
      return;
    }
    requests.push({
      method: request.method ?? "",
      url: request.url ?? "",
      headers: request.headers,
    });
    sockets.handleUpgrade(request, socket, head, (websocket) =>
      sockets.emit("connection", websocket, request),
    );
  });
  sockets.on("connection", (socket, request) => {
    const teamID = String(request.headers["x-flowai-team-id"] ?? "");
    setTimeout(() => {
      if (socket.readyState === socket.OPEN)
        socket.send(
          JSON.stringify({
            frame_id: `${teamID}-replay`,
            team_id: teamID,
            kind: "replay",
          }),
        );
    }, 20);
    setTimeout(() => {
      if (socket.readyState === socket.OPEN)
        socket.send(
          JSON.stringify({
            frame_id: `${teamID}-live`,
            team_id: teamID,
            kind: "live",
          }),
        );
    }, 60);
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
      for (const socket of sockets.clients) socket.terminate();
      sockets.close();
      await new Promise<void>((resolve, reject) =>
        server.close((error) => (error ? reject(error) : resolve())),
      );
    },
  };
}
