import { run, removeContainer, uniqueName } from "./runtime";

export interface DockerService {
  name: string;
  id: string;
  imageId: string;
  baseUrl: string;
  teardown(): Promise<void>;
}

export async function startDockerService(options: {
  imageId: string;
  containerPort: number;
  env?: Record<string, string>;
  args?: string[];
  mounts?: string[];
  groupAdd?: number[];
}): Promise<DockerService> {
  const name = uniqueName("flowai-v0017");
  const args = [
    "run",
    "--detach",
    "--name",
    name,
    "--publish",
    `127.0.0.1::${options.containerPort}`,
  ];
  for (const [key, value] of Object.entries(options.env ?? {}))
    args.push("--env", `${key}=${value}`);
  for (const mount of options.mounts ?? []) args.push("--mount", mount);
  for (const gid of options.groupAdd ?? [])
    args.push("--group-add", String(gid));
  if (
    (process.env.DOCKER_HOST ?? "").includes("podman") &&
    (options.groupAdd?.length ?? 0) > 0
  )
    args.push("--annotation", "run.oci.keep_original_groups=1");
  args.push(options.imageId, ...(options.args ?? []));
  try {
    const id = await run("docker", args);
    const port = await run("docker", [
      "port",
      id,
      `${options.containerPort}/tcp`,
    ]);
    const match = port.match(/127\.0\.0\.1:(\d+)/);
    if (!match) throw new Error(`published port missing: ${port}`);
    const imageId = await run("docker", [
      "inspect",
      id,
      "--format",
      "{{.Image}}",
    ]);
    return {
      name,
      id,
      imageId,
      baseUrl: `http://127.0.0.1:${match[1]}`,
      teardown: async () => await removeContainer(name),
    };
  } catch (error) {
    await removeContainer(name);
    throw error;
  }
}
