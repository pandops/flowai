import { test, expect } from '@playwright/test';
import { StartPostgresContainer, StopPostgresContainer } from '../fixtures/postgres_container';

test.describe('StartPostgresContainer failure-injection', () => {
  test('StartPostgresContainer returns Error for primary pull failure (not AggregateError when no container)', async () => {
    let captured: unknown;
    try {
      await StartPostgresContainer({
        image: 'docker.io/library/this-image-does-not-exist:0',
      });
    } catch (err) {
      captured = err;
    }
    expect(captured).toBeDefined();
    expect((captured as Error).name).toMatch(/Error/);
    expect((captured as Error).message).toMatch(/no such image|invalid|FATAL|manifest|invalid reference/i);
  });

  test('StartPostgresContainer with bad image + removeFailureCount throws the primary error without cleanup', async () => {
    let captured: unknown;
    try {
      await StartPostgresContainer({
        image: 'docker.io/library/this-image-does-not-exist:0',
        removeFailureCount: 1,
      });
    } catch (err) {
      captured = err;
    }
    expect(captured).toBeDefined();
    const err = captured as Error;
    expect(err.message).not.toMatch(/injected postgres remove failure/);
    expect(err.message).toMatch(/no such image|invalid|FATAL|manifest|invalid reference/i);
  });

  test('StopPostgresContainer with a null container returns null', async () => {
    const err = await StopPostgresContainer(null);
    expect(err).toBeNull();
  });
});
