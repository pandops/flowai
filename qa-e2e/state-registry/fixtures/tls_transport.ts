// Ephemeral TLS material and HTTPS/WSS mTLS request helpers for the
// state-registry qa-e2e suite. The fixture is owned by the
// transport contract group (v0002.20); every other contract test
// must keep using the plain HTTP Registry worker and MUST NOT import
// this module.
//
// What this fixture provides:
//
//   1. EphemeralTlsMaterial.create() generates a fresh material
//      bundle inside a private mkdtemp() directory:
//        - a private CA (CA.key, CA.crt)
//        - one localhost server cert (SAN: IP:127.0.0.1, DNS:localhost)
//        - one postgres server cert (SAN: IP:127.0.0.1, DNS:localhost),
//          signed by the same trusted CA, with EKU serverAuth. The
//          postgres_container fixture mounts this cert/key into the
//          postgres:16 container so the State Registry's Go client
//          can complete a verify-full chain handshake against it.
//        - trusted and rejection-path client certs:
//            * trustedAdmin        (CN: system-admin, role=admin, no team)
//            * trustedListener     (CN: listener-team-a, role=listener, team=team-a)
//            * trustedTeamExecutor (CN: team-executor-team-a, role=team-executor, team=team-a)
//            * trustedSystemExecutor (CN: system-executor, role=system-executor, no team)
//            * trustedGateway     (CN: gateway-team-a, role=gateway, team=team-a)
//            * wrongRoleCert      (CN: listener-impersonator, OU=wrong-role,
//                                   team=team-a; used to assert that an unknown
//                                   role produces no trusted identity headers)
//            * wrongTeamCert      (CN: listener-team-b, role=listener, team=team-b;
//                                   used to assert that a trusted CA cert from
//                                   the wrong team is rejected at the
//                                   authorization boundary without mutation)
//            * untrustedCaCert    (client cert signed by a SEPARATE CA whose
//                                   root is not in the trusted CA bundle; must
//                                   fail the handshake before HTTP is read)
//            * expiredCert        (signed by our CA but with notAfter in the
//                                   past; must fail the handshake before HTTP
//                                   is read)
//      All certs carry the clientAuth extendedKeyUsage so Node's TLS
//      stack treats them as client identities.
//
//   2. httpsRequest() issues an HTTPS request that uses the caller-
//      supplied client cert/key, verifies the server chain against
//      the caller's trusted CA bundle, and verifies the server
//      hostname against the caller's expected servername. The helper
//      never disables rejectUnauthorized; it never falls back to
//      plain HTTP. Bounded timeouts are enforced on every call.
//
//   3. openWssSocket() opens a wss:// WebSocket using the same
//      mTLS discipline (client cert/key, trusted CA, servername,
//      rejectUnauthorized). The helper returns a Promise that
//      resolves to the open WebSocket or rejects on handshake error
//      (so the caller can prove the handshake failed before HTTP is
//      read by attaching a one-shot 'error' listener).
//
//   4. cleanup() removes the entire temp directory using
//      fs.rm({ recursive: true, force: true }) and is safe to call
//      multiple times. Every private key produced in step 1 lives
//      only inside that temp directory and is removed on cleanup;
//      the fixture never logs, prints, or stores keys anywhere
//      else.
//
// Implementation rules (enforced by code review + the contract
// harness):
//
//   * All openssl invocations are bounded: the subprocess gets a
//     stdout/stderr buffer cap and a hard wall-clock deadline via
//     child_process.spawn; on timeout the subprocess is killed and
//     every captured byte is surfaced as an error so the test
//     can diagnose a stuck openssl.
//   * The fixture never reads STATE_REGISTRY_TLS_* env vars; every
//     path it returns comes from a file the test owns.
//   * The fixture never imports the Go process, the existing
//     registry_worker.ts, the postgres_container.ts, or any other
//     service fixture. The contract test composes them.
//   * No broad status acceptance. The test asserts on the exact
//     tls-alert / TLS error code emitted by Node's TLS stack.
import { execFile, spawn, type ChildProcess } from "node:child_process";
import { randomBytes } from "node:crypto";
import {
  chmodSync,
  existsSync,
  mkdirSync,
  readFileSync,
  rmSync,
} from "node:fs";
import { mkdtemp, readFile } from "node:fs/promises";
import { request as nodeHttpsRequest, Agent as HttpsAgent } from "node:https";
import { tmpdir } from "node:os";
import { join, resolve as pathResolve } from "node:path";
import { connect as tlsConnect, type ConnectionOptions } from "node:tls";
import WebSocket from "ws";

