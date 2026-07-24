// Shared redactor for fixture logs, errors, and child-process
// stdout/stderr streams. Every secret-bearing token MUST pass through
// sanitize() before reaching the harness console, captured log
// buffer, or thrown error. The sanitizer runs only on text already
// captured; it never logs the underlying secrets.
const AesKeyHexPattern = /STATE_REGISTRY_AES_KEY_HEX=\S+/g;
const PostgresPasswordPattern = /POSTGRES_PASSWORD=\S+/g;
const DsnPattern = /postgresql:\/\/[^\s'"]+/g;
const ContainerNamePattern = /state-registry-pg-[a-zA-Z0-9-]+/g;

export function sanitize(input: string): string {
  let out = input;
  out = out.replace(AesKeyHexPattern, 'STATE_REGISTRY_AES_KEY_HEX=<redacted>');
  out = out.replace(PostgresPasswordPattern, 'POSTGRES_PASSWORD=<redacted>');
  out = out.replace(DsnPattern, 'postgresql://<redacted>@<host>/<db>');
  out = out.replace(ContainerNamePattern, 'state-registry-pg-<redacted>');
  out = out.replace(/[a-f0-9]{64}/gi, '<hex64>');
  out = out.replace(/(^|[^a-f0-9])([a-f0-9]{32})(?![a-f0-9])/gi, '$1<hex-password>');
  out = out.replace(/[a-f0-9]{40,}/gi, '<hex>');
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