import { execFile } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

const exec = promisify(execFile);
const repoRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../../..",
);
const realmPath = path.join(
  repoRoot,
  "qa-e2e/api-gateway/fixtures/keycloak/flowai-e2e-realm.json",
);
const runtime = process.env.FLOWAI_CONTAINER_RUNTIME ?? "docker";
export const keycloakImage = "quay.io/keycloak/keycloak:26.6.3";

export interface OIDCDiscovery {
  issuer: string;
  authorization_endpoint: string;
  token_endpoint: string;
  userinfo_endpoint: string;
  jwks_uri: string;
}

export type KeycloakContainer = Readonly<{
  baseUrl: string;
  issuer: string;
  discovery: OIDCDiscovery;
  imageID: string;
  containerID: string;
  allowRedirectURI: (redirectURI: string) => Promise<void>;
  setOperatorGroups: (groupNames: string[]) => Promise<void>;
  setOperatorAdminRole: (enabled: boolean) => Promise<void>;
  close: () => Promise<void>;
}>;

export async function startKeycloakContainer(): Promise<KeycloakContainer> {
  await exec(runtime, ["pull", keycloakImage], {
    cwd: repoRoot,
    maxBuffer: 20 * 1024 * 1024,
  });
  const started = await exec(
    runtime,
    [
      "run",
      "--detach",
      "--rm",
      "--publish",
      "127.0.0.1::8080",
      "--label",
      "flowai.e2e.service=keycloak",
      "--env",
      "KC_BOOTSTRAP_ADMIN_USERNAME=flowai-bootstrap-admin",
      "--env",
      "KC_BOOTSTRAP_ADMIN_PASSWORD=flowai-bootstrap-password",
      "--env",
      "KC_HOSTNAME_STRICT=false",
      "--env",
      "KC_HTTP_ENABLED=true",
      "--volume",
      `${realmPath}:/opt/keycloak/data/import/flowai-e2e-realm.json:ro,Z`,
      keycloakImage,
      "start-dev",
      "--import-realm",
    ],
    { cwd: repoRoot, maxBuffer: 20 * 1024 * 1024 },
  );
  const containerID = started.stdout.trim();
  if (!containerID)
    throw new Error("container runtime returned no Keycloak container id");

  try {
    const portResult = await exec(runtime, ["port", containerID, "8080/tcp"], {
      cwd: repoRoot,
    });
    const published = portResult.stdout.trim().split("\n")[0];
    const port = published.slice(published.lastIndexOf(":") + 1);
    if (!/^\d+$/.test(port))
      throw new Error(
        `cannot resolve Keycloak published port from ${published}`,
      );
    const baseUrl = `http://127.0.0.1:${port}`;
    const discovery = await waitForDiscovery(
      `${baseUrl}/realms/flowai-e2e/.well-known/openid-configuration`,
      containerID,
    );
    const inspected = await exec(
      runtime,
      ["image", "inspect", keycloakImage, "--format", "{{.Id}}"],
      { cwd: repoRoot },
    );
    return {
      baseUrl,
      issuer: `${baseUrl}/realms/flowai-e2e`,
      discovery,
      imageID: inspected.stdout.trim(),
      containerID,
      allowRedirectURI: async (redirectURI: string) => {
        await configureClientRedirect(baseUrl, redirectURI);
      },
      setOperatorGroups: async (groupNames: string[]) => {
        await configureOperatorGroups(baseUrl, groupNames);
      },
      setOperatorAdminRole: async (enabled: boolean) => {
        await configureOperatorAdminRole(baseUrl, enabled);
      },
      close: async () => {
        await exec(runtime, ["stop", "--time", "3", containerID], {
          cwd: repoRoot,
        }).catch(() => undefined);
      },
    };
  } catch (error) {
    const logs = await exec(runtime, ["logs", containerID], {
      cwd: repoRoot,
      maxBuffer: 10 * 1024 * 1024,
    }).catch(() => ({ stdout: "" }));
    await exec(runtime, ["stop", "--time", "3", containerID], {
      cwd: repoRoot,
    }).catch(() => undefined);
    throw new Error(
      `Keycloak 26.6.3 failed to start: ${String(error)}\n${logs.stdout}`,
    );
  }
}

