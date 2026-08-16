import { access, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { run } from "./runtime";

export type ServiceName = "stateRegistry" | "dockerExecutor" | "k8sExecutor";
export interface BuiltImage {
  id: string;
  tag: string;
}
export interface ImageManifest {
  images: Partial<Record<ServiceName, BuiltImage>>;
  errors: Partial<Record<ServiceName, string>>;
}

const root = path.resolve(__dirname, "../../..");
export const manifestPath = path.join(__dirname, "..", ".images.json");
const definitions: Array<[ServiceName, string, string]> = [
  [
    "stateRegistry",
    "flowai-v0017-state-registry",
    "svc/state-registry/Containerfile",
  ],
  [
    "dockerExecutor",
    "flowai-v0017-executor-docker-openhands",
    "executor/docker_openhands/Containerfile",
  ],
  [
    "k8sExecutor",
    "flowai-v0017-executor-k8s-openhands",
    "executor/k8s-openhands/Containerfile",
  ],
];

let buildPromise: Promise<ImageManifest> | undefined;

export function buildImagesOnce(): Promise<ImageManifest> {
  buildPromise ??= buildImages();
  return buildPromise;
}

async function buildImages(): Promise<ImageManifest> {
  const manifest: ImageManifest = { images: {}, errors: {} };
  for (const [name, tag, relativeFile] of definitions) {
    try {
      await access(path.join(root, relativeFile));
      await run(
        "docker",
        ["build", "--tag", tag, "--file", relativeFile, "."],
        600_000,
      );
      const inspected = await run("docker", [
        "image",
        "inspect",
        tag,
        "--format",
        "{{.Id}}",
      ]);
      const id = inspected.startsWith("sha256:")
        ? inspected
        : `sha256:${inspected}`;
      if (!/^sha256:[a-f0-9]{64}$/.test(id))
        throw new Error(`immutable image ID missing: ${inspected}`);
      manifest.images[name] = { id, tag };
    } catch (error) {
      manifest.errors[name] = String(error);
    }
  }
  await writeFile(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
  return manifest;
}

export async function readManifest(): Promise<ImageManifest> {
  return JSON.parse(await readFile(manifestPath, "utf8")) as ImageManifest;
}
