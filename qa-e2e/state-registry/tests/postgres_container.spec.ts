import { test, expect } from "@playwright/test";
import {
  mkdtempSync,
  rmSync,
  statSync,
  writeFileSync,
  chmodSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve as pathResolve } from "node:path";
import {
  StartPostgresContainer,
  StopPostgresContainer,
} from "../fixtures/postgres_container";

test.describe("StartPostgresContainer failure-injection", () => {
  test("StartPostgresContainer returns Error for primary pull failure (not AggregateError when no container)", async () => {
    let captured: unknown;
    try {
      await StartPostgresContainer({
        image: "docker.io/library/this-image-does-not-exist:0",
      });
    } catch (err) {
      captured = err;
    }
    expect(captured).toBeDefined();
    expect((captured as Error).name).toMatch(/Error/);
    expect((captured as Error).message).toMatch(
      /no such image|invalid|FATAL|manifest|invalid reference/i,
    );
  });

  test("StartPostgresContainer with bad image + removeFailureCount throws the primary error without cleanup", async () => {
    let captured: unknown;
    try {
      await StartPostgresContainer({
        image: "docker.io/library/this-image-does-not-exist:0",
        removeFailureCount: 1,
      });
    } catch (err) {
      captured = err;
    }
    expect(captured).toBeDefined();
    const err = captured as Error;
    expect(err.message).not.toMatch(/injected postgres remove failure/);
    expect(err.message).toMatch(
      /no such image|invalid|FATAL|manifest|invalid reference/i,
    );
  });

  test("StopPostgresContainer with a null container returns null", async () => {
    const err = await StopPostgresContainer(null);
    expect(err).toBeNull();
  });
});

// v0002.20 TLS-mode validation. These tests exercise the
// fail-closed validation paths in StartPostgresContainer without
// booting a real postgres container (which would require a
// container runtime, the postgres:16 image, and a real cert
// chain). The TLS-mode runtime path is exercised end-to-end by
// the contract test (tests/contracts/08-transport.spec.ts), which
// already proves the v0002.20 readyz=200 and fail-closed paths.
//
// Regression net for hands-on QA finding: this describe previously
// leaked ~16 /tmp/flowai-pg-tls-precond-* dirs per focused run with
// world-readable fake.key files (mode 0644). The afterEach +
// stagedPreconditionDirs registration + chmod 0600 on the key +
// chmod 0700 on the dir below are the regression net; deleting
// any of them risks reintroducing the leak.
test.describe("StartPostgresContainer TLS-mode validation", () => {
  const stagedPreconditionDirs: string[] = [];

  test.afterEach(() => {
    while (stagedPreconditionDirs.length > 0) {
      const dir = stagedPreconditionDirs.pop();
      if (!dir) break;
      rmSync(dir, { recursive: true, force: true });
    }
  });

  // Stage a throwaway cert + key on disk so the existence check
  // passes. The contents are not parsed by the validation path
  // under test; the goal is only to exercise the precondition.
  // Security: the key file is chmod 0600 and the staging dir is
  // chmod 0700 so a leaked dir never carries a world-readable
  // private key nor is traversable by other users on a shared host.
  function stageFakeCertKey(): { certPath: string; keyPath: string } {
    const dir = mkdtempSync(join(tmpdir(), "flowai-pg-tls-precond-"));
    chmodSync(dir, 0o700);
    const certPath = pathResolve(dir, "fake.crt");
    const keyPath = pathResolve(dir, "fake.key");
    writeFileSync(
      certPath,
      "-----BEGIN CERTIFICATE-----\nfake\n-----END CERTIFICATE-----\n",
    );
    writeFileSync(
      keyPath,
      "-----BEGIN PRIVATE KEY-----\nfake\n-----END PRIVATE KEY-----\n",
      { mode: 0o600 },
    );
    // Regression net: explicit chmod in case a future change drops
    // the { mode: 0o600 } option on writeFileSync above.
    chmodSync(keyPath, 0o600);
    stagedPreconditionDirs.push(dir);
    return { certPath, keyPath };
  }

  test("staged precondition dir is owner-only (0700) and key is owner-only (0600); cert is world-readable", () => {
    const { certPath, keyPath } = stageFakeCertKey();
    const dir = pathResolve(keyPath, "..");
    const dirMode = statSync(dir).mode & 0o777;
    const keyMode = statSync(keyPath).mode & 0o777;
    const certMode = statSync(certPath).mode & 0o777;
    expect(dirMode, "staging dir is owner-only (0700)").toBe(0o700);
    expect(keyMode, "staged key is owner-only (0600)").toBe(0o600);
    expect(certMode & 0o004, "staged cert remains world-readable").not.toBe(0);
  });

  test("cert path alone (no key) is rejected with a paired-mode error", async () => {
    const { certPath } = stageFakeCertKey();
    let captured: unknown;
    try {
      await StartPostgresContainer({ tlsPostgresCertPath: certPath });
    } catch (err) {
      captured = err;
    }
    expect(captured).toBeDefined();
    expect((captured as Error).message).toMatch(
      /tlsPostgresCertPath and tlsPostgresKeyPath must be supplied together/,
    );
  });

  test("key path alone (no cert) is rejected with a paired-mode error", async () => {
    const { keyPath } = stageFakeCertKey();
    let captured: unknown;
    try {
      await StartPostgresContainer({ tlsPostgresKeyPath: keyPath });
    } catch (err) {
      captured = err;
    }
    expect(captured).toBeDefined();
    expect((captured as Error).message).toMatch(
      /tlsPostgresCertPath and tlsPostgresKeyPath must be supplied together/,
    );
  });

  test("missing cert file on disk is rejected before any container is created", async () => {
    const { keyPath } = stageFakeCertKey();
    let captured: unknown;
    try {
      await StartPostgresContainer({
        tlsPostgresCertPath: "/tmp/flowai-pg-tls-does-not-exist-cert.pem",
        tlsPostgresKeyPath: keyPath,
      });
    } catch (err) {
      captured = err;
    }
    expect(captured).toBeDefined();
    expect((captured as Error).message).toMatch(
      /tlsPostgresCertPath does not exist on disk/,
    );
  });

  test("missing key file on disk is rejected before any container is created", async () => {
    const { certPath } = stageFakeCertKey();
    let captured: unknown;
    try {
      await StartPostgresContainer({
        tlsPostgresCertPath: certPath,
        tlsPostgresKeyPath: "/tmp/flowai-pg-tls-does-not-exist-key.pem",
      });
    } catch (err) {
      captured = err;
    }
    expect(captured).toBeDefined();
    expect((captured as Error).message).toMatch(
      /tlsPostgresKeyPath does not exist on disk/,
    );
  });
});
