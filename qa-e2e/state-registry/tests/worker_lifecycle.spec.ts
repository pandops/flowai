import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { startRegistryWorker } from "../fixtures/registry_worker";

function listed(prefix: string): string[] {
  return execFileSync("docker", ["ps", "-a", "--format", "{{.Names}}"], {
    encoding: "utf8",
  })
    .split("\n")
    .filter((name) => name.startsWith(prefix));
}

test.describe("container-only registry worker lifecycle", () => {
  test("publishes a fresh image-backed service and removes every owned container", async () => {
    const before = listed("state-registry-");
    const worker = await startRegistryWorker();
    const image = worker.workerExe;
    expect(image).toMatch(/^sha256:/);
    expect((await fetch(`${worker.baseUrl}/v1/livez`)).status).toBe(200);
    await worker.teardown();
    expect(listed("state-registry-")).toEqual(before);
  });

  test("restart replaces the service container while preserving PostgreSQL", async () => {
    const worker = await startRegistryWorker();
    const before = worker.baseUrl;
    const result = await worker.restart();
    expect(result.restartCount).toBe(1);
    expect(worker.baseUrl).not.toBe(before);
    expect((await fetch(`${worker.baseUrl}/v1/readyz`)).status).toBe(200);
    await worker.teardown();
  });

  test("host binary override is rejected", async () => {
    await expect(
      startRegistryWorker({ binary: "/tmp/state-registry" }),
    ).rejects.toThrow(/host State Registry binaries are forbidden/);
  });
});