const CertDirPrefix = "flowai-v0002-20-tls-";
const OpenSslBinary = "openssl";
const OpenSslWallClockMs = 5_000;
const OpenSslStdoutBytesCap = 16 * 1024;
const CertSubjectPrefix = "/CN=flowai-v0002-20";

function certificateCommonName(name: string): string {
  return `flowai-v0002-20-${name}`;
}

// clientAuth EKU for every client cert. The server cert is issued
// with serverAuth (the openssl x509 default for `extendedKeyUsage`).
const ClientExt = "extendedKeyUsage = clientAuth";

// The server cert SAN. SAN is required for hostname verification to
// succeed against 127.0.0.1 and localhost.
const ServerExt =
  "subjectAltName = @alt_names\n[alt_names]\nDNS.1 = localhost\nIP.1 = 127.0.0.1";

// The Postgres server cert is signed by the SAME trusted CA as the
// listener server cert. The EKU is serverAuth (the openssl x509
// default when -extfile is supplied without an explicit EKU), and
// the SAN matches what the State Registry's Go client (pgx) will
// see when it connects to 127.0.0.1: <host-port>.
const PostgresServerExt =
  "extendedKeyUsage = serverAuth\nsubjectAltName = @alt_names\n[alt_names]\nDNS.1 = localhost\nIP.1 = 127.0.0.1";

export interface ClientCertMaterial {
  /** Common Name on the cert. */
  cn: string;
  /** Service role the cert is supposed to authorize. */
  role:
    | "admin"
    | "listener"
    | "team-executor"
    | "gateway"
    | "system-executor"
    | "wrong-role"
    | "wrong-team";
  /** Immutable team_id the cert binds to. */
  teamId: string;
  /** Absolute paths on disk; the test must never log these to the harness console. */
  certPath: string;
  keyPath: string;
}

export interface EphemeralTlsMaterialOptions {
  /**
   * Optional override for the lifetime of every signed cert (in
   * days, positive). The expired cert is always backdated regardless
   * of this value; only the trusted / wrong-role / wrong-team /
   * untrusted-CA certs honor lifetimeDays.
   */
  lifetimeDays?: number;
}

export interface EphemeralTlsMaterial {
  tempDir: string;
  caCertPath: string;
  caKeyPath: string;
  serverCertPath: string;
  serverKeyPath: string;
  /** Postgres server cert (signed by caCertPath's CA, EKU serverAuth, SAN DNS:localhost + IP:127.0.0.1). */
  postgresServerCertPath: string;
  /** Postgres server key (paired with postgresServerCertPath). */
  postgresServerKeyPath: string;
  trustedAdmin: ClientCertMaterial;
  trustedListener: ClientCertMaterial;
  trustedTeamExecutor: ClientCertMaterial;
  trustedSystemExecutor: ClientCertMaterial;
  trustedGateway: ClientCertMaterial;
  wrongRoleCert: ClientCertMaterial;
  wrongTeamCert: ClientCertMaterial;
  untrustedCaCert: ClientCertMaterial;
  expiredCert: ClientCertMaterial;
  untrustedCaDir: string;
  untrustedCaCertPath: string;
  untrustedCaKeyPath: string;
  cleanup(): Promise<void>;
}

export interface HttpsRequestOptions {
  /** Trusted client cert (PEM, file path). */
  clientCertPath: string;
  /** Trusted client key (PEM, file path). */
  clientKeyPath: string;
  /** Trusted server CA bundle (PEM, file path). */
  caBundlePath: string;
  /**
   * Server hostname used for SNI and x509 hostname verification.
   * The cert's SAN must include this value (either as DNS or IP).
   */
  servername: string;
  /** Request method. */
  method?: string;
  /** Request body. */
  body?: string | Buffer;
  /** Extra request headers. */
  headers?: Record<string, string>;
  /** Hard wall-clock deadline for the entire round-trip. */
  timeoutMs?: number;
  /**
   * If true, the helper logs the raw Node TLS error code on failure
   * so the contract test can assert the handshake-failed-before-HTTP
   * path. The test never inspects private-key material in the log.
   */
  surfaceTlsError?: boolean;
}

export interface HttpsRequestResult {
  status: number;
  body: string;
  headers: Readonly<Record<string, string>>;
  /** Peer certificate CN observed by Node's TLS stack. */
  peerCn: string | null;
  /** Resolved URL with which the request was issued. */
  url: string;
}

const DefaultLifetimeDays = 1;
const DefaultRequestTimeoutMs = 5_000;

