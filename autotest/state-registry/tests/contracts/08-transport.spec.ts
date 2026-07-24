// Contract group: transport
// Covers v0002.20.
//
// This is the ONE unskipped Playwright test for v0002.20. It
// exercises the mTLS / encrypted-transport contract documented in
// `### Requirement: State Registry uses mutually authenticated
// encrypted transport` plus the four-level TLS shape the OpenAPI
// declares (SystemAdministratorMTLS, ListenerMTLS, ExecutorMTLS,
// GatewayMTLS). The test is never skipped, fixed, or weakened to
// plain HTTP.
//
// The test contract:
//   1. Generates an ephemeral CA + a localhost server cert (SAN
//      IP:127.0.0.1, DNS:localhost) and six client certs with
//      distinguishable role/team identities, plus a wrong-team
//      client cert, an untrusted-CA client cert, and an expired
//      client cert. Private keys never leave the ephemeral
//      material directory and are never logged.
//   2. Starts a TLS-mode Registry worker via the opt-in
//      STATE_REGISTRY_TLS_* env vars. The default startRegistryWorker()
//      (no TLS options) is byte-for-byte behavior compatible with
//      the plain-HTTP harness used by every other contract test.
//   3. Probes the HTTPS+mTLS listener with the trusted client cert
//      and the trusted CA bundle, with rejectUnauthorized=true and
//      the SAN hostname set. The worker fails its startup
//      readiness check when the Go scaffold binds plain HTTP
//      instead of HTTPS, which is the documented RED for the
//      current scaffold step.
//   4. Exercises every behavior-specific assertion in the
//      contract (in test.step blocks, each with a behavior-named
//      failure message):
//        - trusted service cert completes TLS and reaches team
//          authorization
//        - no cert, expired cert, and untrusted-CA cert fail the
//          handshake BEFORE any HTTP read
//        - valid wrong-role / wrong-team cert reaches or is
//          rejected at the correct authorization boundary without
//          mutation
//        - listener / Executor / Gateway certs cannot call /admin/*
//        - WebSocket upgrade uses wss:// with a trusted Gateway
//          cert; unauthenticated / untrusted handshakes fail
//        - server certificate / hostname verification is active
//          (a probe against a wrong servername fails before HTTP)
//   5. Encodes a precise RED observation when the Postgres
//      container / scaffold cannot be configured to enforce
//      server-chain verification: the test never claims plain TCP
//      proves TLS; it surfaces the gap and the env-var name the
//      State Registry is expected to consume.
//   6. Keeps the AES-256-GCM fail-closed control as a separate
//      step (omit AES key, expect worker to refuse to start) so
//      the AES-key contract is re-verified alongside the
//      transport contract.
//
// Failure shape contract:
//   - The test fails with a single error whose message names the
//     expected HTTPS / mTLS listener (or the expected Postgres
//     chain config). It never accepts a generic 404 from an
//     absent route.
//   - The test never uses X-FlowAI-Role headers as proof of
//     transport authentication; every transport assertion comes
//     from a Node tls.connect / https.request / wss:// handshake
//     against the real listener.
//   - The test never disables rejectUnauthorized and never falls
//     back to plain HTTP.
//
// Cleanup contract:
//   - The ephemeral material temp directory is removed in finally
//     even when an assertion throws.
//   - The TLS-mode Registry worker is torn down in finally even
//     when the readiness probe failed.
//   - All WebSocket connections opened in this test are closed
//     before the test returns.
import { test, expect } from '@playwright/test';
import { existsSync } from 'node:fs';
import WsWebSocket from 'ws';
import { startRegistryWorker, type RegistryWorker } from '../../fixtures/registry_worker';
import {
  createEphemeralTlsMaterial,
  httpsRequest,
  openWssSocket,
  tlsHandshakeProbe,
  type EphemeralTlsMaterial,
  type HttpsRequestResult,
} from '../../fixtures/tls_transport';
import { imageReference } from './_setup';

const LoopbackHost = '127.0.0.1';
const LoopbackServername = 'localhost';
const WssTestPath = '/v1/events/stream';

let tlsWorker: RegistryWorker | null = null;
let tlsMaterial: EphemeralTlsMaterial | null = null;
const openedWssSockets: WsWebSocket[] = [];

test.afterAll(async () => {
  while (openedWssSockets.length > 0) {
    const ws = openedWssSockets.pop();
    if (!ws) break;
    try {
      ws.terminate();
    } catch {
      // best-effort
    }
  }
  if (tlsWorker) {
    try {
      await tlsWorker.teardown();
    } finally {
      tlsWorker = null;
    }
  }
  if (tlsMaterial) {
    try {
      await tlsMaterial.cleanup();
    } finally {
      tlsMaterial = null;
    }
  }
});

