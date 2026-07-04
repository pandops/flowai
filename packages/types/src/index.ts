// packages/types - shared TypeScript types across the FlowAI v2 monorepo.
// Real domain types are added in later waves per ADR 0010 backlog.

export interface HealthStatus {
  status: 'ok' | 'degraded' | 'down';
  service: string;
  version: string;
}

export const SERVICE_VERSION = '0.1.0';