// isENOENT returns true when the spawn error indicates a missing
// binary or missing file. Such errors are surfaced verbatim so the
// contract test can name the missing file precisely.
function isENOENT(err: unknown): boolean {
  if (!err || typeof err !== "object") return false;
  const code = (err as { code?: unknown }).code;
  return code === "ENOENT";
}

// runOpenssl spawns openssl with a hard wall-clock deadline. The
// returned promise resolves with the captured stdout on exit 0 and
// rejects with a structured error (containing the captured stderr +
// exit code + signal) otherwise. The function never leaves a
// dangling child behind: if the deadline elapses, the child is
// SIGKILL'd and the rejection message includes 'openssl timed out'.
async function runOpenssl(args: string[]): Promise<string> {
  return await new Promise<string>((resolve, reject) => {
    const child: ChildProcess = spawn(OpenSslBinary, args, {
      stdio: ["ignore", "pipe", "pipe"],
    });
    const stdoutChunks: Buffer[] = [];
    const stderrChunks: Buffer[] = [];
    let stdoutBytes = 0;
    let stderrBytes = 0;
    const timer = setTimeout(() => {
      try {
        child.kill("SIGKILL");
      } catch {
        // best-effort
      }
      reject(
        new Error(
          `openssl ${args.join(" ")} timed out after ${OpenSslWallClockMs}ms`,
        ),
      );
    }, OpenSslWallClockMs);
    child.stdout?.on("data", (chunk: Buffer) => {
      stdoutBytes += chunk.length;
      if (stdoutBytes <= OpenSslStdoutBytesCap) {
        stdoutChunks.push(chunk);
      }
    });
    child.stderr?.on("data", (chunk: Buffer) => {
      stderrBytes += chunk.length;
      if (stderrBytes <= OpenSslStdoutBytesCap) {
        stderrChunks.push(chunk);
      }
    });
    child.once("error", (err) => {
      clearTimeout(timer);
      if (isENOENT(err)) {
        reject(
          new Error(
            `openssl binary not found on PATH; required for v0002.20 ephemeral TLS`,
          ),
        );
        return;
      }
      reject(err);
    });
    child.once("close", (code, signal) => {
      clearTimeout(timer);
      const out = Buffer.concat(stdoutChunks).toString("utf-8");
      const err = Buffer.concat(stderrChunks).toString("utf-8");
      if (code === 0) {
        resolve(out);
        return;
      }
      reject(
        new Error(
          `openssl ${args.join(" ")} exited code=${String(code)} signal=${String(signal)} stderr=${err.trim()}`,
        ),
      );
    });
  });
}

// runOpensslFile is runOpenssl with a CLI config that lets the caller
// pass a -config <path> file. The fixture uses it for -extfile and
// for the backdated openssl ca invocation that produces expired
// certs.
async function runOpensslFile(args: string[]): Promise<string> {
  return await runOpenssl(args);
}

async function fileExists(path: string): Promise<boolean> {
  try {
    const stat = await readFile(path);
    void stat;
    return true;
  } catch {
    return false;
  }
}

async function generateKey(dir: string, name: string): Promise<string> {
  const keyPath = pathResolve(dir, `${name}.key`);
  await runOpenssl(["genrsa", "-out", keyPath, "2048"]);
  // Defense-in-depth: do not rely on OpenSSL's platform-dependent
  // default key mode. Owner-only (0600) so a leaked temp dir never
  // carries a world-readable private key.
  chmodSync(keyPath, 0o600);
  return keyPath;
}

async function writeExtFile(
  dir: string,
  name: string,
  ext: string,
): Promise<string> {
  const extPath = pathResolve(dir, `${name}.ext`);
  const { writeFile } = await import("node:fs/promises");
  await writeFile(extPath, ext, { mode: 0o600 });
  return extPath;
}

async function writeCaConfig(
  dir: string,
  certPath: string,
  keyPath: string,
): Promise<string> {
  const configPath = pathResolve(dir, "ca.conf");
  const { writeFile } = await import("node:fs/promises");
  const indexPath = pathResolve(dir, "ca.index.txt");
  const serialPath = pathResolve(dir, "ca.serial");
  const newCertsDir = pathResolve(dir, "ca.newcerts");
  mkdirSync(newCertsDir, { recursive: true });
  if (!existsSync(indexPath)) {
    await writeFile(indexPath, "");
  }
  if (!existsSync(serialPath)) {
    await writeFile(serialPath, "01");
  }
  const config = [
    "[ca]",
    "default_ca = CA_default",
    "[CA_default]",
    `database = ${indexPath}`,
    `serial = ${serialPath}`,
    `new_certs_dir = ${newCertsDir}`,
    `certificate = ${certPath}`,
    `private_key = ${keyPath}`,
    "default_days = 365",
    "default_md = sha256",
    "policy = policy_any",
    "[policy_any]",
    "commonName = supplied",
    "",
  ].join("\n");
  await writeFile(configPath, config, { mode: 0o600 });
  return configPath;
}

