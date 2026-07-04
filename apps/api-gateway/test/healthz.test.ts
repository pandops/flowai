import { describe, expect, it } from 'vitest';
import { buildApp } from '../src/server.js';

describe('api-gateway placeholder', () => {
  it('serves /healthz with 200 and expected payload', async () => {
    const server = buildApp();
    const port = await new Promise<number>((resolve) => {
      server.listen(0, () => {
        const addr = server.address();
        if (addr && typeof addr === 'object') resolve(addr.port);
        else resolve(0);
      });
    });
    const res = await fetch(`http://127.0.0.1:${port}/healthz`);
    expect(res.status).toBe(200);
    const body = (await res.json()) as { status: string; service: string };
    expect(body.status).toBe('ok');
    expect(body.service).toBe('api-gateway');
    await new Promise<void>((resolve) => server.close(() => resolve()));
  });
});
