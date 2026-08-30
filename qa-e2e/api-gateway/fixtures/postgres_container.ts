import { execFile } from "node:child_process";
import { promisify } from "node:util";
import pg from "pg";

const exec = promisify(execFile);
const runtime = process.env.FLOWAI_CONTAINER_RUNTIME ?? "docker";
const image = "docker.io/library/postgres:17-alpine";

export type PostgresContainer = Readonly<{
  connectionString: string;
  close: () => Promise<void>;
}>;

export async function startPostgresContainer(): Promise<PostgresContainer> {
  const started = await exec(runtime, [
    "run",
    "--detach",
    "--rm",
    "--publish",
    "127.0.0.1::5432",
    "--label",
    "flowai.e2e.service=api-gateway-postgres",
    "--env",
    "POSTGRES_PASSWORD=flowai-e2e-postgres",
    image,
  ]);
  const containerID = started.stdout.trim();
  if (!containerID) throw new Error("no PostgreSQL container ID");
  try {
    const portResult = await exec(runtime, ["port", containerID, "5432/tcp"]);
    const published = portResult.stdout.trim().split("\n")[0];
    const port = published.slice(published.lastIndexOf(":") + 1);
    if (!/^\d+$/.test(port))
      throw new Error(`invalid PostgreSQL port ${published}`);
    for (let attempt = 0; attempt < 300; attempt += 1) {
      const ready = await exec(runtime, [
        "exec",
        containerID,
        "pg_isready",
        "-U",
        "postgres",
      ])
        .then(() => true)
        .catch(() => false);
      const connectionString = `postgres://postgres:flowai-e2e-postgres@127.0.0.1:${port}/postgres?sslmode=disable`;
      if (ready) {
        const client = new pg.Client({
          connectionString,
          connectionTimeoutMillis: 500,
        });
        const hostReady = await client
          .connect()
          .then(async () => {
            await client.end();
            return true;
          })
          .catch(async () => {
            await client.end().catch(() => undefined);
            return false;
          });
        if (hostReady)
          return {
            connectionString,
            close: async () => {
              await exec(runtime, ["stop", "--time", "2", containerID]).catch(
                () => undefined,
              );
            },
          };
      }
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    throw new Error("PostgreSQL did not become ready");
  } catch (error) {
    await exec(runtime, ["stop", "--time", "2", containerID]).catch(
      () => undefined,
    );
    throw error;
  }
}