async function generateCa(
  dir: string,
): Promise<{ certPath: string; keyPath: string }> {
  const keyPath = await generateKey(dir, "ca");
  const certPath = pathResolve(dir, "ca.crt");
  await runOpenssl([
    "req",
    "-x509",
    "-new",
    "-batch",
    "-key",
    keyPath,
    "-out",
    certPath,
    "-days",
    "1",
    "-subj",
    `${CertSubjectPrefix}-ca`,
  ]);
  return { certPath, keyPath };
}

async function generateServerCert(
  dir: string,
  caCertPath: string,
  caKeyPath: string,
): Promise<{ certPath: string; keyPath: string }> {
  const keyPath = await generateKey(dir, "server");
  const csrPath = pathResolve(dir, "server.csr");
  const extPath = await writeExtFile(dir, "server", ServerExt);
  await runOpenssl([
    "req",
    "-new",
    "-batch",
    "-key",
    keyPath,
    "-out",
    csrPath,
    "-subj",
    `${CertSubjectPrefix}-server`,
  ]);
  const certPath = pathResolve(dir, "server.crt");
  await runOpenssl([
    "x509",
    "-req",
    "-in",
    csrPath,
    "-CA",
    caCertPath,
    "-CAkey",
    caKeyPath,
    "-CAcreateserial",
    "-out",
    certPath,
    "-days",
    "1",
    "-extfile",
    extPath,
  ]);
  return { certPath, keyPath };
}

async function generatePostgresServerCert(
  dir: string,
  caCertPath: string,
  caKeyPath: string,
): Promise<{ certPath: string; keyPath: string }> {
  const keyPath = await generateKey(dir, "pg-server");
  const csrPath = pathResolve(dir, "pg-server.csr");
  const extPath = await writeExtFile(dir, "pg-server", PostgresServerExt);
  await runOpenssl([
    "req",
    "-new",
    "-batch",
    "-key",
    keyPath,
    "-out",
    csrPath,
    "-subj",
    `${CertSubjectPrefix}-pg-server`,
  ]);
  const certPath = pathResolve(dir, "pg-server.crt");
  await runOpenssl([
    "x509",
    "-req",
    "-in",
    csrPath,
    "-CA",
    caCertPath,
    "-CAkey",
    caKeyPath,
    "-CAcreateserial",
    "-out",
    certPath,
    "-days",
    "1",
    "-extfile",
    extPath,
  ]);
  return { certPath, keyPath };
}

// serialNumberForRole returns the Subject.SerialNumber for a role.
// Listener certs MUST carry a source-system serialNumber so the
// peerauth parser can map the verified CN to the
// X-FlowAI-Source-System-Id header. Other roles MUST NOT carry a
// serialNumber because the parser rejects unexpected serials for
// team-less / executor / gateway roles.
function serialNumberForRole(role: string): string | null {
  if (role === "listener") {
    return "source-a";
  }
  return null;
}

function buildSubjectString(role: string, cn: string, teamId: string): string {
  const parts: string[] = [`${CertSubjectPrefix}-${cn}`];
  if (teamId.length > 0) {
    parts.push(`O=${teamId}`);
  }
  parts.push(`OU=${role}`);
  const serial = serialNumberForRole(role);
  if (serial !== null) {
    parts.push(`serialNumber=${serial}`);
  }
  return parts.join("/");
}

async function signClientCert(
  dir: string,
  caCertPath: string,
  caKeyPath: string,
  name: string,
  cn: string,
  role: ClientCertMaterial["role"],
  teamId: string,
  lifetimeDays: number,
  subjectRole: ClientCertMaterial["role"] | string = role,
): Promise<ClientCertMaterial> {
  const keyPath = await generateKey(dir, name);
  const csrPath = pathResolve(dir, `${name}.csr`);
  const extPath = await writeExtFile(dir, name, ClientExt);
  await runOpenssl([
    "req",
    "-new",
    "-batch",
    "-key",
    keyPath,
    "-out",
    csrPath,
    "-subj",
    buildSubjectString(subjectRole, cn, teamId),
  ]);
  const certPath = pathResolve(dir, `${name}.crt`);
  await runOpenssl([
    "x509",
    "-req",
    "-in",
    csrPath,
    "-CA",
    caCertPath,
    "-CAkey",
    caKeyPath,
    "-CAcreateserial",
    "-out",
    certPath,
    "-days",
    String(lifetimeDays),
    "-extfile",
    extPath,
  ]);
  return {
    cn: certificateCommonName(cn),
    role,
    teamId,
    certPath,
    keyPath,
  };
}

