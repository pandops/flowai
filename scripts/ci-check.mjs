#!/usr/bin/env node
// scripts/ci-check.mjs
// Aggregates local CI commands declared in .omo/artifacts/command-manifest.yaml.
// Exits non-zero on any failure so it can be wired both as a pnpm script
// and as the `ci_check` job in .gitlab-ci.yml.
import { spawnSync } from 'node:child_process';

const ROOT = new URL('..', import.meta.url).pathname;
const MANIFEST = new URL('../.omo/artifacts/command-manifest.yaml', import.meta.url);

const checks = [
  { name: 'format_check', argv: ['npm', ['run', '--silent', 'format:check']], allowSkip: false },
  { name: 'lint', argv: ['npm', ['run', '--silent', 'lint']], allowSkip: false },
  { name: 'typecheck', argv: ['npm', ['run', '--silent', 'typecheck']], allowSkip: false },
  { name: 'unit_test', argv: ['npm', ['run', '--silent', 'test']], allowSkip: false },
  { name: 'go_vet', argv: ['npm', ['run', '--silent', 'go:vet']], allowSkip: true },
  { name: 'go_test', argv: ['npm', ['run', '--silent', 'go:test']], allowSkip: true },
  { name: 'helm_lint', argv: ['npm', ['run', '--silent', 'helm:lint']], allowSkip: false },
  { name: 'k3d_smoke', argv: ['npm', ['run', '--silent', 'k3d:smoke']], allowSkip: true, skipCode: 78 },
];

let failed = 0;
const startedAt = Date.now();
console.log(`[ci-check] using manifest hint: ${MANIFEST.pathname}`);

for (const check of checks) {
  const [cmd, args] = check.argv;
  process.stdout.write(`[ci-check] -> ${check.name} (${cmd} ${args.join(' ')})\n`);
  const res = spawnSync(cmd, args, { cwd: ROOT, stdio: 'inherit', env: process.env });
  if (res.status === 0) {
    process.stdout.write(`[ci-check] OK ${check.name}\n`);
    continue;
  }
  if (check.allowSkip && res.status === check.skipCode) {
    process.stdout.write(`[ci-check] SKIP ${check.name} (declared skip code ${check.skipCode})\n`);
    continue;
  }
  process.stderr.write(`[ci-check] FAIL ${check.name} (exit ${res.status})\n`);
  failed += 1;
}

const elapsed = ((Date.now() - startedAt) / 1000).toFixed(2);
if (failed > 0) {
  console.error(`[ci-check] ${failed} check(s) failed in ${elapsed}s`);
  process.exit(1);
}
console.log(`[ci-check] all checks passed in ${elapsed}s`);
