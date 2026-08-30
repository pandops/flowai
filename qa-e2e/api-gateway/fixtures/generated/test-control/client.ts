// Generated from openspec/specs/state-registry/openapi/test-control.openapi.yaml. Do not edit.
export class TestControlClient {
  constructor(
    private readonly baseURL: string,
    private readonly token: string,
  ) {}
  private headers = {
    authorization: `Bearer ${this.token}`,
    "content-type": "application/json",
  };
  async createBarrier(name: string): Promise<{ barrier_id: string }> {
    const response = await fetch(`${this.baseURL}/test-control/v1/barriers`, {
      method: "POST",
      headers: this.headers,
      body: JSON.stringify({ name }),
    });
    if (response.status !== 201)
      throw new Error(
        `createBarrier: ${response.status} ${await response.text()}`,
      );
    return response.json() as Promise<{ barrier_id: string }>;
  }
  async waitForBarrier(id: string, waitMs = 5000): Promise<void> {
    const response = await fetch(
      `${this.baseURL}/test-control/v1/barriers/${id}?wait_ms=${waitMs}`,
      { headers: this.headers },
    );
    if (response.status !== 200)
      throw new Error(
        `waitForBarrier: ${response.status} ${await response.text()}`,
      );
    const body = (await response.json()) as { state: string };
    if (body.state !== "reached")
      throw new Error(`waitForBarrier: state ${body.state}`);
  }
  async releaseBarrier(id: string): Promise<void> {
    const response = await fetch(
      `${this.baseURL}/test-control/v1/barriers/${id}/release`,
      { method: "POST", headers: this.headers },
    );
    if (response.status !== 200)
      throw new Error(
        `releaseBarrier: ${response.status} ${await response.text()}`,
      );
  }
  async deleteBarrier(id: string): Promise<void> {
    const response = await fetch(
      `${this.baseURL}/test-control/v1/barriers/${id}`,
      { method: "DELETE", headers: this.headers },
    );
    if (response.status !== 204)
      throw new Error(
        `deleteBarrier: ${response.status} ${await response.text()}`,
      );
  }
}