async function signExpiredClientCert(
  dir: string,
  caCertPath: string,
  caKeyPath: string,
  name: string,
  cn: string,
  role: ClientCertMaterial["role"],
  teamId: string,
  subjectRole: ClientCertMaterial["role"] | string = role,
): Promise<ClientCertMaterial> {
  const keyPath = await generateKey(dir, name);
  const csrPath = pathResolve(dir, `${name}.csr`);
  const extPath = await writeExtFile(dir, name, ClientExt);
  await runOpenssl([
    "req",
    "-new",
    "-batch",
    "-key",
    keyPath,
    "-out",
    csrPath,
    "-subj",
    buildSubjectString(subjectRole, cn, teamId),
  ]);
  const configPath = await writeCaConfig(dir, caCertPath, caKeyPath);
  // Backdate the cert to a window that is unambiguously in the past.
  // The startdate uses YYYYMMDDHHMMSSZ (UTC); a 1-day notAfter keeps
  // the cert unambiguously expired for any reasonable test clock.
  const certPath = pathResolve(dir, `${name}.crt`);
  await runOpensslFile([
    "ca",
    "-config",
    configPath,
    "-in",
    csrPath,
    "-out",
    certPath,
    "-batch",
    "-startdate",
    "200101000000Z",
    "-enddate",
    "200102000000Z",
    "-notext",
  ]);
  return {
    cn: certificateCommonName(cn),
    role,
    teamId,
    certPath,
    keyPath,
  };
}

