// Container runtime helpers for the state-registry qa-e2e suite.
// The harness starts an ephemeral postgres:16 container and queries
// the runtime for the OS-assigned host port after publication. Real
// runtime errors are surfaced so cleanup tests can fail loudly; only
// the documented not-found / already-removed cases are ignored.
import { spawn, type ChildProcess } from "node:child_process";
import { sanitize } from "./redact";

export interface ContainerRuntime {
  binary: "docker" | "podman";
  pull(image: string): Promise<void>;
  runDetached(args: string[]): Promise<ContainerHandle>;
  exec(handle: ContainerHandle, args: string[]): Promise<string>;
  logs(handle: ContainerHandle): Promise<string>;
  remove(handle: ContainerHandle): Promise<void>;
  inspectMappedHostPort(
    handle: ContainerHandle,
    containerPort: number,
  ): Promise<number>;
  isAlreadyRemovedError(message: string): boolean;
}

export interface ContainerHandle {
  id: string;
  name: string;
  runtime: ContainerRuntime;
  inspect(): Promise<ContainerInspect>;
}

export interface ContainerInspect {
  ready: boolean;
  state?: string;
  healthStatus?: string;
}

export class ContainerRuntimeError extends Error {
  readonly command: string;
  readonly stderr: string;
  readonly stdout: string;
  readonly exitCode: number;
  readonly primaryError: Error;
  constructor(
    command: string,
    exitCode: number,
    stdout: string,
    stderr: string,
  ) {
    const sanitizedCmd = sanitize(command);
    const sanitizedStdout = sanitize(stdout);
    const sanitizedStderr = sanitize(stderr);
    super(
      `${sanitizedCmd} exited code=${exitCode} ` +
        `stdout=${sanitizedStdout} stderr=${sanitizedStderr}`,
    );
    this.command = sanitizedCmd;
    this.stdout = sanitizedStdout;
    this.stderr = sanitizedStderr;
    this.exitCode = exitCode;
    this.primaryError = new Error(
      `container runtime command failed: command=${sanitizedCmd} exitCode=${String(exitCode)}`,
    );
    this.name = "ContainerRuntimeError";
  }
}

async function runShell(bin: string, args: string[]): Promise<string> {
  return await new Promise<string>((resolve, reject) => {
    const child: ChildProcess = spawn(bin, args, {
      stdio: ["ignore", "pipe", "pipe"],
    });
    const stdout: Buffer[] = [];
    const stderr: Buffer[] = [];
    child.stdout?.on("data", (b: Buffer) => stdout.push(b));
    child.stderr?.on("data", (b: Buffer) => stderr.push(b));
    child.once("error", reject);
    child.once("close", (code: number | null) => {
      const out = Buffer.concat(stdout).toString("utf-8").trim();
      const err = Buffer.concat(stderr).toString("utf-8").trim();
      if (code === 0) {
        resolve(out);
        return;
      }
      reject(
        new ContainerRuntimeError(
          `${bin} ${args.join(" ")}`,
          code ?? -1,
          out,
          err,
        ),
      );
    });
  });
}

async function runShellCombined(bin: string, args: string[]): Promise<string> {
  return await new Promise<string>((resolve, reject) => {
    const child: ChildProcess = spawn(bin, args, {
      stdio: ["ignore", "pipe", "pipe"],
    });
    const stdout: Buffer[] = [];
    const stderr: Buffer[] = [];
    child.stdout?.on("data", (b: Buffer) => stdout.push(b));
    child.stderr?.on("data", (b: Buffer) => stderr.push(b));
    child.once("error", reject);
    child.once("close", (code: number | null) => {
      const out = Buffer.concat(stdout).toString("utf-8").trim();
      const err = Buffer.concat(stderr).toString("utf-8").trim();
      if (code === 0) {
        resolve(`${out}\n${err}`.trim());
        return;
      }
      reject(
        new ContainerRuntimeError(
          `${bin} ${args.join(" ")}`,
          code ?? -1,
          out,
          err,
        ),
      );
    });
  });
}

export async function DetectRuntime(): Promise<ContainerRuntime> {
  for (const candidate of ["docker", "podman"] as const) {
    try {
      await runShell(candidate, ["--version"]);
      if (candidate === "docker") {
        return new DockerRuntime();
      }
      return new PodmanRuntime();
    } catch {
      // try the next candidate
    }
  }
  throw new Error("neither docker nor podman is available on PATH");
}

