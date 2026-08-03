// Shared redactor for fixture logs, errors, and child-process
// stdout/stderr streams. Every secret-bearing token MUST pass through
// sanitize() before reaching the harness console, captured log
// buffer, or thrown error. The sanitizer runs only on text already
// captured; it never logs the underlying secrets.
const AesKeyHexPattern = /STATE_REGISTRY_AES_KEY_HEX=\S+/g;
const PostgresPasswordPattern = /POSTGRES_PASSWORD=\S+/g;
const DsnPattern = /postgresql:\/\/[^\s'"]+/g;
const ContainerNamePattern = /state-registry-pg-[a-zA-Z0-9-]+/g;

// v0002.20 transport: filesystem paths that could leak through the
// State's TLS surface. The DSN redaction above drops the query
// string (which carries sslrootcert), but the path can also appear
// OUTSIDE a DSN: inside the docker/podman run command line, in a
// Go process startup log line that echoes the env var, or in a
// pgx error message that quotes the CA file path. The patterns
// below catch every place v0002.20 surfaces a TLS path so it
// never reaches the harness console.
const TlsEnvVarPattern =
  /STATE_REGISTRY_(?:POSTGRES_TLS_CA|TLS_SERVER_CERT|TLS_SERVER_KEY|TLS_CLIENT_CA)=\S+/g;
const PostgresSslRootCertPattern = /sslrootcert=\S+/g;
// Staging directory paths owned by the fixture (postgres TLS
// staging tmpdir, listener TLS material tmpdir). The container
// runtime sanitizes the full docker run command, so the staging
// path lands in the captured log buffer; the pattern keeps the
// host-side tmpdir prefix while redacting the random suffix.
const PostgresTlsStagingDirPattern = /(\/tmp\/flowai-pg-tls-)[A-Za-z0-9_-]+/g;
const ListenerTlsStagingDirPattern =
  /(\/tmp\/flowai-v0002-20-tls-)[A-Za-z0-9_-]+/g;

export function sanitize(input: string): string {
  let out = input;
  out = out.replace(AesKeyHexPattern, "STATE_REGISTRY_AES_KEY_HEX=<redacted>");
  out = out.replace(TlsEnvVarPattern, (match) => {
    const eq = match.indexOf("=");
    const name = eq >= 0 ? match.slice(0, eq) : match;
    return `${name}=<redacted>`;
  });
  out = out.replace(PostgresPasswordPattern, "POSTGRES_PASSWORD=<redacted>");
  out = out.replace(DsnPattern, "postgresql://<redacted>@<host>/<db>");
  out = out.replace(PostgresSslRootCertPattern, "sslrootcert=<redacted>");
  out = out.replace(ContainerNamePattern, "state-registry-pg-<redacted>");
  out = out.replace(PostgresTlsStagingDirPattern, "$1<redacted>");
  out = out.replace(ListenerTlsStagingDirPattern, "$1<redacted>");
  out = out.replace(/[a-f0-9]{64}/gi, "<hex64>");
  out = out.replace(
    /(^|[^a-f0-9])([a-f0-9]{32})(?![a-f0-9])/gi,
    "$1<hex-password>",
  );
  out = out.replace(/[a-f0-9]{40,}/gi, "<hex>");
  return out;
}

// truncateBounds logs and captured stderr/stdout to a fixed byte
// ceiling. The latest tail wins because startup failures emit the
// relevant signal at the end of the stream.
export function truncateTail(input: string, maxBytes: number): string {
  if (input.length <= maxBytes) {
    return input;
  }
  return input.slice(-maxBytes);
}
