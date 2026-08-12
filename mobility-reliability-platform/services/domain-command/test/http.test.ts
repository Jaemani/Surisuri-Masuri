import { describe, expect, test } from 'vitest';
import { createRepairRequestHandler, transitionRepairRequestHandler } from '../src/http.js';
import { bodyHash } from '../src/canonical.js';
import { InMemoryCommandStore } from '../src/store.js';
import type { ActorContext } from '../src/types.js';

const actor: ActorContext = { uid: 'worker-1', tenantId: 'tenant-a', roles: ['case_worker'] };

function makeResponse() {
  const result: { statusCode?: number; payload?: unknown } = {};
  return {
    result,
    response: {
      status(code: number) { result.statusCode = code; return this; },
      json(payload: unknown) { result.payload = payload; return payload; },
    },
  };
}

describe('HTTP trust boundary', () => {
  test('requires ID token, App Check, and idempotency key before command execution', async () => {
    const { response, result } = makeResponse();
    const store = new InMemoryCommandStore();
    await createRepairRequestHandler({ method: 'POST', headers: { authorization: 'Bearer token', 'idempotency-key': 'http-001' }, body: { tenantId: 'tenant-a' } }, response, { store, resolveActor: async () => actor });
    expect(result.statusCode).toBe(401);
    expect(result.payload).toEqual({ error: { code: 'APP_CHECK_REQUIRED', message: 'A Firebase App Check token is required.' } });
  });

  test('passes a server-derived actor to the command store and returns its result', async () => {
    const { response, result } = makeResponse();
    const store = new InMemoryCommandStore();
    const command = { beneficiaryId: 'person-1', deviceId: 'device-1', issueSummary: '브레이크 점검', publicFundingInvolved: false };
    await createRepairRequestHandler({ method: 'POST', headers: { authorization: 'Bearer token', 'x-firebase-appcheck': 'app-check', 'idempotency-key': 'http-002' }, body: { tenantId: 'tenant-a', ...command } }, response, { store, resolveActor: async (_id, _app, tenantCandidate) => ({ ...actor, tenantId: String(tenantCandidate) }) });
    expect(result.statusCode).toBe(201);
    expect(result.payload).toMatchObject({ commandType: 'create_repair_request', tenantId: 'tenant-a', status: 'requested' });
    expect(bodyHash({ tenantId: 'tenant-a', ...command })).toHaveLength(64);
  });

  test('rejects transition over-posting and client-controlled submission time', async () => {
    const store = new InMemoryCommandStore();
    const created = await store.createRepair({ actor, command: { beneficiaryId: 'person-1', deviceId: 'device-1', issueSummary: '브레이크 점검', publicFundingInvolved: false }, idempotencyKey: 'seed-transition', bodyHash: 'seed' });
    const request = (body: Record<string, unknown>) => ({ method: 'POST', headers: { authorization: 'Bearer token', 'x-firebase-appcheck': 'app-check', 'idempotency-key': `transition-${String(body.toStatus)}` }, body: { tenantId: 'tenant-a', repairRequestId: created.resourceId, expectedRevision: 1, ...body } });

    const reassignment = makeResponse();
    await transitionRepairRequestHandler(request({ toStatus: 'scheduled', scheduledAt: new Date(Date.now() + 86_400_000).toISOString(), repairerFirebaseUid: 'attacker' }), reassignment.response, { store, resolveActor: async () => ({ ...actor, roles: ['repairer'], uid: 'repairer-1' }) });
    expect(reassignment.result).toMatchObject({ statusCode: 400, payload: { error: { code: 'UNEXPECTED_COMMAND_FIELD' } } });

    const forgedTime = makeResponse();
    await transitionRepairRequestHandler(request({ toStatus: 'repairer_submitted', billedAmountKrw: 10000, submittedAt: '2020-01-01T00:00:00.000Z' }), forgedTime.response, { store, resolveActor: async () => ({ ...actor, roles: ['repairer'], uid: 'repairer-1' }) });
    expect(forgedTime.result).toMatchObject({ statusCode: 400, payload: { error: { code: 'UNEXPECTED_COMMAND_FIELD' } } });
  });
});
