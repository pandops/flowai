import { readFile } from "node:fs/promises";
import path from "node:path";
import { test, expect } from "@playwright/test";
import { parse } from "yaml";

const repoRoot = path.resolve(import.meta.dirname, "../../..");

const contracts = [
  {
    file: "openspec/specs/api-gateway/openapi/auth.openapi.yaml",
    operations: [
      ["/auth/v1/login", "get"],
      ["/auth/v1/callback", "get"],
      ["/auth/v1/teams", "get"],
      ["/auth/v1/token", "post"],
      ["/auth/v1/session", "delete"],
      ["/admin/v1/oidc-team-mappings", "post"],
    ],
  },
  {
    file: "openspec/specs/state-registry/openapi/team-admin.openapi.yaml",
    operations: [
      ["/v1/tasks", "post"],
      ["/admin/teams", "post"],
      ["/admin/teams/{team_id}", "get"],
      ["/admin/teams/{team_id}", "patch"],
      ["/admin/teams/{team_id}/archive", "post"],
      ["/internal/v1/teams/{team_id}", "get"],
    ],
  },
  {
    file: "openspec/specs/state-registry/openapi/test-control.openapi.yaml",
    operations: [
      ["/test-control/v1/barriers", "post"],
      ["/test-control/v1/barriers/{barrier_id}", "get"],
      ["/test-control/v1/barriers/{barrier_id}", "delete"],
      ["/test-control/v1/barriers/{barrier_id}/release", "post"],
    ],
  },
] as const;

for (const contract of contracts) {
  test(`${path.basename(contract.file)} exposes every v0007 operation with finite responses`, async () => {
    const document = parse(
      await readFile(path.join(repoRoot, contract.file), "utf8"),
    );
    expect(document.openapi).toBe("3.1.0");
    for (const [route, method] of contract.operations) {
      const operation = document.paths?.[route]?.[method];
      expect(operation, `${method.toUpperCase()} ${route}`).toBeTruthy();
      expect(operation.operationId).toBeTruthy();
      expect(Object.keys(operation.responses ?? {}).length).toBeGreaterThan(0);
    }
    expect(
      document.components?.schemas?.Error ??
        document.components?.schemas?.Barrier,
    ).toBeTruthy();
  });
}
