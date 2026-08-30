import { expect, test } from "@playwright/test";
import { startAuthenticatedGateway } from "../fixtures/authenticated_gateway.js";

test("v0007.8 missing bearer is rejected before State Registry", async ({
  page,
}) => {
  const gateway = await startAuthenticatedGateway(page);
  try {
    const response = await fetch(`${gateway.baseURL}/ui/v1/echo`);
    expect(response.status).toBe(401);
    expect(await response.json()).toMatchObject({
      code: "invalid_working_token",
      request_id: expect.any(String),
    });
    expect(gateway.downstreamRequests).toHaveLength(0);
  } finally {
    await gateway.close();
  }
});

test("v0007.9 v0007.10 v0007.13 v0007.14 replace spoofed context and contain bearer", async ({
  page,
}) => {
  const gateway = await startAuthenticatedGateway(page);
  try {
    const token = (
      (await (await gateway.issue("team-alpha")).json()) as {
        access_token: string;
      }
    ).access_token;
    const response = await fetch(`${gateway.baseURL}/ui/v1/echo`, {
      headers: {
        authorization: `Bearer ${token}`,
        cookie: "flowai_session=forged",
        "x-flowai-operator-id": "forged",
        "x-flowai-team-id": "team-beta",
        "x-flowai-team-name": "Forged",
        "x-flowai-request-id": "forged",
      },
    });
    expect(response.status).toBe(207);
    const observed = gateway.downstreamRequests.at(-1)!;
    expect(observed.headers.authorization).toBeUndefined();
    expect(observed.headers.cookie).toBeUndefined();
    expect(observed.headers["x-flowai-team-id"]).toBe("team-alpha");
    expect(observed.headers["x-flowai-team-name"]).toBe("Alpha Team");
    expect(observed.headers["x-flowai-operator-id"]).toMatch(/^oidc_/);
    expect(observed.headers["x-flowai-request-id"]).toMatch(/^[a-f0-9]{32}$/);
  } finally {
    await gateway.close();
  }
});

test("v0007.11 preserves State Registry ownership denial status headers and body", async ({
  page,
}) => {
  const gateway = await startAuthenticatedGateway(page);
  try {
    const token = (
      (await (await gateway.issue("team-alpha")).json()) as {
        access_token: string;
      }
    ).access_token;
    const response = await fetch(`${gateway.baseURL}/ui/v1/denied`, {
      headers: { authorization: `Bearer ${token}` },
    });
    expect(response.status).toBe(404);
    expect(response.headers.get("x-state-registry-result")).toBe("denied");
    expect(await response.json()).toEqual({
      code: "not_found",
      message: "resource is not visible",
    });
  } finally {
    await gateway.close();
  }
});

test("v0007.12 stale token loses access before proxying", async ({ page }) => {
  const gateway = await startAuthenticatedGateway(page);
  try {
    const token = (
      (await (await gateway.issue("team-alpha")).json()) as {
        access_token: string;
      }
    ).access_token;
    const logout = await fetch(`${gateway.baseURL}/auth/v1/session`, {
      method: "DELETE",
      headers: { cookie: `flowai_session=${gateway.sessionID}` },
    });
    expect(logout.status).toBe(204);
    const response = await fetch(`${gateway.baseURL}/ui/v1/echo`, {
      headers: { authorization: `Bearer ${token}` },
    });
    expect(response.status).toBe(403);
    expect(gateway.downstreamRequests).toHaveLength(0);
  } finally {
    await gateway.close();
  }
});
