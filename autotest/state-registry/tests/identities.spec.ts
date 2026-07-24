// Identity-fixture immutability and team-binding contract.
// This spec verifies that:
//   1. team-owned identity fixtures (listener, team-owned Executor,
//      trusted Gateway) carry their teamId and emit X-FlowAI-Team-Id
//   2. team-less identity fixtures (system-owned Executor, system
//      administrator) carry NO teamId and emit NO X-FlowAI-Team-Id
//   3. attach() returns an immutable snapshot that callers cannot
//      mutate to widen team authority
import { test, expect } from '@playwright/test';
import {
  listenerTeamA,
  listenerTeamB,
  teamExecutorTeamA,
  teamExecutorTeamB,
  systemExecutor,
  gatewayTeamA,
  gatewayTeamB,
  systemAdministrator,
  TeamA,
  TeamB,
  type IdentityContext,
} from '../fixtures/identities';

function expectTeamBound(
  identity: IdentityContext,
  expectedTeamId: string,
): void {
  expect(identity.teamId, `${identity.role} must carry teamId`).toBe(expectedTeamId);
  const headers = identity.attach();
  expect(headers['X-FlowAI-Team-Id'], `${identity.role} must emit X-FlowAI-Team-Id`).toBe(expectedTeamId);
}

function expectTeamless(identity: IdentityContext): void {
  expect(identity.teamId, `${identity.role} must NOT carry teamId`).toBeUndefined();
  const headers = identity.attach();
  expect(
    headers['X-FlowAI-Team-Id'],
    `${identity.role} must NOT emit X-FlowAI-Team-Id`,
  ).toBeUndefined();
}

test.describe('state-registry identity fixtures', () => {
  test('team-owned listeners and Executors emit X-FlowAI-Team-Id', () => {
    expectTeamBound(listenerTeamA(), TeamA);
    expectTeamBound(listenerTeamB(), TeamB);
    expectTeamBound(teamExecutorTeamA(), TeamA);
    expectTeamBound(teamExecutorTeamB(), TeamB);
  });

  test('trusted Gateway forwards operator context per team', () => {
    expectTeamBound(gatewayTeamA(), TeamA);
    expectTeamBound(gatewayTeamB(), TeamB);
    expect(gatewayTeamA().attach()['X-FlowAI-Operator-Id']).toBe(`op-${TeamA}`);
    expect(gatewayTeamB().attach()['X-FlowAI-Operator-Id']).toBe(`op-${TeamB}`);
    expect(gatewayTeamA().attach()['X-FlowAI-Team-Name']).toBe(`Team ${TeamA}`);
  });

  test('system-owned Executor carries no team binding', () => {
    expectTeamless(systemExecutor());
    expect(systemExecutor().attach()['X-FlowAI-Executor-Id']).toBeTruthy();
  });

  test('system administrator carries no team binding', () => {
    expectTeamless(systemAdministrator());
    expect(systemAdministrator().attach()['X-FlowAI-Admin-Subject']).toBeTruthy();
  });

  test('attach() returns an immutable header snapshot', () => {
    const headers = listenerTeamA().attach();
    expect(() => {
      (headers as Record<string, string>)['X-FlowAI-Team-Id'] = 'team-impersonator';
    }).toThrow();
    const refreshed = listenerTeamA().attach();
    expect(refreshed['X-FlowAI-Team-Id']).toBe(TeamA);
  });
});