class DockerRuntime implements ContainerRuntime {
  public readonly binary = "docker" as const;
  async pull(image: string): Promise<void> {
    await runShell("docker", ["pull", "--quiet", image]);
  }
  async runDetached(args: string[]): Promise<ContainerHandle> {
    const id = await runShell("docker", args);
    return {
      id: id.trim(),
      name: nameFromArgs(args),
      runtime: this,
      async inspect() {
        return await inspectDocker(id.trim());
      },
    };
  }
  async exec(handle: ContainerHandle, args: string[]): Promise<string> {
    return await runShell("docker", ["exec", handle.id, ...args]);
  }
  async logs(handle: ContainerHandle): Promise<string> {
    return await runShellCombined("docker", ["logs", handle.id]);
  }
  async remove(handle: ContainerHandle): Promise<void> {
    const err = await tryRemove("docker", handle);
    if (err && !this.isAlreadyRemovedError(err.stderr)) {
      throw err;
    }
  }
  async inspectMappedHostPort(
    handle: ContainerHandle,
    containerPort: number,
  ): Promise<number> {
    const out = await runShell("docker", [
      "inspect",
      "--format",
      `{{(index (index .NetworkSettings.Ports "${containerPort}/tcp") 0).HostPort}}`,
      handle.id,
    ]);
    const port = Number(out);
    if (!Number.isFinite(port) || port <= 0) {
      throw new ContainerRuntimeError(
        "docker inspect mapped host port",
        1,
        out,
        "",
      );
    }
    return port;
  }
  isAlreadyRemovedError(message: string): boolean {
    return /no such (container|object)|not found/i.test(message);
  }
}

class PodmanRuntime implements ContainerRuntime {
  public readonly binary = "podman" as const;
  async pull(image: string): Promise<void> {
    await runShell("podman", ["pull", "--quiet", image]);
  }
  async runDetached(args: string[]): Promise<ContainerHandle> {
    const id = await runShell("podman", args);
    return {
      id: id.trim(),
      name: nameFromArgs(args),
      runtime: this,
      async inspect() {
        return await inspectPodman(id.trim());
      },
    };
  }
  async exec(handle: ContainerHandle, args: string[]): Promise<string> {
    return await runShell("podman", ["exec", handle.id, ...args]);
  }
  async logs(handle: ContainerHandle): Promise<string> {
    return await runShellCombined("podman", ["logs", handle.id]);
  }
  async remove(handle: ContainerHandle): Promise<void> {
    const err = await tryRemove("podman", handle);
    if (err && !this.isAlreadyRemovedError(err.stderr)) {
      throw err;
    }
  }
  async inspectMappedHostPort(
    handle: ContainerHandle,
    containerPort: number,
  ): Promise<number> {
    const out = await runShell("podman", [
      "inspect",
      "--format",
      `{{(index (index .NetworkSettings.Ports "${containerPort}/tcp") 0).HostPort}}`,
      handle.id,
    ]);
    const port = Number(out);
    if (!Number.isFinite(port) || port <= 0) {
      throw new ContainerRuntimeError(
        "podman inspect mapped host port",
        1,
        out,
        "",
      );
    }
    return port;
  }
  isAlreadyRemovedError(message: string): boolean {
    return /no such (container|object)|already in use|not found/i.test(message);
  }
}

async function tryRemove(
  bin: string,
  handle: ContainerHandle,
): Promise<ContainerRuntimeError | null> {
  try {
    await runShell(bin, ["rm", "--force", handle.id]);
    return null;
  } catch (err) {
    if (err instanceof ContainerRuntimeError) {
      return err;
    }
    throw err;
  }
}

function nameFromArgs(args: string[]): string {
  for (let i = 0; i < args.length; i++) {
    if (args[i] === "--name") {
      return (args[i + 1] ?? "").trim();
    }
  }
  return "";
}

async function inspectDocker(id: string): Promise<ContainerInspect> {
  try {
    const json = await runShell("docker", [
      "inspect",
      "--format",
      "{{json .State}}",
      id,
    ]);
    const parsed = JSON.parse(json) as {
      Running?: boolean;
      Health?: { Status?: string };
      Status?: string;
    };
    return {
      ready: parsed.Running === true,
      state: parsed.Status,
      healthStatus: parsed.Health?.Status,
    };
  } catch {
    return { ready: false };
  }
}

async function inspectPodman(id: string): Promise<ContainerInspect> {
  try {
    const json = await runShell("podman", ["inspect", "--format", "json", id]);
    const arr = JSON.parse(json) as Array<{
      State?: { Running?: boolean; HealthCheckStatus?: string };
      Status?: string;
    }>;
    const first = arr[0];
    return {
      ready: first?.State?.Running === true,
      state: first?.Status,
      healthStatus: first?.State?.HealthCheckStatus,
    };
  } catch {
    return { ready: false };
  }
}