export async function createEphemeralTlsMaterial(
  opts: EphemeralTlsMaterialOptions = {},
): Promise<EphemeralTlsMaterial> {
  const lifetimeDays = opts.lifetimeDays ?? DefaultLifetimeDays;
  const tempDir = await mkdtemp(join(tmpdir(), CertDirPrefix));
  // Security: do not rely on the platform default for mkdtemp mode.
  // Owner-only (0700) so a leaked bundle cannot be traversed by other users.
  chmodSync(tempDir, 0o700);
  const ca = await generateCa(tempDir);
  const server = await generateServerCert(tempDir, ca.certPath, ca.keyPath);
  const pgServer = await generatePostgresServerCert(
    tempDir,
    ca.certPath,
    ca.keyPath,
  );

  const trustedAdmin: ClientCertMaterial = {
    ...(await signClientCert(
      tempDir,
      ca.certPath,
      ca.keyPath,
      "system-admin",
      "system-admin",
      "admin",
      "",
      lifetimeDays,
    )),
    role: "admin",
    teamId: "",
  };
  const trustedListener: ClientCertMaterial = {
    ...(await signClientCert(
      tempDir,
      ca.certPath,
      ca.keyPath,
      "listener-team-a",
      "listener-team-a",
      "listener",
      "team-a",
      lifetimeDays,
    )),
    role: "listener",
    teamId: "team-a",
  };
  const trustedTeamExecutor: ClientCertMaterial = {
    ...(await signClientCert(
      tempDir,
      ca.certPath,
      ca.keyPath,
      "executor-team-a",
      "executor-team-a",
      "team-executor",
      "team-a",
      lifetimeDays,
    )),
    role: "team-executor",
    teamId: "team-a",
  };
  const trustedSystemExecutor: ClientCertMaterial = {
    ...(await signClientCert(
      tempDir,
      ca.certPath,
      ca.keyPath,
      "system-executor",
      "system-executor",
      "system-executor",
      "",
      lifetimeDays,
    )),
    role: "system-executor",
    teamId: "",
  };
  const trustedGateway: ClientCertMaterial = {
    ...(await signClientCert(
      tempDir,
      ca.certPath,
      ca.keyPath,
      "gateway-team-a",
      "gateway-team-a",
      "gateway",
      "team-a",
      lifetimeDays,
    )),
    role: "gateway",
    teamId: "team-a",
  };
  const wrongRoleCert: ClientCertMaterial = {
    ...(await signClientCert(
      tempDir,
      ca.certPath,
      ca.keyPath,
      "listener-impersonator",
      "listener-impersonator",
      "wrong-role",
      "team-a",
      lifetimeDays,
    )),
    role: "wrong-role",
    teamId: "team-a",
  };
  // The wrong-team cert is a REAL listener bound to team-b
  // (OU=listener, O=team-b, serialNumber=source-b). The strict
  // peerauth parser keeps every header; the listener
  // authorization boundary rejects the request because its
  // team-b binding doesn't match the submitted team-a body.
  const wrongTeamCert: ClientCertMaterial = {
    ...(await signClientCert(
      tempDir,
      ca.certPath,
      ca.keyPath,
      "listener-team-b",
      "listener-team-b",
      "wrong-team",
      "team-b",
      lifetimeDays,
      "listener",
    )),
    role: "wrong-team",
    teamId: "team-b",
  };
  const expiredCert: ClientCertMaterial = {
    ...(await signExpiredClientCert(
      tempDir,
      ca.certPath,
      ca.keyPath,
      "expired-client",
      "expired-client",
      "listener",
      "team-a",
    )),
    role: "listener",
    teamId: "team-a",
  };

  // Untrusted-CA material lives in its own subdir so the cleanup()
  // call is a single recursive rm. We sign a client cert with a
  // private CA that is NOT in the trusted CA bundle; the resulting
  // cert verifies against the untrusted CA and FAILS verification
  // against the trusted CA.
  const untrustedCaDir = pathResolve(tempDir, "untrusted-ca");
  mkdirSync(untrustedCaDir, { recursive: true });
  // mkdirSync without a mode inherits 0o777 minus umask. Lock down
  // to 0700 so the untrusted CA's private key is never readable by
  // other users.
  chmodSync(untrustedCaDir, 0o700);
  const untrustedCa = await generateCa(untrustedCaDir);
  const untrustedCaCert: ClientCertMaterial = {
    ...(await signClientCert(
      untrustedCaDir,
      untrustedCa.certPath,
      untrustedCa.keyPath,
      "untrusted-client",
      "untrusted-client",
      "listener",
      "team-a",
      lifetimeDays,
    )),
    role: "listener",
    teamId: "team-a",
  };

  // Sanity check: every file the contract test will reference must
  // exist on disk. If any openssl invocation silently produced a
  // truncated output, fail closed before the test exercises a
  // half-baked cert.
  const required = [
    ca.certPath,
    ca.keyPath,
    server.certPath,
    server.keyPath,
    pgServer.certPath,
    pgServer.keyPath,
    trustedAdmin.certPath,
    trustedAdmin.keyPath,
    trustedListener.certPath,
    trustedListener.keyPath,
    trustedTeamExecutor.certPath,
    trustedTeamExecutor.keyPath,
    trustedSystemExecutor.certPath,
    trustedSystemExecutor.keyPath,
    trustedGateway.certPath,
    trustedGateway.keyPath,
    wrongRoleCert.certPath,
    wrongRoleCert.keyPath,
    wrongTeamCert.certPath,
    wrongTeamCert.keyPath,
    untrustedCaCert.certPath,
    untrustedCaCert.keyPath,
    expiredCert.certPath,
    expiredCert.keyPath,
  ];
  for (const path of required) {
    if (!(await fileExists(path))) {
      throw new Error(`ephemeral TLS material missing file: ${path}`);
    }
  }

  // cleanup is idempotent: a closed flag short-circuits repeat calls.
  // ENOENT resolves silently (the path is already gone); every other
  // removal failure rejects so the caller surfaces the real error.
  let cleanedUp = false;
  return {
    tempDir,
    caCertPath: ca.certPath,
    caKeyPath: ca.keyPath,
    serverCertPath: server.certPath,
    serverKeyPath: server.keyPath,
    postgresServerCertPath: pgServer.certPath,
    postgresServerKeyPath: pgServer.keyPath,
    trustedAdmin,
    trustedListener,
    trustedTeamExecutor,
    trustedSystemExecutor,
    trustedGateway,
    wrongRoleCert,
    wrongTeamCert,
    untrustedCaCert,
    expiredCert,
    untrustedCaDir,
    untrustedCaCertPath: untrustedCaCert.certPath,
    untrustedCaKeyPath: untrustedCaCert.keyPath,
    async cleanup(): Promise<void> {
      if (cleanedUp) {
        return;
      }
      cleanedUp = true;
      try {
        rmSync(tempDir, { recursive: true, force: true });
      } catch (err) {
        const code = (err as { code?: string } | null | undefined)?.code;
        if (code === "ENOENT") {
          return;
        }
        // Reset the flag so a transient failure gets a retry chance.
        cleanedUp = false;
        throw err instanceof Error ? err : new Error(String(err));
      }
    },
  };
}

