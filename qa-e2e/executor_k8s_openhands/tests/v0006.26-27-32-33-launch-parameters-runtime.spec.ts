import * as path from "node:path";

import { startMockedProxy } from "../../web-ui/fixtures/mocked_proxy";
import { expect, test } from "../fixtures/k3d-suite";

const namespace = "flowai-executor-k8s";

test("v0006.26/.27/.32/.33 UI env and secret mutations control new K8s task Pods", async ({
  page,
  suite,
}) => {
  test.setTimeout(180_000);
  const suffix = crypto
    .randomUUID()
    .replaceAll("-", "")
    .slice(0, 10)
    .toUpperCase();
  const envKey = `FLOWAI_ENV_${suffix}`;
  const secretKey = `FLOWAI_SECRET_${suffix}`;
  const envValue = `env-${suffix.toLowerCase()}`;
  const secretValue = `secret-${crypto.randomUUID()}`;
  const proxy = await startMockedProxy(
    [
      {
        team_id: suite.teamA.admin.team_id,
        team_name: suite.teamA.admin.team_name,
      },
    ],
    path.resolve(__dirname, "../../../svc/web-ui/web/index.html"),
    suite.registryBaseURL,
  );
  const podFor = async (taskID: string): Promise<string> => {
    let pod = "";
    await expect
      .poll(
        async () => {
          pod = await suite
            .kubectl(
              "get",
              "pods",
              "-n",
              namespace,
              "-l",
              `flowai.task_id=${taskID}`,
              "-o",
              "jsonpath={.items[0].metadata.name}",
            )
            .catch(() => "");
          return pod;
        },
        { timeout: 30_000 },
      )
      .not.toBe("");
    return pod;
  };
  const readKey = async (pod: string, key: string): Promise<string | null> => {
    try {
      return await suite.kubectl(
        "exec",
        "-n",
        namespace,
        pod,
        "--",
        "printenv",
        key,
      );
    } catch {
      return null;
    }
  };
  try {
    await page.goto(`${proxy.baseUrl}?view=parameters`);
    await page.getByRole("button", { name: "Add variable" }).click();
    await page.locator("#env-dialog").getByLabel("Key name").fill(envKey);
    await page
      .locator("#env-dialog")
      .getByLabel("Value", { exact: true })
      .fill(envValue);
    await page
      .locator("#env-dialog")
      .getByRole("button", { name: "Save" })
      .click();
    await expect(page.getByText(`${envKey}=${envValue}`)).toBeVisible();

    await page.getByRole("button", { name: "Add secret" }).first().click();
    await page
      .locator("#secret-dialog")
      .getByLabel("Secret key")
      .fill(secretKey);
    await page
      .locator("#secret-dialog")
      .getByLabel("New secret value")
      .fill(secretValue);
    await page
      .locator("#secret-dialog")
      .getByRole("button", { name: "Save secret" })
      .click();
    await expect(page.getByText(`${secretKey} ••••••••`)).toBeVisible();
    await expect(page.getByText(secretValue)).toHaveCount(0);

    const projectionResponse = await fetch(
      `${suite.registryBaseURL}/ui/v1/teams/${suite.teamA.admin.team_id}/launch-parameters?limit=10`,
    );
    expect(projectionResponse.status).toBe(200);
    const projection = (await projectionResponse.json()) as {
      items: Array<{
        env: Record<string, string>;
        secrets: Array<{ name: string }>;
      }>;
    };
    expect(projection.items.some((item) => item.env[envKey] === envValue)).toBe(
      true,
    );
    expect(
      projection.items.some((item) =>
        item.secrets.some((secret) => secret.name === secretKey),
      ),
    ).toBe(true);
    expect(JSON.stringify(projection)).not.toContain(secretValue);

    const firstTask = await suite.ingestTask(suite.teamA, {
      prompt: "Keep the task alive long enough to inspect its environment.",
    });
    const firstPod = await podFor(firstTask.task_id);
    await expect
      .poll(() => readKey(firstPod, envKey), { timeout: 20_000 })
      .toBe(envValue);
    await expect
      .poll(() => readKey(firstPod, secretKey), { timeout: 20_000 })
      .toBe(secretValue);

    page.once("dialog", (dialog) => dialog.accept());
    await page.getByRole("button", { name: `Delete ${envKey}` }).click();
    await expect(page.getByText(new RegExp(`^${envKey}=`))).toHaveCount(0);
    page.once("dialog", (dialog) => dialog.accept());
    await page
      .locator(".token.secret")
      .filter({ hasText: secretKey })
      .getByRole("button", { name: "Delete" })
      .click();
    await expect(page.getByText(`${secretKey} ••••••••`)).toHaveCount(0);

    const removedProjection = (await (
      await fetch(
        `${suite.registryBaseURL}/ui/v1/teams/${suite.teamA.admin.team_id}/launch-parameters?limit=10`,
      )
    ).json()) as {
      items: Array<{
        env: Record<string, string>;
        secrets: Array<{ name: string }>;
      }>;
    };
    expect(removedProjection.items.every((item) => !(envKey in item.env))).toBe(
      true,
    );
    expect(
      removedProjection.items.every((item) =>
        item.secrets.every((secret) => secret.name !== secretKey),
      ),
    ).toBe(true);

    const secondTask = await suite.ingestTask(suite.teamA, {
      prompt: "Verify removed launch parameters are absent.",
    });
    const secondPod = await podFor(secondTask.task_id);
    await expect
      .poll(() => readKey(secondPod, envKey), { timeout: 20_000 })
      .toBeNull();
    await expect
      .poll(() => readKey(secondPod, secretKey), { timeout: 20_000 })
      .toBeNull();
  } finally {
    await proxy.close();
  }
});
