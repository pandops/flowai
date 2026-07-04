import { describe, expect, it } from 'vitest';
import { SERVICE_VERSION, type HealthStatus } from '../src/index.js';

describe('@flowai/types placeholder', () => {
  it('exports a version string', () => {
    expect(SERVICE_VERSION).toMatch(/^\d+\.\d+\.\d+$/);
  });

  it('accepts a HealthStatus literal', () => {
    const sample: HealthStatus = { status: 'ok', service: 'test', version: SERVICE_VERSION };
    expect(sample.status).toBe('ok');
  });
});
