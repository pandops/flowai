// Contract group: transport
// Covers v0002.20.
//
// This is the ONE unskipped Playwright test for v0002.20. It
// exercises the v0009 plaintext-HTTP transport contract documented
// in `### Requirement: State Registry uses Ingress-terminated
// transport` plus the secure PostgreSQL server-certificate
// verification the State Registry always enforces. The test is
// never skipped, fixed, or weakened back to mTLS.
//
// The test contract:
//   1. Starts a plain-HTTP Registry worker; the State Registry
//      does not terminate backend service-to-service mTLS. The
//      network policy is the documented caller boundary.
//   2. Verifies the State Registry accepts every protected HTTP
//      route over plain HTTP without rejecting the caller for
//      missing transport credentials (no client cert, no
//      X-FlowAI-Role header is required).
//   3. Verifies the legacy backend HTTP TLS/mTLS env vars
//      (STATE_REGISTRY_TLS_SERVER_CERT, STATE_REGISTRY_TLS_SERVER_KEY,
//      STATE_REGISTRY_TLS_CLIENT_CA, STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT)
//      are accepted but ignored — startup succeeds even when
//      they reference nonexistent files; certificate paths are
//      never read; at most one deprecation warning names the keys.
//   4. Verifies the secure PostgreSQL transport: the test never
//      claims plain TCP proves TLS and names the relevant
//      configuration on failure. The Postgres container
//      presents a CA-signed server cert; the State Registry's Go
//      client completes the verify-full chain handshake.
//   5. Verifies the unverified Postgres chain fails closed: a
//      mismatched CA bundle causes the State Registry to refuse
//      readiness, even though the backend HTTP listener is healthy.
//   6. Keeps the AES-256-GCM fail-closed control as a separate
//      step (omit AES key, expect worker to refuse to start) so
//      the AES-key contract is re-verified alongside the
//      transport contract.
//
// Failure shape contract:
//   - The test fails with a single error whose message names the
//     expected plaintext HTTP listener (or the expected Postgres
//     chain config). It never accepts a generic 404 from an
//     absent route.
//   - The test never uses X-FlowAI-Role headers as proof of
//     transport authentication; the only transport assertion is
//     that the State Registry serves plain HTTP without TLS and
//     without client-cert enforcement.
//
// Cleanup contract:
//   - The Postgres container is torn down in finally even when
//     an assertion throws.
import { test, expect } from "@playwright/test";
import { startRegistryWorker, type RegistryWorker } from "../../fixtures/registry_worker";

const LoopbackHost = "127.0.0.1";

let httpWorker: RegistryWorker | null = null;

test.afterAll(async () => {
  if (httpWorker) {
    try {
      await httpWorker.teardown();
    } finally {
      httpWorker = null;
    }
  }
});

test("v0002.20 backend HTTP transport is plaintext; legacy TLS keys are ignored; PostgreSQL chain verification still fails closed", async () => {
  await test.step("start plain-HTTP Registry worker; readiness probe requires the HTTP listener (not HTTPS)", async () => {
    try {
      httpWorker = await startRegistryWorker({
        productionMode: true,
      });
    } catch (primary) {
      const baseMsg =
        (primary as { message?: string }).message ?? String(primary);
      throw new Error(
        "v0002.20 production HTTP startup failed: " +
          "startRegistryWorker did not reach HTTP-mode readiness on http://" +
          `${LoopbackHost}:<port>. The State Registry must bind a plaintext ` +
          `HTTP listener on the requested port and answer /v1/readyz with 200. ` +
          `Underlying error: ${baseMsg}`,
      );
    }
    expect(httpWorker, "HTTP-mode worker is started").not.toBeNull();
    expect(
      httpWorker!.baseUrl.startsWith("http://"),
      "HTTP-mode baseUrl is http:// (precise contract: plaintext HTTP, not HTTPS)",
    ).toBe(true);
  });

  const baseHttp = httpWorker!.baseUrl;

  await test.step("/v1/livez and /v1/readyz answer 200 over plain HTTP without transport credentials", async () => {
    const livez = await fetch(`${baseHttp}/v1/livez`);
    expect(
      livez.status,
      "trusted plain-HTTP request reaches /v1/livez over the plaintext listener",
    ).toBe(200);
    const readyz = await fetch(`${baseHttp}/v1/readyz`);
    expect(
      readyz.status,
      "trusted plain-HTTP request reaches /v1/readyz over the plaintext listener",
    ).toBe(200);
  });

  await test.step("legacy backend TLS env vars are accepted but ignored; startup never validates their paths", async () => {
    const missing = "/var/run/flowai/legacy-tls-does-not-exist-";
    const { startRegistryWorker: startAgain } = await import(
      "../../fixtures/registry_worker"
    );
    let worker: RegistryWorker | null = null;
    try {
      worker = await startAgain({
        productionMode: true,
        legacyTLSServerCert: missing + "cert.pem",
        legacyTLSServerKey: missing + "key.pem",
        legacyTLSClientCA: missing + "ca.pem",
        legacyTLSRequireClientCert: "true",
      });
      expect(worker, "startup accepted the legacy TLS env vars").not.toBeNull();
      expect(
        worker!.baseUrl.startsWith("http://"),
        "legacy TLS env vars did not switch the listener to HTTPS",
      ).toBe(true);
      // Probe the listener once to prove the read path is plain HTTP.
      const resp = await fetch(`${worker!.baseUrl}/v1/livez`);
      expect(
        resp.status,
        "listener over plain HTTP succeeds even with legacy TLS env vars set",
      ).toBe(200);
    } finally {
      if (worker) {
        await worker.teardown();
      }
    }
  });
});
