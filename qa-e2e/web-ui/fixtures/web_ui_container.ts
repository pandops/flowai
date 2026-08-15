import { execFile } from "node:child_process";
import path from "node:path";
import { promisify } from "node:util";

const exec = promisify(execFile);
const repoRoot = path.resolve(__dirname, "../../..");
const runtime = process.env.FLOWAI_CONTAINER_RUNTIME ?? "docker";
const imageTag = process.env.FLOWAI_WEB_UI_IMAGE ?? "flowai-web-ui-e2e:local";

let imageBuild: Promise<string> | undefined;

export type WebUIContainer = Readonly<{
  baseUrl: string;
  imageID: string;
  close: () => Promise<void>;
}>;

export async function startWebUIContainer(): Promise<WebUIContainer> {
  const imageID = await buildImage();
  const started = await exec(
    runtime,
    [
      "run",
      "--detach",
      "--rm",
      "--publish",
      "127.0.0.1::8080",
      "--label",
      "flowai.e2e.service=web-ui",
      imageTag,
    ],
    { cwd: repoRoot },
  );
  const containerID = started.stdout.trim();
  if (!containerID)
    throw new Error("container runtime returned no Web UI container id");

  try {
    const portResult = await exec(runtime, ["port", containerID, "8080/tcp"], {
      cwd: repoRoot,
    });
    const published = portResult.stdout.trim().split("\n")[0];
    const port = published.slice(published.lastIndexOf(":") + 1);
    if (!/^\d+$/.test(port))
      throw new Error(`cannot resolve Web UI published port from ${published}`);
    const baseUrl = `http://127.0.0.1:${port}`;
    await waitUntilHealthy(baseUrl);
    return {
      baseUrl,
      imageID,
      close: async () => {
        await exec(runtime, ["stop", "--time", "2", containerID], {
          cwd: repoRoot,
        }).catch(() => undefined);
      },
    };
  } catch (error) {
    await exec(runtime, ["stop", "--time", "2", containerID], {
      cwd: repoRoot,
    }).catch(() => undefined);
    throw error;
  }
}

function buildImage(): Promise<string> {
  if (!imageBuild) {
    imageBuild = (async () => {
      await exec(
        runtime,
        ["build", "--file", "svc/web-ui/Containerfile", "--tag", imageTag, "."],
        { cwd: repoRoot, maxBuffer: 20 * 1024 * 1024 },
      );
      const inspected = await exec(
        runtime,
        ["image", "inspect", imageTag, "--format", "{{.Id}}"],
        { cwd: repoRoot },
      );
      const imageID = inspected.stdout.trim();
      if (!imageID)
        throw new Error("container runtime returned no Web UI image id");
      return imageID;
    })();
  }
  return imageBuild;
}

async function waitUntilHealthy(baseUrl: string): Promise<void> {
  let lastError: unknown;
  for (let attempt = 0; attempt < 50; attempt += 1) {
    try {
      const response = await fetch(`${baseUrl}/healthz`);
      if (response.ok) return;
      lastError = new Error(`health status ${response.status}`);
    } catch (error) {
      lastError = error;
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(
    `Web UI container did not become healthy: ${String(lastError)}`,
  );
}