test('v0002.20 protected APIs reject untrusted transport identities before request processing; mTLS, WSS, and Postgres-chain contract', async () => {
  tlsMaterial = await createEphemeralTlsMaterial({ lifetimeDays: 1 });
  const material = tlsMaterial;

  await test.step('ephemeral TLS material is generated; private keys live only in the temp directory', async () => {
    expect(existsSync(material.caCertPath), 'CA cert exists').toBe(true);
    expect(existsSync(material.caKeyPath), 'CA key exists (test does not log it)').toBe(true);
    expect(existsSync(material.serverCertPath), 'server cert exists').toBe(true);
    expect(existsSync(material.serverKeyPath), 'server key exists (test does not log it)').toBe(true);
    for (const c of [
      material.trustedListener,
      material.trustedTeamExecutor,
      material.trustedGateway,
      material.wrongRoleCert,
      material.wrongTeamCert,
      material.untrustedCaCert,
      material.expiredCert,
    ]) {
      expect(existsSync(c.certPath), `client cert ${c.cn} exists`).toBe(true);
      expect(existsSync(c.keyPath), `client key ${c.cn} exists (test does not log it)`).toBe(true);
    }
  });

  await test.step('start TLS-mode Registry worker; readiness probe requires the trusted HTTPS+mTLS listener', async () => {
    try {
      tlsWorker = await startRegistryWorker({
        tlsServerCertPath: material.serverCertPath,
        tlsServerKeyPath: material.serverKeyPath,
        tlsClientCaPath: material.caCertPath,
        tlsRequireClientCert: true,
        tlsClientCertPath: material.trustedGateway.certPath,
        tlsClientKeyPath: material.trustedGateway.keyPath,
        tlsServername: LoopbackServername,
      });
    } catch (primary) {
      const diagnostics = (primary as { diagnostics?: string }).diagnostics ?? '';
      const baseMsg = (primary as { message?: string }).message ?? String(primary);
      throw new Error(
        'v0002.20 RED (State Registry does not yet serve HTTPS+mTLS): ' +
        'startRegistryWorker failed to reach TLS-mode readiness on https://' +
        `${LoopbackHost}:<port> using STATE_REGISTRY_TLS_SERVER_CERT=` +
        `${material.serverCertPath}, STATE_REGISTRY_TLS_SERVER_KEY=<redacted>, ` +
        `STATE_REGISTRY_TLS_CLIENT_CA=${material.caCertPath}, ` +
        'STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT=true. ' +
        'The State Registry is expected to bind an https:// listener on the requested port, ' +
        'require a client cert from the trusted CA bundle, and answer /v1/readyz with 200 to a ' +
        'trusted client cert. Underlying error: ' +
        `${baseMsg}; diagnostics_tail=${diagnostics.slice(-512)}`,
      );
    }
    expect(tlsWorker, 'TLS-mode worker is started').not.toBeNull();
    expect(
      tlsWorker!.baseUrl.startsWith('https://'),
      'TLS-mode baseUrl is https:// (precise contract: HTTPS, not HTTP)',
    ).toBe(true);
  });

  const baseHttps = tlsWorker!.baseUrl;
  const port = tlsWorker!.bindPort;

  await test.step('trusted service cert completes TLS; /v1/livez and /v1/readyz answer 200 with the verified peer CN', async () => {
    const livez = await httpsRequest(`${baseHttps}/v1/livez`, {
      clientCertPath: material.trustedGateway.certPath,
      clientKeyPath: material.trustedGateway.keyPath,
      caBundlePath: material.caCertPath,
      servername: LoopbackServername,
    });
    expect(livez.status, 'trusted Gateway cert reaches /v1/livez over mTLS').toBe(200);
    expect(livez.peerCn, 'client verifies the Registry server certificate CN').toBe('flowai-v0002-20-server');
    const readyz = await httpsRequest(`${baseHttps}/v1/readyz`, {
      clientCertPath: material.trustedGateway.certPath,
      clientKeyPath: material.trustedGateway.keyPath,
      caBundlePath: material.caCertPath,
      servername: LoopbackServername,
    });
    expect(readyz.status, 'trusted Gateway cert reaches /v1/readyz over mTLS').toBe(200);
  });

  await test.step('handshake fails before any HTTP read: no client cert', async () => {
    const probe = await tlsHandshakeProbe({
      host: LoopbackHost,
      port,
      caBundlePath: material.caCertPath,
      servername: LoopbackServername,
    });
    expect(probe.ok, 'TLS handshake without a client cert is rejected before HTTP').toBe(false);
    expect(
      probe.clientCertRequired,
      'server returned TLS alert certificate_required (no client cert presented)',
    ).toBe(true);
  });

  await test.step('handshake fails before any HTTP read: expired client cert', async () => {
    const probe = await tlsHandshakeProbe({
      host: LoopbackHost,
      port,
      caBundlePath: material.caCertPath,
      clientCertPath: material.expiredCert.certPath,
      clientKeyPath: material.expiredCert.keyPath,
      servername: LoopbackServername,
    });
    expect(probe.ok, 'TLS handshake with an expired client cert is rejected before HTTP').toBe(false);
    expect(
      probe.tlsErrorCode === 'CERT_HAS_EXPIRED' || /expired/i.test(probe.message),
      `expected expired cert handshake to fail; got code=${probe.tlsErrorCode} message=${probe.message}`,
    ).toBe(true);
  });

  await test.step('handshake fails before any HTTP read: untrusted-CA client cert', async () => {
    const probe = await tlsHandshakeProbe({
      host: LoopbackHost,
      port,
      caBundlePath: material.caCertPath,
      clientCertPath: material.untrustedCaCert.certPath,
      clientKeyPath: material.untrustedCaCert.keyPath,
      servername: LoopbackServername,
    });
    expect(probe.ok, 'TLS handshake with a cert from an untrusted CA is rejected before HTTP').toBe(false);
    expect(
      probe.tlsErrorCode === 'UNABLE_TO_VERIFY_LEAF_SIGNATURE'
        || probe.tlsErrorCode === 'CERT_UNTRUSTED'
        || /unable to verify/i.test(probe.message)
        || /untrusted/i.test(probe.message),
      `expected untrusted-CA cert handshake to fail; got code=${probe.tlsErrorCode} message=${probe.message}`,
    ).toBe(true);
  });

  await test.step('server hostname verification is active: wrong servername is rejected before HTTP', async () => {
    const probe = await tlsHandshakeProbe({
      host: LoopbackHost,
      port,
      caBundlePath: material.caCertPath,
      clientCertPath: material.trustedGateway.certPath,
      clientKeyPath: material.trustedGateway.keyPath,
      servername: 'flowai-wrong-hostname.invalid',
    });
    expect(probe.ok, 'TLS handshake with a wrong servername is rejected before HTTP').toBe(false);
    expect(
      probe.tlsErrorCode === 'ERR_TLS_CERT_ALTNAME_INVALID'
        || /hostname/i.test(probe.message)
        || /altname/i.test(probe.message)
        || /does not match/i.test(probe.message),
      `expected hostname-mismatch failure; got code=${probe.tlsErrorCode} message=${probe.message}`,
    ).toBe(true);
  });

  await test.step('valid wrong-role cert completes TLS but is rejected at the /admin/* authorization boundary (no mutation)', async () => {
    const livez = await httpsRequest(`${baseHttps}/v1/livez`, {
      clientCertPath: material.wrongRoleCert.certPath,
      clientKeyPath: material.wrongRoleCert.keyPath,
      caBundlePath: material.caCertPath,
      servername: LoopbackServername,
    });
    expect(livez.status, 'public health probe remains reachable after the trusted TLS handshake').toBe(200);
    const adminPost = await httpsRequest(`${baseHttps}/admin/teams`, {
      method: 'POST',
      clientCertPath: material.wrongRoleCert.certPath,
      clientKeyPath: material.wrongRoleCert.keyPath,
      caBundlePath: material.caCertPath,
      servername: LoopbackServername,
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        team_name: `v0002-20-wrongrole-${Date.now().toString(36)}`,
        default_image: imageReference('default-v0002-20-wrongrole'),
      }),
    });
    expect(adminPost.status, 'wrong-role listener cert is forbidden on /admin/teams').toBe(403);
  });

  await test.step('listener, Executor, and Gateway certs each cannot call /admin/*; only a system-administrator identity may', async () => {
    const adminCases: Array<{ label: string; cert: { certPath: string; keyPath: string } }> = [
      { label: 'listener-team-a', cert: material.trustedListener },
      { label: 'team-executor-team-a', cert: material.trustedTeamExecutor },
      { label: 'gateway-team-a', cert: material.trustedGateway },
    ];
    for (const c of adminCases) {
      const resp = await httpsRequest(`${baseHttps}/admin/teams`, {
        method: 'POST',
        clientCertPath: c.cert.certPath,
        clientKeyPath: c.cert.keyPath,
        caBundlePath: material.caCertPath,
        servername: LoopbackServername,
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          team_name: `v0002-20-${c.label}-${Date.now().toString(36)}`,
          default_image: imageReference(`default-v0002-20-${c.label}`),
        }),
      });
      expect(resp.status, `${c.label} cert is forbidden on /admin/teams`).toBe(403);
    }
  });

  await test.step('valid wrong-team listener cert is rejected at the team authorization boundary without mutation', async () => {
    const adminPost = await httpsRequest(`${baseHttps}/v1/tasks`, {
      method: 'POST',
      clientCertPath: material.wrongTeamCert.certPath,
      clientKeyPath: material.wrongTeamCert.keyPath,
      caBundlePath: material.caCertPath,
      servername: LoopbackServername,
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({}),
    });
    expect(adminPost.status, 'wrong-team listener cert is forbidden before task ingestion').toBe(403);
  });

  await test.step('WebSocket upgrade uses wss:// with a trusted Gateway cert; unauthenticated/untrusted handshakes fail', async () => {
    const wssUrl = `wss://${LoopbackHost}:${port}${WssTestPath}`;

    const trustedWss = openWssSocket(wssUrl, {
      caBundlePath: material.caCertPath,
      clientCertPath: material.trustedGateway.certPath,
      clientKeyPath: material.trustedGateway.keyPath,
      servername: LoopbackServername,
    });
    openedWssSockets.push(trustedWss);
    const trustedOpened = await new Promise<boolean>((resolve) => {
      let settled = false;
      const onOpen = (): void => { if (!settled) { settled = true; resolve(true); } };
      const onError = (): void => { if (!settled) { settled = true; resolve(false); } };
      trustedWss.once('open', onOpen);
      trustedWss.once('error', onError);
      setTimeout(() => { if (!settled) { settled = true; resolve(false); } }, 5_000);
    });
    expect(
      trustedOpened,
      'wss:// upgrade with the trusted Gateway cert is accepted at the TLS layer',
    ).toBe(true);

    const noCertWss = openWssSocket(wssUrl, {
      caBundlePath: material.caCertPath,
      servername: LoopbackServername,
    });
    openedWssSockets.push(noCertWss);
    const noCertResult = await new Promise<{ ok: boolean; message: string }>((resolve) => {
      let settled = false;
      const onError = (err: Error): void => {
        if (!settled) { settled = true; resolve({ ok: false, message: err.message }); }
      };
      const onOpen = (): void => { if (!settled) { settled = true; resolve({ ok: true, message: 'open' }); } };
      noCertWss.once('error', onError);
      noCertWss.once('open', onOpen);
      setTimeout(() => { if (!settled) { settled = true; resolve({ ok: false, message: 'timeout' }); } }, 5_000);
    });
    expect(
      noCertResult.ok === false && /certificate required|alert/i.test(noCertResult.message),
      `wss:// upgrade without a client cert is rejected at the TLS layer; got ok=${noCertResult.ok} message=${noCertResult.message}`,
    ).toBe(true);

    const untrustedWss = openWssSocket(wssUrl, {
      caBundlePath: material.caCertPath,
      clientCertPath: material.untrustedCaCert.certPath,
      clientKeyPath: material.untrustedCaCert.keyPath,
      servername: LoopbackServername,
    });
    openedWssSockets.push(untrustedWss);
    const untrustedResult = await new Promise<{ ok: boolean; message: string }>((resolve) => {
      let settled = false;
      const onError = (err: Error): void => {
        if (!settled) { settled = true; resolve({ ok: false, message: err.message }); }
      };
      const onOpen = (): void => { if (!settled) { settled = true; resolve({ ok: true, message: 'open' }); } };
      untrustedWss.once('error', onError);
      untrustedWss.once('open', onOpen);
      setTimeout(() => { if (!settled) { settled = true; resolve({ ok: false, message: 'timeout' }); } }, 5_000);
    });
    expect(
      untrustedResult.ok === false
        && /unable to verify|untrusted/i.test(untrustedResult.message),
      `wss:// upgrade with an untrusted-CA cert is rejected at the TLS layer; got ok=${untrustedResult.ok} message=${untrustedResult.message}`,
    ).toBe(true);
  });

  await test.step('Postgres server-chain verification: valid chain reaches /v1/readyz=200; unverifiable chain fails closed before ready (precise RED when not implemented)', async () => {
    let pgTlsWorker: RegistryWorker | null = null;
    try {
      pgTlsWorker = await startRegistryWorker({
        tlsServerCertPath: material.serverCertPath,
        tlsServerKeyPath: material.serverKeyPath,
        tlsClientCaPath: material.caCertPath,
        tlsRequireClientCert: true,
        tlsPostgresCaPath: material.caCertPath,
        tlsPostgresVerifyMode: 'verify-full',
        tlsClientCertPath: material.trustedGateway.certPath,
        tlsClientKeyPath: material.trustedGateway.keyPath,
        tlsServername: LoopbackServername,
      });
      const result: HttpsRequestResult = await httpsRequest(`${pgTlsWorker.baseUrl}/v1/readyz`, {
        clientCertPath: material.trustedGateway.certPath,
        clientKeyPath: material.trustedGateway.keyPath,
        caBundlePath: material.caCertPath,
        servername: LoopbackServername,
      });
      expect(result.status, 'with a verified Postgres chain, /v1/readyz returns 200').toBe(200);
    } catch (primary) {
      const baseMsg = (primary as { message?: string }).message ?? String(primary);
      throw new Error(
        'v0002.20 RED (State Registry does not yet verify the Postgres server chain): ' +
        'startRegistryWorker with STATE_REGISTRY_POSTGRES_TLS_CA=' +
        `${material.caCertPath} and STATE_REGISTRY_POSTGRES_TLS_MODE=verify-full ` +
        'failed to reach /v1/readyz on https://' +
        `${LoopbackHost}:<port>. The State Registry is expected to consume the ` +
        'STATE_REGISTRY_POSTGRES_TLS_CA / STATE_REGISTRY_POSTGRES_TLS_MODE env vars, ' +
        'validate the Postgres server certificate chain at startup, and refuse to report ready ' +
        'when the chain is unverifiable. Underlying error: ' + baseMsg,
      );
    } finally {
      if (pgTlsWorker) {
        try {
          await pgTlsWorker.teardown();
        } catch {
          // best-effort
        }
      }
    }
  });

  await test.step('Postgres server-chain verification: invalid CA/hostname configuration fails closed before ready', async () => {
    let pgTlsWorker: RegistryWorker | null = null;
    let observedFailure = false;
    let lastMsg = '';
    try {
      pgTlsWorker = await startRegistryWorker({
        tlsServerCertPath: material.serverCertPath,
        tlsServerKeyPath: material.serverKeyPath,
        tlsClientCaPath: material.caCertPath,
        tlsRequireClientCert: true,
        tlsPostgresCaPath: material.untrustedCaCertPath,
        tlsPostgresVerifyMode: 'verify-full',
        tlsClientCertPath: material.trustedGateway.certPath,
        tlsClientKeyPath: material.trustedGateway.keyPath,
        tlsServername: LoopbackServername,
      });
      lastMsg = `worker started with STATE_REGISTRY_POSTGRES_TLS_CA=${material.untrustedCaCertPath}; expected fail-closed`;
    } catch (primary) {
      observedFailure = true;
      const msg = (primary as { message?: string }).message ?? String(primary);
      expect(
        /postgres|tls|certificate|chain|verify|ca|closed/i.test(msg),
        `startup failure names the Postgres chain failure as the cause: ${msg}`,
      ).toBe(true);
    } finally {
      if (pgTlsWorker) {
        try {
          await pgTlsWorker.teardown();
        } catch {
          // best-effort
        }
      }
    }
    if (!observedFailure) {
      throw new Error(
        'v0002.20 RED (State Registry does not fail closed on an unverifiable Postgres chain): ' +
        `startRegistryWorker reached readiness with STATE_REGISTRY_POSTGRES_TLS_CA=${material.untrustedCaCertPath} ` +
        'and STATE_REGISTRY_POSTGRES_TLS_MODE=verify-full, but the Postgres container here is reached with ' +
        'sslmode=disable and the State Registry is expected to refuse to report ready when the configured ' +
        'Postgres chain is unverifiable. Underlying observation: ' + lastMsg,
      );
    }
  });

  await test.step('AES-256-GCM data key fail-closed: omitting STATE_REGISTRY_AES_KEY_HEX must refuse to start the worker', async () => {
    let failureCaught = false;
    let w: RegistryWorker | null = null;
    try {
      w = await startRegistryWorker({ omitAesKey: true });
    } catch (primary) {
      failureCaught = true;
      const msg = (primary as { message?: string }).message ?? String(primary);
      expect(
        /AES_KEY_HEX|aes|key/i.test(msg),
        `AES-key fail-closed: startup failure names the AES key as the cause: ${msg}`,
      ).toBe(true);
    } finally {
      if (w) {
        try {
          await w.teardown();
        } catch {
          // best-effort
        }
      }
    }
    expect(
      failureCaught,
      'AES-256-GCM data key fail-closed: omitting STATE_REGISTRY_AES_KEY_HEX must refuse to start the worker',
    ).toBe(true);
  });
});
