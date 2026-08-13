# FlowAI end-to-end tests

Cross-service Playwright tests live under `qa-e2e/state-registry/`.
They exercise the durable State Registry together with the concrete Docker
Executor through public HTTP surfaces. The removed mocked task server and its
Router/Env Registry test suite are not part of the runtime or test topology.

Run the complete suite:

```bash
cd qa-e2e/state-registry
npm test
```

Generated HTML, JSON, API, video, and trace artifacts are written below
`qa-e2e/reports/` and are ignored by Git. Open the HTML report with:

```bash
npx playwright show-report ../reports/api/state-registry
```

Open a trace with:

```bash
npx playwright show-trace ../reports/video/state-registry/<test>/trace.zip
```

Implementation-active E2E case definitions live in `qa-e2e/test-cases/`.
