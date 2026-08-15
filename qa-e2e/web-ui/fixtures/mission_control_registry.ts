import { createServer, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import { WebSocket, WebSocketServer } from "ws";

type Task = {
  task_id: string;
  task_type_id: string;
  source_system_id: string;
  source_id: string;
  current_state: string;
  executor_id: string | null;
  ingested_at: string;
  claimed_at: string | null;
  completed_at: string | null;
  environment: Record<string, string>;
  secret_keys: string[];
};

export type MissionControlRegistry = {
  origin: string;
  tasks: Task[];
  launchParameters: any[];
  audit: any[];
  push: (teamId: string, frame: unknown) => void;
  close: () => Promise<void>;
};

export async function startMissionControlRegistry(): Promise<MissionControlRegistry> {
  const now = Date.now();
  const tasks: Task[] = Array.from({ length: 18 }, (_, index) => {
    const states = ["pending", "created", "running", "finished", "failed"];
    const current_state = states[index % states.length];
    return {
      task_id:
        index === 2
          ? "task-alpha-running"
          : `task-${String(index + 1).padStart(3, "0")}`,
      task_type_id: index % 2 ? "code-review" : "repository-task",
      source_system_id: "listener-alpha",
      source_id: `external-${index + 1}`,
      current_state,
      executor_id: current_state === "pending" ? null : "executor-alpha",
      ingested_at: new Date(now - index * 60_000).toISOString(),
      claimed_at:
        current_state === "pending"
          ? null
          : new Date(now - index * 60_000 + 5_000).toISOString(),
      completed_at: ["finished", "failed"].includes(current_state)
        ? new Date(now - index * 60_000 + 35_000).toISOString()
        : null,
      environment: { FLOWAI_VISIBLE: "fixture-value", WORKSPACE: "/workspace" },
      secret_keys: ["FLOWAI_SECRET"],
    };
  });
  const launchParameters: any[] = [
    {
      environment_id: "env-team",
      team_id: "team-alpha",
      scope: "team",
      task_type_id: null,
      name: "Team parameters",
      env: { FLOWAI_VISIBLE: "fixture-value" },
      image: null,
      revision: 2,
      secrets: [
        {
          secret_id: "secret-alpha",
          key: "FLOWAI_SECRET",
          current_version: 2,
          revoked: false,
        },
      ],
      created_at: new Date(now - 60_000).toISOString(),
      updated_at: new Date(now).toISOString(),
    },
    {
      environment_id: "env-type",
      team_id: "team-alpha",
      scope: "task_type",
      task_type_id: "repository-task",
      name: "Repositories",
      env: { WORKSPACE: "/workspace" },
      image: null,
      revision: 1,
      secrets: [],
      created_at: new Date(now).toISOString(),
      updated_at: new Date(now).toISOString(),
    },
  ];
  const audit = Array.from({ length: 15 }, (_, index) => ({
    audit_id: `audit-${index}`,
    actor_id: index % 2 ? "operator-alpha" : "executor-alpha",
    actor_type: index % 2 ? "operator" : "executor",
    action: index % 3 ? "environment.replace" : "task.control.requested",
    resource_type: index % 3 ? "environment" : "task_control_request",
    resource_id: index % 3 ? "env-team" : "control-alpha",
    request_id: `request-${index}`,
    outcome: "accepted",
    occurred_at: new Date(now - index * 1_000).toISOString(),
  }));
  const sockets = new Map<string, Set<WebSocket>>();
  const server = createServer(async (request, response) => {
    const url = new URL(request.url ?? "/", "http://fixture");
    const match = url.pathname.match(/^\/ui\/v1\/teams\/([^/]+)(\/.*)$/);
    response.setHeader("content-type", "application/json; charset=utf-8");
    if (!match)
      return json(response, 404, {
        code: "not_found",
        message: "resource not found",
      });
    const [, teamId, route] = match;
    if (teamId !== "team-alpha")
      return json(response, 404, {
        code: "not_found",
        message: "resource not found",
      });
    if (route === "/dashboard" && request.method === "GET") {
      const counts = {
        pending: 0,
        created: 0,
        running: 0,
        finished: 0,
        failed: 0,
      };
      tasks.forEach(
        (task) => counts[task.current_state as keyof typeof counts]++,
      );
      const period = url.searchParams.get("period") ?? "week";
      const buckets =
        period === "month"
          ? calendarMonthBuckets(url.searchParams.get("month"), tasks)
          : isoWeekBuckets(url.searchParams.get("week"), tasks);
      return json(response, 200, { period, counts, buckets });
    }
    if (route === "/task-types" && request.method === "GET") {
      return json(response, 200, {
        items: [
          { task_type_id: "repository-task", label: "Repository task" },
          { task_type_id: "code-review", label: "Code review" },
        ],
        next_cursor: null,
      });
    }
    if (route === "/tasks" && request.method === "GET") {
      let items = [...tasks];
      const status = url.searchParams.get("status"),
        search = (url.searchParams.get("search") ?? "").toLowerCase();
      if (status) items = items.filter((item) => item.current_state === status);
      const date = url.searchParams.get("date");
      if (date)
        items = items.filter((item) => item.ingested_at.slice(0, 10) === date);
      if (search)
        items = items.filter((item) =>
          JSON.stringify(item).toLowerCase().includes(search),
        );
      const limit = Number(url.searchParams.get("limit") ?? 10),
        offset = Number(url.searchParams.get("cursor") ?? 0);
      return json(response, 200, {
        items: items.slice(offset, offset + limit),
        next_cursor:
          offset + limit < items.length ? String(offset + limit) : null,
      });
    }
    const taskMatch = route.match(
      /^\/tasks\/([^/]+)(\/events|\/logs|\/controls\/cancel)?$/,
    );
    if (taskMatch) {
      const task = tasks.find(
        (item) => item.task_id === decodeURIComponent(taskMatch[1]),
      );
      if (!task)
        return json(response, 404, {
          code: "not_found",
          message: "resource not found",
        });
      if (!taskMatch[2] && request.method === "GET")
        return json(response, 200, task);
      if (taskMatch[2] === "/events" && request.method === "GET")
        return json(response, 200, {
          items:
            task.current_state === "pending"
              ? []
              : [
                  {
                    event_id: "created",
                    event_type: "task.lifecycle.created",
                    status: "created",
                    executor_id: task.executor_id,
                    occurred_at: task.claimed_at,
                    payload: {},
                  },
                  {
                    event_id: "running",
                    event_type: "task.lifecycle.running",
                    status: "running",
                    executor_id: task.executor_id,
                    occurred_at: new Date(
                      new Date(task.claimed_at!).getTime() + 1000,
                    ).toISOString(),
                    payload: {},
                  },
                ],
          next_cursor: null,
        });
      if (taskMatch[2] === "/logs" && request.method === "GET")
        return json(response, 200, {
          items: [
            {
              log_chunk_id: "log-1",
              task_id: task.task_id,
              executor_id: task.executor_id,
              log_offset: 1,
              stream: "reasoning",
              content: "Inspecting the workspace",
              occurred_at: task.claimed_at,
            },
            {
              log_chunk_id: "log-2",
              task_id: task.task_id,
              executor_id: task.executor_id,
              log_offset: 2,
              stream: "work",
              content: "FlowAI marker written",
              occurred_at: task.claimed_at,
            },
          ],
          next_cursor: null,
        });
      if (taskMatch[2] === "/controls/cancel" && request.method === "POST")
        return json(response, 202, {
          control_id: "control-alpha",
          status: "pending",
        });
    }
    const executors = [
      {
        executor_id: "executor-alpha",
        executor_type: "executor_docker_openhands",
        scope: "team",
        authorized_tag: "openhands",
        status: "busy",
        max_capacity: 2,
        running_count: 1,
        available_capacity: 1,
        runtime_metadata: { runtime: "docker" },
        updated_at: new Date(now).toISOString(),
      },
      {
        executor_id: "executor-system",
        executor_type: "executor_k8s_openhands",
        scope: "system",
        authorized_tag: "openhands",
        status: "idle",
        max_capacity: 4,
        running_count: 0,
        available_capacity: 4,
        runtime_metadata: { runtime: "k8s" },
        updated_at: new Date(now - 1000).toISOString(),
      },
    ];
    if (route === "/executors" && request.method === "GET") {
      let items = executors;
      const status = url.searchParams.get("status"),
        search = (url.searchParams.get("search") ?? "").toLowerCase();
      if (status) items = items.filter((item) => item.status === status);
      if (search)
        items = items.filter((item) =>
          JSON.stringify(item).toLowerCase().includes(search),
        );
      return json(response, 200, { items, next_cursor: null });
    }
    const executorMatch = route.match(
      /^\/executors\/([^/]+)(\/events|\/tasks)?$/,
    );
    if (executorMatch) {
      const executor = executors.find(
        (item) => item.executor_id === decodeURIComponent(executorMatch[1]),
      );
      if (!executor)
        return json(response, 404, {
          code: "not_found",
          message: "resource not found",
        });
      if (!executorMatch[2]) return json(response, 200, executor);
      if (executorMatch[2] === "/events")
        return json(response, 200, {
          items: [
            {
              event_id: "exec-event",
              event_type: "executor.healthy",
              status: "healthy",
              occurred_at: new Date(now).toISOString(),
              payload: {},
            },
          ],
          next_cursor: null,
        });
      return json(response, 200, {
        items: tasks.filter(
          (task) =>
            task.executor_id === executor.executor_id &&
            task.current_state === "running",
        ),
        next_cursor: null,
      });
    }
    if (route === "/launch-parameters" && request.method === "GET") {
      const scope = url.searchParams.get("scope");
      return json(response, 200, {
        items: scope
          ? launchParameters.filter((item) => item.scope === scope)
          : launchParameters,
        next_cursor: null,
      });
    }
    if (route === "/launch-parameters" && request.method === "POST") {
      const body = await readJSON(request);
      const created = {
        environment_id: `env-${launchParameters.length}`,
        team_id: teamId,
        revision: 1,
        secrets: [],
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
        ...body,
      };
      launchParameters.push(created);
      return json(response, 201, created);
    }
    const secretMatch = route.match(
      /^\/launch-parameters\/([^/]+)\/secrets\/([^/]+)(\/versions)?$/,
    );
    if (secretMatch) {
      const env = launchParameters.find(
        (item) => item.environment_id === secretMatch[1],
      );
      const secret = env?.secrets.find(
        (item) => item.secret_id === secretMatch[2],
      );
      if (!env || !secret)
        return json(response, 404, {
          code: "not_found",
          message: "resource not found",
        });
      if (secretMatch[3] === "/versions" && request.method === "GET")
        return json(response, 200, {
          items: Array.from({ length: secret.current_version }, (_, index) => ({
            version: index + 1,
            created_at: new Date(now + index).toISOString(),
          })),
          next_cursor: null,
        });
      if (!secretMatch[3] && request.method === "PUT") {
        await readJSON(request);
        secret.current_version += 1;
        return json(response, 200, secret);
      }
      if (!secretMatch[3] && request.method === "DELETE") {
        env.secrets.splice(env.secrets.indexOf(secret), 1);
        response.writeHead(204).end();
        return;
      }
    }
    const envMatch = route.match(
      /^\/launch-parameters\/([^/]+)(\/revisions|\/secrets)?$/,
    );
    if (envMatch) {
      const env = launchParameters.find(
        (item) => item.environment_id === envMatch[1],
      );
      if (!env)
        return json(response, 404, {
          code: "not_found",
          message: "resource not found",
        });
      if (!envMatch[2] && request.method === "PUT") {
        const body = await readJSON(request);
        Object.assign(env, body, {
          revision: env.revision + 1,
          updated_at: new Date().toISOString(),
        });
        return json(response, 200, env);
      }
      if (!envMatch[2] && request.method === "DELETE") {
        launchParameters.splice(launchParameters.indexOf(env), 1);
        response.writeHead(204).end();
        return;
      }
      if (envMatch[2] === "/revisions")
        return json(response, 200, {
          items: [
            { ...env, revision: env.revision, deleted: false },
            { ...env, revision: 1, deleted: false },
          ],
          next_cursor: null,
        });
      if (envMatch[2] === "/secrets" && request.method === "POST") {
        const body = await readJSON(request);
        const secret = {
          secret_id: `secret-${Date.now()}`,
          key: body.key,
          current_version: 1,
          revoked: false,
        };
        env.secrets.push(secret);
        return json(response, 201, secret);
      }
    }
    if (route === "/audit" && request.method === "GET") {
      const search = (url.searchParams.get("search") ?? "").toLowerCase();
      const items = audit.filter(
        (item) =>
          !search || JSON.stringify(item).toLowerCase().includes(search),
      );
      return json(response, 200, { items, next_cursor: null });
    }
    return json(response, 404, {
      code: "not_found",
      message: "resource not found",
    });
  });
  const wss = new WebSocketServer({ noServer: true });
  server.on("upgrade", (request, socket, head) => {
    const match = (request.url ?? "").match(
      /^\/ui\/v1\/teams\/([^/]+)\/stream/,
    );
    if (!match) return socket.destroy();
    wss.handleUpgrade(request, socket, head, (ws) => {
      const set = sockets.get(match[1]) ?? new Set<WebSocket>();
      set.add(ws);
      sockets.set(match[1], set);
      ws.on("close", () => set.delete(ws));
    });
  });
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => resolve());
  });
  const origin = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  return {
    origin,
    tasks,
    launchParameters,
    audit,
    push(teamId, frame) {
      for (const ws of sockets.get(teamId) ?? [])
        if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(frame));
    },
    close: () => closeServer(server, wss, sockets),
  };
}

