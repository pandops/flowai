import { lstat, realpath } from "node:fs/promises";

export interface SocketAccess {
  hostPath: string;
  gid: number;
}

export async function preflightSocket(
  configuredPath: string,
): Promise<SocketAccess> {
  if (!configuredPath)
    throw new Error("Docker socket path must be configured explicitly");
  const hostPath = await realpath(configuredPath);
  const info = await lstat(hostPath);
  if (!info.isSocket()) throw new Error(`${hostPath} is not a Unix socket`);
  const mode = info.mode & 0o777;
  if (mode === 0o666) throw new Error(`${hostPath} mode 0666 is forbidden`);
  if ((mode & 0o060) !== 0o060)
    throw new Error(`${hostPath} requires group read/write bits`);
  return { hostPath, gid: info.gid };
}
