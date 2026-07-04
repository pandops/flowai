// packages/events - event schema registry and contract helpers per ADR 0005.
// Concrete event contracts land in later waves (see .omo/artifacts/event-contracts/).

export interface EventEnvelope<TPayload> {
  id: string;
  schema: string;
  version: number;
  emittedAt: string;
  payload: TPayload;
}

export function wrap<TPayload>(schema: string, payload: TPayload): EventEnvelope<TPayload> {
  return {
    id: cryptoRandomId(),
    schema,
    version: 1,
    emittedAt: new Date().toISOString(),
    payload,
  };
}

function cryptoRandomId(): string {
  // Node 20+: prefer crypto.randomUUID; fallback keeps the scaffold dependency-free.
  const g = globalThis as { crypto?: { randomUUID?: () => string } };
  if (g.crypto?.randomUUID) {
    return g.crypto.randomUUID();
  }
  return `evt-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}