function json(
  response: import("node:http").ServerResponse,
  status: number,
  value: unknown,
) {
  response.writeHead(status).end(JSON.stringify(value));
}
async function readJSON(request: import("node:http").IncomingMessage) {
  const chunks: Buffer[] = [];
  for await (const chunk of request) chunks.push(Buffer.from(chunk));
  return JSON.parse(Buffer.concat(chunks).toString("utf8") || "{}");
}
async function closeServer(
  server: Server,
  wss: WebSocketServer,
  sockets: Map<string, Set<WebSocket>>,
) {
  for (const set of sockets.values()) for (const ws of set) ws.terminate();
  wss.close();
  await new Promise<void>((resolve) => server.close(() => resolve()));
}

function isoWeekBuckets(requestedWeek: string | null, tasks: Task[]) {
  const today = new Date();
  const fallback = isoWeekKey(today);
  const match = /^(\d{4})-W(\d{2})$/.exec(requestedWeek ?? fallback);
  const year = Number(match?.[1] ?? fallback.slice(0, 4));
  const week = Number(match?.[2] ?? fallback.slice(6));
  const januaryFourth = new Date(Date.UTC(year, 0, 4));
  const monday = new Date(januaryFourth);
  monday.setUTCDate(
    januaryFourth.getUTCDate() -
      (januaryFourth.getUTCDay() || 7) +
      1 +
      (week - 1) * 7,
  );
  return ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"].map(
    (shortLabel, index) => {
      const date = new Date(monday);
      date.setUTCDate(monday.getUTCDate() + index);
      const dayTasks = tasks.filter(
        (task) =>
          new Date(task.ingested_at).toISOString().slice(0, 10) ===
          date.toISOString().slice(0, 10),
      );
      return {
        key: date.toISOString().slice(0, 10),
        label: shortLabel,
        shortLabel,
        total: dayTasks.length,
        counts: summarize(dayTasks),
        isFuture: date.getTime() > today.getTime(),
      };
    },
  );
}