async function configureOperatorAdminRole(
  baseUrl: string,
  enabled: boolean,
): Promise<void> {
  const accessToken = await bootstrapAccessToken(baseUrl);
  const headers = {
    authorization: `Bearer ${accessToken}`,
    "content-type": "application/json",
  };
  const usersResponse = await fetch(
    `${baseUrl}/admin/realms/flowai-e2e/users?username=operator-admin&exact=true`,
    { headers },
  );
  if (!usersResponse.ok)
    throw new Error(`Keycloak user lookup status ${usersResponse.status}`);
  const users = (await usersResponse.json()) as Array<{ id: string }>;
  if (users.length !== 1)
    throw new Error(`expected one Keycloak E2E user, got ${users.length}`);
  const roleResponse = await fetch(
    `${baseUrl}/admin/realms/flowai-e2e/roles/flowai-system-admin`,
    { headers },
  );
  if (!roleResponse.ok)
    throw new Error(`Keycloak admin role lookup status ${roleResponse.status}`);
  const role = (await roleResponse.json()) as Record<string, unknown>;
  const response = await fetch(
    `${baseUrl}/admin/realms/flowai-e2e/users/${users[0].id}/role-mappings/realm`,
    {
      method: enabled ? "POST" : "DELETE",
      headers,
      body: JSON.stringify([role]),
    },
  );
  if (response.status !== 204)
    throw new Error(
      `Keycloak admin role ${enabled ? "add" : "remove"} status ${response.status}`,
    );
}

async function configureOperatorGroups(
  baseUrl: string,
  groupNames: string[],
): Promise<void> {
  const accessToken = await bootstrapAccessToken(baseUrl);
  const headers = {
    authorization: `Bearer ${accessToken}`,
    "content-type": "application/json",
  };
  const usersResponse = await fetch(
    `${baseUrl}/admin/realms/flowai-e2e/users?username=operator-admin&exact=true`,
    { headers },
  );
  if (!usersResponse.ok)
    throw new Error(`Keycloak user lookup status ${usersResponse.status}`);
  const users = (await usersResponse.json()) as Array<{ id: string }>;
  if (users.length !== 1)
    throw new Error(`expected one Keycloak E2E user, got ${users.length}`);
  const groupsResponse = await fetch(
    `${baseUrl}/admin/realms/flowai-e2e/groups`,
    { headers },
  );
  if (!groupsResponse.ok)
    throw new Error(`Keycloak group lookup status ${groupsResponse.status}`);
  const groups = (await groupsResponse.json()) as Array<{
    id: string;
    name: string;
  }>;
  const wanted = new Set(groupNames);
  for (const group of groups) {
    const method = wanted.has(group.name) ? "PUT" : "DELETE";
    const response = await fetch(
      `${baseUrl}/admin/realms/flowai-e2e/users/${users[0].id}/groups/${group.id}`,
      { method, headers },
    );
    if (response.status !== 204)
      throw new Error(
        `Keycloak ${method} user group ${group.name} status ${response.status}`,
      );
  }
}

async function bootstrapAccessToken(baseUrl: string): Promise<string> {
  const tokenResponse = await fetch(
    `${baseUrl}/realms/master/protocol/openid-connect/token`,
    {
      method: "POST",
      headers: { "content-type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({
        grant_type: "password",
        client_id: "admin-cli",
        username: "flowai-bootstrap-admin",
        password: "flowai-bootstrap-password",
      }),
    },
  );
  if (!tokenResponse.ok)
    throw new Error(`Keycloak bootstrap token status ${tokenResponse.status}`);
  return ((await tokenResponse.json()) as { access_token: string })
    .access_token;
}

async function configureClientRedirect(
  baseUrl: string,
  redirectURI: string,
): Promise<void> {
  const accessToken = await bootstrapAccessToken(baseUrl);
  const headers = {
    authorization: `Bearer ${accessToken}`,
    "content-type": "application/json",
  };
  const clientsResponse = await fetch(
    `${baseUrl}/admin/realms/flowai-e2e/clients?clientId=flowai-api-gateway-e2e`,
    { headers },
  );
  if (!clientsResponse.ok)
    throw new Error(`Keycloak client lookup status ${clientsResponse.status}`);
  const clients = (await clientsResponse.json()) as Array<
    Record<string, unknown> & { id: string }
  >;
  if (clients.length !== 1)
    throw new Error(`expected one Keycloak E2E client, got ${clients.length}`);
  const client = {
    ...clients[0],
    redirectUris: [redirectURI],
    webOrigins: [new URL(redirectURI).origin],
  };
  const updateResponse = await fetch(
    `${baseUrl}/admin/realms/flowai-e2e/clients/${clients[0].id}`,
    { method: "PUT", headers, body: JSON.stringify(client) },
  );
  if (updateResponse.status !== 204)
    throw new Error(`Keycloak client update status ${updateResponse.status}`);
}

async function waitForDiscovery(
  url: string,
  containerID: string,
): Promise<OIDCDiscovery> {
  let lastError: unknown;
  for (let attempt = 0; attempt < 180; attempt += 1) {
    try {
      const response = await fetch(url);
      if (response.ok) return (await response.json()) as OIDCDiscovery;
      lastError = new Error(`discovery status ${response.status}`);
    } catch (error) {
      lastError = error;
    }
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
  const logs = await exec(runtime, ["logs", containerID], {
    cwd: repoRoot,
    maxBuffer: 10 * 1024 * 1024,
  }).catch(() => ({ stdout: "" }));
  throw new Error(
    `Keycloak discovery did not become ready: ${String(lastError)}\n${logs.stdout}`,
  );
}