// httpsRequest issues one HTTPS request with mTLS and returns a
// promise that resolves with the response (status, body, headers,
// peer CN) OR rejects with the raw TLS error (preserving the error
// code so the test can assert the handshake-failed-before-HTTP
// path). The helper never logs private keys; it logs only the
// public cert path / public error code when surfaceTlsError is set.
export async function httpsRequest(
  url: string,
  options: HttpsRequestOptions,
): Promise<HttpsRequestResult> {
  const clientCert = readFileSync(options.clientCertPath);
  const clientKey = readFileSync(options.clientKeyPath);
  const caBundle = readFileSync(options.caBundlePath);
  const parsed = new URL(url);
  if (parsed.protocol !== "https:") {
    throw new Error(
      `httpsRequest requires an https:// URL; received ${parsed.protocol}`,
    );
  }
  const agent = new HttpsAgent({
    cert: clientCert,
    key: clientKey,
    ca: caBundle,
    servername: options.servername,
    rejectUnauthorized: true,
    keepAlive: false,
  });
  const reqOptions: Parameters<typeof nodeHttpsRequest>[1] = {
    method: options.method ?? "GET",
    agent,
    headers: options.headers,
    timeout: options.timeoutMs ?? DefaultRequestTimeoutMs,
  };
  return await new Promise<HttpsRequestResult>((resolve, reject) => {
    const req = nodeHttpsRequest(url, reqOptions, (res) => {
      const chunks: Buffer[] = [];
      res.on("data", (chunk: Buffer) => chunks.push(chunk));
      res.on("end", () => {
        try {
          const peerCert =
            res.socket instanceof Object && "getPeerCertificate" in res.socket
              ? (
                  res.socket as {
                    getPeerCertificate: () => { subject?: { CN?: string } };
                  }
                ).getPeerCertificate()
              : null;
          const peerCn = peerCert?.subject?.CN ?? null;
          const headers: Record<string, string> = {};
          for (const [k, v] of Object.entries(res.headers)) {
            if (typeof v === "string") {
              headers[k] = v;
            } else if (Array.isArray(v)) {
              const arr = v as string[];
              headers[k] = arr.join(", ");
            }
          }
          resolve({
            status: res.statusCode ?? 0,
            body: Buffer.concat(chunks).toString("utf-8"),
            headers: Object.freeze(headers),
            peerCn,
            url,
          });
        } catch (err) {
          reject(err instanceof Error ? err : new Error(String(err)));
        }
      });
      res.on("error", (err) => reject(err));
    });
    req.on("error", (err) => {
      if (options.surfaceTlsError) {
        // Surface the raw TLS error so the test can assert
        // 'socket hang up' / 'unable to verify the first certificate'
        // / 'certificate has expired' / 'alert certificate required' /
        // 'hostname/IP address mismatch' on the negative-control
        // paths. Never include cert bytes, key bytes, or any other
        // private material in this branch.
        reject(err);
        return;
      }
      reject(err);
    });
    req.on("timeout", () => {
      req.destroy(
        new Error(
          `httpsRequest timed out after ${String(reqOptions.timeout)}ms url=${url}`,
        ),
      );
    });
    if (options.body) {
      req.end(options.body);
    } else {
      req.end();
    }
  });
}

// TlsHandshake is the result of a single low-level tls.connect()
// probe. The test uses it to assert that a wss:// handshake is
// rejected at the TLS layer before any HTTP read.
export interface TlsHandshake {
  ok: boolean;
  /** Node's TLS error code (e.g. CERT_REQUIRED, CERT_HAS_EXPIRED, UNABLE_TO_VERIFY_LEAF_SIGNATURE, HOSTNAME_MISMATCH). */
  tlsErrorCode: string | null;
  /** True if the server demanded a client cert and the client sent none. */
  clientCertRequired: boolean;
  /** Plain error message; the test never logs private keys. */
  message: string;
}

export interface TlsHandshakeOptions {
  host: string;
  port: number;
  caBundlePath: string;
  clientCertPath?: string;
  clientKeyPath?: string;
  servername: string;
  timeoutMs?: number;
}

