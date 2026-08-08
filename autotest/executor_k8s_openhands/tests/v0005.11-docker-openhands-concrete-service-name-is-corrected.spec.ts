// v0005.11-docker-openhands-concrete-service-name-is-corrected
//
// The existing Docker OpenHands concrete Executor is corrected
// from executor_docker_opehands to executor_docker_openhands
// consistently across its filesystem, executable surface, and
// State Registry wire registration, while keeping the Go constant
// name ExecutorTypeDockerOpenHands. The old wire value has no
// compatibility alias, and archived OpenSpec artifacts remain
// historical records rather than current runtime inputs.

import { test, expect } from "@playwright/test";
import { access, glob, readFile } from "node:fs/promises";
import { resolve } from "node:path";

test("v0005.11 docker openhands concrete service name is corrected", async () => {
  const repositoryRoot = resolve(__dirname, "../../..");
  // Assert the renamed directory, command, and config YAML exist;
  // the misspelled non-archived paths do not.
  expect(
    (
      await Array.fromAsync(
        glob("executor_docker_openhands/cmd/executor_docker_openhands", {
          cwd: repositoryRoot,
        }),
      )
    ).length,
  ).toBeGreaterThan(0);
  expect(
    (
      await Array.fromAsync(
        glob(
          "executor_docker_openhands/configs/executor_docker_openhands.yaml",
          {
            cwd: repositoryRoot,
          },
        ),
      )
    ).length,
  ).toBeGreaterThan(0);
  await expect(
    access(resolve(repositoryRoot, "autotest/executor_docker_openhands")),
  ).resolves.toBeUndefined();
  await expect(
    access(resolve(repositoryRoot, "executor_docker_opehands")),
  ).rejects.toThrow();
  await expect(
    access(resolve(repositoryRoot, "autotest/executor_docker_opehands")),
  ).rejects.toThrow();
  const platform = await readFile(
    resolve(
      repositoryRoot,
      "executor_docker_openhands/internal/platform/types.go",
    ),
    "utf8",
  );
  expect(platform).toContain(
    'ExecutorTypeDockerOpenHands = "executor_docker_openhands"',
  );
  expect(platform).not.toContain('"executor_docker_opehands"');
});
