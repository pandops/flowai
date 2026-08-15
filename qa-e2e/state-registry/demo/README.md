# FlowAI real OpenHands demo

This local-only demo starts an ephemeral PostgreSQL database, the real State
Registry, one team-scoped Docker OpenHands Executor, the v0006 mocked proxy,
and the Web UI. The UI is exposed on port `4173`; State Registry and the
Executor remain bound to loopback.

Start the stack:

```sh
cd qa-e2e/state-registry
npm run demo:start
```

In «Параметры», add team-scoped variables `OPENAI_MODEL` and
`OPENAI_BASE_URL`, plus a team-scoped secret named `OPENAI_API_KEY`. The
Executor reads all three values only from the immutable task snapshot. Secret plaintext
is accepted once, encrypted by State Registry, omitted from reads, and opened
only for the assigned task.

Submit a prompt through the listener surface (task creation intentionally does
not exist in the Web UI):

```sh
npm run demo:prompt -- "Inspect the workspace and explain what you find"
```

Open «Задачи», select the printed `task_id`, and observe the real lifecycle and
live OpenHands output. Stop the stack with `Ctrl+C`; all demo state is
ephemeral. Override the model with `FLOWAI_DEMO_MODEL` and the public UI port
with `FLOWAI_DEMO_PORT`.

This is an unauthenticated v0006 development stand. Expose it only on a trusted
local network and never reuse a production credential.