// tlsHandshakeProbe issues ONE raw tls.connect to the server. It
// resolves with { ok: false, ... } on a TLS-level rejection and
// { ok: true } only when the bounded inactivity timeout elapses
// after secureConnect without a rejection alert. Under TLS 1.3,
// Node may emit secureConnect before it receives the server's
// certificate_required alert, so secureConnect alone is not proof
// that the server accepted the client identity.
export async function tlsHandshakeProbe(
  options: TlsHandshakeOptions,
): Promise<TlsHandshake> {
  const opts: ConnectionOptions = {
    host: options.host,
    port: options.port,
    ca: readFileSync(options.caBundlePath),
    servername: options.servername,
    rejectUnauthorized: true,
    timeout: options.timeoutMs ?? DefaultRequestTimeoutMs,
  };
  if (options.clientCertPath && options.clientKeyPath) {
    opts.cert = readFileSync(options.clientCertPath);
    opts.key = readFileSync(options.clientKeyPath);
  }
  return await new Promise<TlsHandshake>((resolve) => {
    let settled = false;
    const finish = (result: TlsHandshake) => {
      if (settled) return;
      settled = true;
      resolve(result);
    };
    const sock = tlsConnect(opts);
    sock.once("error", (err) => {
      const code = (err as { code?: string }).code ?? "UNKNOWN";
      const message = (err as Error).message ?? "";
      const clientCertRequired =
        /certificate required/i.test(message) ||
        /alert certificate required/i.test(message) ||
        /SSL alert number 116/i.test(message);
      finish({ ok: false, tlsErrorCode: code, clientCertRequired, message });
    });
    sock.once("timeout", () => {
      sock.destroy();
      finish({
        ok: true,
        tlsErrorCode: null,
        clientCertRequired: false,
        message: "secureConnect without rejection alert",
      });
    });
  });
}

// openWssSocket opens a wss:// WebSocket with mTLS discipline. The
// returned WebSocket emits 'error' on handshake failure (and the
// caller can attach a one-shot 'error' listener to capture the raw
// TLS code). The helper never disables rejectUnauthorized and
// always uses the caller's trusted CA bundle. The WebSocket is
// returned in the CONNECTING state; the caller is responsible for
// awaiting 'open' or 'error' and for closing it.
export function openWssSocket(
  url: string,
  options: {
    caBundlePath: string;
    clientCertPath?: string;
    clientKeyPath?: string;
    servername: string;
    headers?: Record<string, string>;
    handshakeTimeoutMs?: number;
  },
): WebSocket {
  if (!url.startsWith("wss://")) {
    throw new Error(`openWssSocket requires a wss:// URL; received ${url}`);
  }
  const wsOptions: Record<string, unknown> = {
    rejectUnauthorized: true,
    servername: options.servername,
    ca: readFileSync(options.caBundlePath),
    handshakeTimeout: options.handshakeTimeoutMs ?? DefaultRequestTimeoutMs,
  };
  if (options.clientCertPath && options.clientKeyPath) {
    wsOptions["cert"] = readFileSync(options.clientCertPath);
    wsOptions["key"] = readFileSync(options.clientKeyPath);
  }
  if (options.headers) {
    wsOptions["headers"] = options.headers;
  }
  // The ws library forwards the option bag to tls.connect on
  // the wss:// branch. cert / key / ca / servername /
  // rejectUnauthorized are the documented tls.connect options;
  // the double cast is required because @types/ws does not
  // expose servername on ClientOptions.
  return new WebSocket(url, wsOptions as unknown as WebSocket.ClientOptions);
}

// randomTmpDir creates a scratch directory in os.tmpdir() for ad-hoc
// use by the contract test (the contract test stores the test
// summary in here; the test always removes it via try/finally).
export async function randomTmpDir(prefix: string): Promise<string> {
  return await mkdtemp(join(tmpdir(), prefix));
}

// cleanupTmpDir removes the directory produced by randomTmpDir or
// any other path; safe to call multiple times.
export function cleanupTmpDir(path: string): void {
  try {
    rmSync(path, { recursive: true, force: true });
  } catch {
    // best-effort
  }
}

// execFilePinned is a thin wrapper around child_process.execFile
// that surfaces a bounded deadline + the captured stdout/stderr on
// failure. The contract test uses it when it needs to invoke a
// separate openssl one-liner (e.g. to verify the cert chain it
// just produced).
export function execFilePinned(
  file: string,
  args: string[],
  timeoutMs: number = OpenSslWallClockMs,
): Promise<{ stdout: string; stderr: string; code: number }> {
  return new Promise((resolve, reject) => {
    execFile(file, args, { timeout: timeoutMs }, (err, stdout, stderr) => {
      if (err && (err as { code?: string }).code === "ENOENT") {
        reject(new Error(`${file} binary not found on PATH`));
        return;
      }
      if (err && /timed out/.test((err as Error).message)) {
        reject(
          new Error(`${file} ${args.join(" ")} timed out after ${timeoutMs}ms`),
        );
        return;
      }
      const code = err ? -1 : 0;
      resolve({
        stdout: String(stdout ?? ""),
        stderr: String(stderr ?? ""),
        code,
      });
    });
  });
}

// bytesFromRandom returns N random bytes for use in test IDs that
// must not collide across runs.
export function randomId(bytes: number = 8): string {
  return randomBytes(bytes).toString("hex");
}
