import { readFile } from "node:fs/promises";

type DemoState = {
  registry_url: string;
  team_id: string;
  source_system_id: string;
  listener_identity: string;
  task_type_id: string;
};

async function main(): Promise<void> {
  const prompt = process.argv.slice(2).join(" ").trim();
  if (!prompt) throw new Error("Укажите промпт после --");
  const statePath = process.env.FLOWAI_DEMO_STATE ?? "/tmp/flowai-demo.json";
  const state = JSON.parse(await readFile(statePath, "utf8")) as DemoState;
  const response = await fetch(`${state.registry_url}/v1/tasks`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "X-FlowAI-Role": "listener",
      "X-FlowAI-Team-Id": state.team_id,
      "X-FlowAI-Source-System-Id": state.source_system_id,
      "X-FlowAI-Listener-Identity": state.listener_identity,
      "X-FlowAI-Request-Id": `demo-${crypto.randomUUID()}`,
    },
    body: JSON.stringify({
      team_id: state.team_id,
      source_system_id: state.source_system_id,
      source_id: `demo-${crypto.randomUUID()}`,
      task_type_id: state.task_type_id,
      payload: { prompt },
      image: null,
    }),
  });
  const body = await response.text();
  if (response.status !== 201)
    throw new Error(`State Registry returned ${response.status}: ${body}`);
  const task = JSON.parse(body) as { task_id: string };
  process.stdout.write(`Задача создана: ${task.task_id}\n`);
  process.stdout.write(
    `Откройте «История» → ${task.task_id}, чтобы смотреть выполнение и live log.\n`,
  );
}

main().catch((error) => {
  process.stderr.write(
    `${error instanceof Error ? error.message : String(error)}\n`,
  );
  process.exit(1);
});
