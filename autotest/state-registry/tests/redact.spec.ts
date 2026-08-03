// Tests for the secret redactor. The redactor runs over captured log
// streams and thrown errors before they reach the harness console or
// the test reports, so leaking any token at this layer breaks the
// scaffold contract.
import { test, expect } from "@playwright/test";
import { sanitize, truncateTail } from "../fixtures/redact";

test.describe("redact.sanitize", () => {
  test("replaces STATE_REGISTRY_AES_KEY_HEX tokens", () => {
    const keyHex = "STATE_REGISTRY_AES_KEY_HEX=" + "a".repeat(64);
    expect(sanitize(keyHex)).toContain("STATE_REGISTRY_AES_KEY_HEX=<redacted>");
    expect(sanitize(keyHex)).not.toContain("a".repeat(64));
  });

  test("replaces POSTGRES_PASSWORD tokens", () => {
    const envLine = "POSTGRES_PASSWORD=" + "f".repeat(32);
    expect(sanitize(envLine)).toContain("POSTGRES_PASSWORD=<redacted>");
    expect(sanitize(envLine)).not.toContain("f".repeat(32));
  });

  test("replaces DSNs", () => {
    const dsn =
      "postgresql://postgres:fee123@127.0.0.1:54321/flowai?sslmode=disable";
    expect(sanitize(dsn)).toContain("postgresql://<redacted>@<host>/<db>");
    expect(sanitize(dsn)).not.toContain("fee123");
  });

  test("replaces the 32-char hex container password regardless of casing", () => {
    const password = "0123456789abcdef0123456789abcdef";
    expect(sanitize(`password=${password}`)).toContain("<hex-password>");
    expect(sanitize(`password=${password}`)).not.toContain(password);
  });

  test("redacts long hex sequences without over-redacting short hex", () => {
    const longHex = "b".repeat(40);
    const shortHex = "c".repeat(8);
    expect(sanitize(`a=${longHex}`)).toContain("<hex>");
    expect(sanitize(`a=${longHex}`)).not.toContain("b".repeat(40));
    expect(sanitize(`a=${shortHex}`)).toContain(shortHex);
  });

  test("redacts ephemeral container names", () => {
    expect(sanitize("state-registry-pg-1737000000-abcdef12")).toContain(
      "state-registry-pg-<redacted>",
    );
  });

  test("redacts sslrootcert query parameter values", () => {
    const line =
      "pq: SSL error: sslrootcert=/tmp/flowai-v0002-20-tls-abc123/ca.crt certificate verify failed";
    expect(sanitize(line)).toContain("sslrootcert=<redacted>");
    expect(sanitize(line)).not.toContain(
      "/tmp/flowai-v0002-20-tls-abc123/ca.crt",
    );
  });

  test("redacts STATE_REGISTRY_POSTGRES_TLS_CA env var value", () => {
    const line =
      "STATE_REGISTRY_POSTGRES_TLS_CA=/tmp/flowai-v0002-20-tls-abc123/ca.crt";
    expect(sanitize(line)).toContain(
      "STATE_REGISTRY_POSTGRES_TLS_CA=<redacted>",
    );
    expect(sanitize(line)).not.toContain(
      "/tmp/flowai-v0002-20-tls-abc123/ca.crt",
    );
  });

  test("redacts STATE_REGISTRY_TLS_SERVER_CERT, TLS_SERVER_KEY, and TLS_CLIENT_CA env var values", () => {
    const lines = [
      "STATE_REGISTRY_TLS_SERVER_CERT=/tmp/flowai-v0002-20-tls-abc123/server.crt",
      "STATE_REGISTRY_TLS_SERVER_KEY=/tmp/flowai-v0002-20-tls-abc123/server.key",
      "STATE_REGISTRY_TLS_CLIENT_CA=/tmp/flowai-v0002-20-tls-abc123/ca.crt",
    ].join(" | ");
    const out = sanitize(lines);
    expect(out).toContain("STATE_REGISTRY_TLS_SERVER_CERT=<redacted>");
    expect(out).toContain("STATE_REGISTRY_TLS_SERVER_KEY=<redacted>");
    expect(out).toContain("STATE_REGISTRY_TLS_CLIENT_CA=<redacted>");
    for (const p of [
      "/tmp/flowai-v0002-20-tls-abc123/server.crt",
      "/tmp/flowai-v0002-20-tls-abc123/server.key",
      "/tmp/flowai-v0002-20-tls-abc123/ca.crt",
    ]) {
      expect(out).not.toContain(p);
    }
  });

  test("redacts postgres TLS staging dir random suffix while keeping the prefix", () => {
    const line =
      "--mount type=bind,source=/tmp/flowai-pg-tls-AbCdEf1234,target=/etc/flowai/pg-tls,readonly";
    const out = sanitize(line);
    expect(out).toContain("/tmp/flowai-pg-tls-<redacted>");
    expect(out).toContain(",target=/etc/flowai/pg-tls,readonly");
    expect(out).not.toContain("AbCdEf1234");
  });

  test("redacts listener TLS staging dir random suffix while keeping the prefix", () => {
    const line = "tempDir=/tmp/flowai-v0002-20-tls-AbCdEf1234";
    const out = sanitize(line);
    expect(out).toContain("/tmp/flowai-v0002-20-tls-<redacted>");
    expect(out).not.toContain("AbCdEf1234");
  });
});

test.describe("redact.truncateTail", () => {
  test("keeps full input when within budget", () => {
    const input = "a".repeat(500);
    expect(truncateTail(input, 1024)).toBe(input);
  });

  test("keeps the most recent tail when over budget", () => {
    const input = "A".repeat(50) + "B".repeat(50) + "C".repeat(50);
    expect(truncateTail(input, 60)).toBe(input.slice(-60));
  });
});
