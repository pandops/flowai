import { test, expect } from "@playwright/test";
import { ContainerRuntimeError } from "../fixtures/container_runtime";

test("ContainerRuntimeError stores sanitized values for stdout, stderr, and command", () => {
  const err = new ContainerRuntimeError(
    "docker run --env POSTGRES_PASSWORD=supersecret",
    125,
    "stdout postgresql://u:secret@h/d",
    "stderr postgresql://u:secret@h/d",
  );
  expect(err.command).not.toContain("supersecret");
  expect(err.command).toContain("POSTGRES_PASSWORD=<redacted>");
  expect(err.stdout).not.toContain("secret");
  expect(err.stderr).not.toContain("secret");
  expect(err.message).not.toContain("secret");
  expect(err.exitCode).toBe(125);
  expect(err.primaryError).toBeDefined();
});

test('isAlreadyRemovedError does NOT treat "already in use" as removed', () => {
  const err = new ContainerRuntimeError(
    "docker rm --force abc",
    1,
    "",
    "Error: another process is already in use",
  );
  expect(/already in use/i.test(err.stderr)).toBe(true);
  expect(/no such|not found/i.test(err.stderr)).toBe(false);
});

test("ContainerRuntimeError preserves the original command and exitCode in sanitized form", () => {
  const err = new ContainerRuntimeError(
    "docker rm --force abc",
    125,
    "",
    "container not found",
  );
  expect(err.command).toBe("docker rm --force abc");
  expect(err.exitCode).toBe(125);
  expect(err.stderr).toBe("container not found");
});
