import { describe, expect, it } from 'vitest';
import { wrap } from '../src/index.js';

describe('@flowai/events placeholder', () => {
  it('wraps a payload in a versioned envelope', () => {
    const envelope = wrap('flowai.test.event', { hello: 'world' });
    expect(envelope.schema).toBe('flowai.test.event');
    expect(envelope.version).toBe(1);
    expect(envelope.payload).toEqual({ hello: 'world' });
    expect(typeof envelope.id).toBe('string');
    expect(envelope.id.length).toBeGreaterThan(0);
  });
});
