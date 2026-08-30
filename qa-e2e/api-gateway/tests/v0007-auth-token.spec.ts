import { verify } from "node:crypto";
import { expect, test } from "@playwright/test";
import { startAuthenticatedGateway } from "../fixtures/authenticated_gateway.js";
import pg from "pg";
import { ReadOnlyDatabaseAssertions } from "../fixtures/harness.js";

test("v0007.1 v0007.3 and v0007.24 issue immutable one-team RS256 tokens with distinct jti", async ({
  page,
}) => {
  const gateway = await startAuthenticatedGateway(page);
  try {
    const alpha1 = await gateway.issue("team-alpha");
    const alpha2 = await gateway.issue("team-alpha");
    const beta = await gateway.issue("team-beta");
    for (const response of [alpha1, alpha2, beta])
      expect(response.status).toBe(200);
    const tokens = await Promise.all(
      [alpha1, alpha2, beta].map(
        async (response) =>
          ((await response.json()) as { access_token: string }).access_token,
      ),
    );
    const claims = tokens.map((token) =>
      decodeAndVerify(token, gateway.publicKey),
    );
    expect(claims.map((value) => value.team_id)).toEqual([
      "team-alpha",
      "team-alpha",
      "team-beta",
    ]);
    expect(new Set(claims.map((value) => value.jti)).size).toBe(3);
    for (const value of claims) {
      expect(value.operator_id).toBe(value.sub);
      expect(value.aud).toBe("flowai-state-registry");
      expect(value.exp).toBeGreaterThan(value.iat);
    }
    const database = new ReadOnlyDatabaseAssertions(gateway.postgresURL);
    try {
      expect(
        await database.exactRows(
          `SELECT count(*)::int AS count, count(DISTINCT encode(jti_hash, 'hex'))::int AS distinct_count FROM api_gateway.working_token_records`,
        ),
      ).toEqual([{ count: 3, distinct_count: 3 }]);
    } finally {
      await database.close();
    }
  } finally {
    await gateway.close();
  }
});

test("v0007.2 and v0007.3 reject an inaccessible selected team", async ({
  page,
}) => {
  const gateway = await startAuthenticatedGateway(page);
  try {
    const response = await gateway.issue("team-forged");
    expect(response.status).toBe(403);
    expect(await response.json()).toMatchObject({
      code: "team_not_accessible",
      request_id: expect.any(String),
    });
  } finally {
    await gateway.close();
  }
});

test("v0007.4 logout deletes server session and prevents further token issue", async ({
  page,
}) => {
  const gateway = await startAuthenticatedGateway(page);
  try {
    const logout = await fetch(`${gateway.baseURL}/auth/v1/session`, {
      method: "DELETE",
      headers: { cookie: `flowai_session=${gateway.sessionID}` },
    });
    expect(logout.status).toBe(204);
    expect(logout.headers.get("set-cookie")).toContain("Max-Age=0");
    const response = await gateway.issue("team-alpha");
    expect(response.status).toBe(401);
    const database = new ReadOnlyDatabaseAssertions(gateway.postgresURL);
    try {
      expect(
        await database.exactRows(
          `SELECT count(*)::int AS count FROM api_gateway.oidc_sessions`,
        ),
      ).toEqual([{ count: 0 }]);
    } finally {
      await database.close();
    }
  } finally {
    await gateway.close();
  }
});

test("v0007.2 and v0007.4 validate bearer, contain credentials, and inject canonical context", async ({
  page,
}) => {
  const gateway = await startAuthenticatedGateway(page);
  try {
    const issued = await gateway.issue("team-alpha");
    const token = ((await issued.json()) as { access_token: string })
      .access_token;
    const response = await fetch(
      `${gateway.baseURL}/ui/v1/echo?value=preserved`,
      {
        headers: {
          authorization: `Bearer ${token}`,
          cookie: "browser=secret",
          "x-flowai-operator-id": "forged",
          "x-flowai-team-id": "team-beta",
          "x-flowai-request-id": "forged",
        },
      },
    );
    expect(response.status).toBe(207);
    expect(response.headers.get("x-state-registry-result")).toBe("unchanged");
    expect(await response.json()).toEqual({
      source: "state-registry",
      query: "/ui/v1/echo?value=preserved",
    });
    expect(gateway.downstreamRequests).toHaveLength(1);
    const observed = gateway.downstreamRequests[0];
    expect(observed.headers.authorization).toBeUndefined();
    expect(observed.headers.cookie).toBeUndefined();
    expect(observed.headers["x-flowai-operator-id"]).not.toBe("forged");
    expect(observed.headers["x-flowai-team-id"]).toBe("team-alpha");
    expect(observed.headers["x-flowai-request-id"]).not.toBe("forged");
    const invalid = await fetch(`${gateway.baseURL}/ui/v1/echo`, {
      headers: { authorization: "Bearer invalid" },
    });
    expect(invalid.status).toBe(401);
    expect(gateway.downstreamRequests).toHaveLength(1);
  } finally {
    await gateway.close();
  }
});

test("v0007.2 v0007.23 fail closed when mandatory membership refresh is unavailable", async ({
  page,
}) => {
  const gateway = await startAuthenticatedGateway(page, {
    API_GATEWAY_MEMBERSHIP_MAX_AGE: "0s",
  });
  const database = new pg.Pool({ connectionString: gateway.postgresURL });
  try {
    const refreshed = await gateway.issue("team-alpha");
    expect(refreshed.status).toBe(200);
    await gateway.stopOIDC();
    const response = await gateway.issue("team-alpha");
    expect(response.status).toBe(502);
    expect(((await response.json()) as { code: string }).code).toBe(
      "oidc_provider_unavailable",
    );
    const rows = await database.query(
      `SELECT count(*)::int AS count FROM api_gateway.working_token_records`,
    );
    expect(rows.rows).toEqual([{ count: 1 }]);
  } finally {
    await database.end();
    await gateway.close();
  }
});

function decodeAndVerify(token: string, publicKey: any): Record<string, any> {
  const [header, payload, signature] = token.split(".");
  if (!signature) throw new Error("not JWT");
  expect(JSON.parse(Buffer.from(header, "base64url").toString())).toMatchObject(
    { alg: "RS256", kid: "flowai-working-e2e-1" },
  );
  expect(
    verify(
      "RSA-SHA256",
      Buffer.from(`${header}.${payload}`),
      publicKey,
      Buffer.from(signature, "base64url"),
    ),
  ).toBe(true);
  return JSON.parse(Buffer.from(payload, "base64url").toString());
}
