import { execFile } from "node:child_process";
import { promisify } from "node:util";
import path from "node:path";

const exec = promisify(execFile);
const repoRoot = path.resolve(__dirname, "../../..");

export async function run(
  command: string,
  args: string[],
  timeout = 180_000,
): Promise<string> {
  const { stdout } = await exec(command, args, {
    cwd: repoRoot,
    timeout,
    maxBuffer: 16 * 1024 * 1024,
  });
  return stdout.trim();
}

export async function removeContainer(name: string): Promise<void> {
  await run("docker", ["rm", "--force", name]).catch(() => undefined);
}

export function uniqueName(prefix: string): string {
  return `${prefix}-${process.pid}-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}
