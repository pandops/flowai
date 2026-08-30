import { test, expect } from "@playwright/test";
import {
  ProviderNeutralOIDCFixture,
  StateRegistryRequestSpy,
  WebSocketEventPublisher,
  adminJWTFixture,
  opaqueSessionID,
  pkcePair,
  postgresIsolation,
  sessionFixture,
} from "../fixtures/harness.js";

test("provider-neutral harness exposes deterministic OIDC and isolation primitives", async ({
  request,
}) => {
  const oidc = new ProviderNeutralOIDCFixture({ teams: ["team-a", "team-b"] });
  await oidc.start();
  try {
    const discovery = await request.get(
      `${oidc.issuer}/.well-known/openid-configuration`,
    );
    expect(discovery.status()).toBe(200);
    expect((await discovery.json()).issuer).toBe(oidc.issuer);
    const jwks = await request.get(`${oidc.issuer}/jwks`);
    expect((await jwks.json()).keys[0]).toMatchObject({
      alg: "RS256",
      use: "sig",
    });
    oidc.setFailure("userinfo", { kind: "status", status: 503 });
    expect((await request.get(`${oidc.issuer}/userinfo`)).status()).toBe(503);
    expect(oidc.requests.map((value) => value.url)).toEqual([
      "/.well-known/openid-configuration",
      "/jwks",
      "/userinfo",
    ]);
  } finally {
    await oidc.stop();
  }

  const pkce = pkcePair();
  expect(pkce.verifier).not.toBe(pkce.challenge);
  expect(opaqueSessionID()).not.toBe(opaqueSessionID());
  expect(sessionFixture().sessionID).toBeTruthy();
  expect(
    adminJWTFixture("https://admin.invalid", "flowai-admin", [
      "flowai-system-admin",
    ]).token.split("."),
  ).toHaveLength(3);
  expect(postgresIsolation.gateway.schema).not.toBe(
    postgresIsolation.stateRegistry.schema,
  );
  expect(postgresIsolation.gateway.runtimeRole).not.toBe(
    postgresIsolation.stateRegistry.runtimeRole,
  );

  const spy = new StateRegistryRequestSpy();
  spy.record({
    method: "GET",
    url: "/internal/v1/teams/team-a",
    headers: {},
    body: "",
  });
  expect(spy.last().url).toBe("/internal/v1/teams/team-a");

  const publisher = new WebSocketEventPublisher();
  expect(publisher.address()).toMatch(/^ws:\/\/127\.0\.0\.1:/);
  await publisher.close();
});