function calendarMonthBuckets(requestedMonth: string | null, tasks: Task[]) {
  const current = new Date();
  const fallback = `${current.getUTCFullYear()}-${String(current.getUTCMonth() + 1).padStart(2, "0")}`;
  const [year, month] = (
    /^\d{4}-\d{2}$/.test(requestedMonth ?? "") ? requestedMonth! : fallback
  )
    .split("-")
    .map(Number);
  return Array.from(
    { length: new Date(Date.UTC(year, month, 0)).getUTCDate() },
    (_, index) => {
      const date = new Date(Date.UTC(year, month - 1, index + 1));
      const dayTasks = tasks.filter(
        (task) =>
          new Date(task.ingested_at).toISOString().slice(0, 10) ===
          date.toISOString().slice(0, 10),
      );
      return {
        key: date.toISOString().slice(0, 10),
        label: String(index + 1),
        shortLabel: String(index + 1),
        total: dayTasks.length,
        counts: summarize(dayTasks),
        isFuture: date.getTime() > current.getTime(),
      };
    },
  );
}

function summarize(tasks: Task[]) {
  const counts: Record<string, number> = {};
  for (const task of tasks)
    counts[task.current_state] = (counts[task.current_state] ?? 0) + 1;
  return counts;
}

function isoWeekKey(date: Date) {
  const utc = new Date(
    Date.UTC(date.getUTCFullYear(), date.getUTCMonth(), date.getUTCDate()),
  );
  utc.setUTCDate(utc.getUTCDate() + 4 - (utc.getUTCDay() || 7));
  const yearStart = new Date(Date.UTC(utc.getUTCFullYear(), 0, 1));
  return `${utc.getUTCFullYear()}-W${String(Math.ceil(((utc.getTime() - yearStart.getTime()) / 86_400_000 + 1) / 7)).padStart(2, "0")}`;
}
