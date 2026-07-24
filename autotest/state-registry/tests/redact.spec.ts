// Tests for the secret redactor. The redactor runs over captured log
// streams and thrown errors before they reach the harness console or
// the test reports, so leaking any token at this layer breaks the
// scaffold contract.
import { test, expect } from '@playwright/test';
import { sanitize, truncateTail } from '../fixtures/redact';

test.describe('redact.sanitize', () => {
  test('replaces STATE_REGISTRY_AES_KEY_HEX tokens', () => {
    const keyHex = 'STATE_REGISTRY_AES_KEY_HEX=' + 'a'.repeat(64);
    expect(sanitize(keyHex)).toContain('STATE_REGISTRY_AES_KEY_HEX=<redacted>');
    expect(sanitize(keyHex)).not.toContain('a'.repeat(64));
  });

  test('replaces POSTGRES_PASSWORD tokens', () => {
    const envLine = 'POSTGRES_PASSWORD=' + 'f'.repeat(32);
    expect(sanitize(envLine)).toContain('POSTGRES_PASSWORD=<redacted>');
    expect(sanitize(envLine)).not.toContain('f'.repeat(32));
  });

  test('replaces DSNs', () => {
    const dsn = 'postgresql://postgres:fee123@127.0.0.1:54321/flowai?sslmode=disable';
    expect(sanitize(dsn)).toContain('postgresql://<redacted>@<host>/<db>');
    expect(sanitize(dsn)).not.toContain('fee123');
  });

  test('replaces the 32-char hex container password regardless of casing', () => {
    const password = '0123456789abcdef0123456789abcdef';
    expect(sanitize(`password=${password}`)).toContain('<hex-password>');
    expect(sanitize(`password=${password}`)).not.toContain(password);
  });

  test('redacts long hex sequences without over-redacting short hex', () => {
    const longHex = 'b'.repeat(40);
    const shortHex = 'c'.repeat(8);
    expect(sanitize(`a=${longHex}`)).toContain('<hex>');
    expect(sanitize(`a=${longHex}`)).not.toContain('b'.repeat(40));
    expect(sanitize(`a=${shortHex}`)).toContain(shortHex);
  });

  test('redacts ephemeral container names', () => {
    expect(sanitize('state-registry-pg-1737000000-abcdef12')).toContain('state-registry-pg-<redacted>');
  });
});

test.describe('redact.truncateTail', () => {
  test('keeps full input when within budget', () => {
    const input = 'a'.repeat(500);
    expect(truncateTail(input, 1024)).toBe(input);
  });

  test('keeps the most recent tail when over budget', () => {
    const input = 'A'.repeat(50) + 'B'.repeat(50) + 'C'.repeat(50);
    expect(truncateTail(input, 60)).toBe(input.slice(-60));
  });
});