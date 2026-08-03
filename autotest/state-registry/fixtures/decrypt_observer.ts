// Decrypt-operation observer for the state-registry autotest suite.
// The harness queries GET /v1/_test/decrypt-ops on the running state-
// registry process and returns the cumulative counter. The counter
// must remain zero for smoke requests because the protected decrypt
// path is unimplemented; any positive value means protected behavior
// was exercised during the observation window.
export interface DecryptObservation {
  raw: number;
  at: string;
}

export async function snapshotDecryptOps(baseUrl: string): Promise<DecryptObservation> {
  const resp = await fetch(`${baseUrl}/v1/_test/decrypt-ops`);
  if (!resp.ok) {
    throw new Error(`failed to fetch decrypt-ops: status=${String(resp.status)}`);
  }
  const body = (await resp.json()) as { decrypt_ops?: number };
  const raw = typeof body.decrypt_ops === 'number' ? body.decrypt_ops : 0;
  return { raw, at: new Date().toISOString() };
}