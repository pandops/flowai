// api-gateway entrypoint - placeholder per ADR 0002 / 0009.
// Real implementation lands in a later wave.
import { createServer } from 'node:http';

const PORT = Number(process.env['PORT'] ?? 8080);

export function buildApp() {
  return createServer((req, res) => {
    if (req.url === '/healthz') {
      res.writeHead(200, { 'content-type': 'application/json' });
      res.end(JSON.stringify({ status: 'ok', service: 'api-gateway' }));
      return;
    }
    res.writeHead(404, { 'content-type': 'application/json' });
    res.end(JSON.stringify({ error: 'not_found' }));
  });
}

if (import.meta.url === `file://${process.argv[1]}`) {
  buildApp().listen(PORT, () => {
    // eslint-disable-next-line no-console
    console.log(`[api-gateway] listening on :${PORT}`);
  });
}
