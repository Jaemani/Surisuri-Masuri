import { describe, expect, test } from 'vitest';
import { getConsoleOperationsSnapshotHandler, getMobileProductSnapshotHandler } from '../src/projection-http.js';
import type { MobileProductSnapshot, ProductProjectionStore } from '../src/projection-types.js';
import type { ActorContext } from '../src/types.js';

const actor: ActorContext = { uid: 'worker-1', tenantId: 'tenant-a', roles: ['case_worker'], personId: 'person-1' };
const mobile: MobileProductSnapshot = { roleSession: { role: 'user', displayName: '이용자 00000001 님', isDemo: false }, repairRequest: null, device: { id: 'device-1', name: '전동보장구', registrationNumber: 'MOB-1', registeredAt: '2026년 등록', status: 'healthy', timeline: [] }, subsidy: { program: '수리 지원금', cycle: '현재', used: 0, total: 0, nextReview: '미정', note: '없음' } };

function response() {
  const state: { status?: number; body?: unknown; headers: Record<string, string> } = { headers: {} };
  return { state, value: { status(code: number) { state.status = code; return this; }, json(body: unknown) { state.body = body; return body; }, set(field: string, value: string) { state.headers[field] = value; return this; } } };
}

const store: ProductProjectionStore = { getMobileSnapshot: async () => mobile, getConsoleProjection: async (_actor, projection) => ({ projection }) };
const resolveActor = async () => actor;

describe('projection HTTP boundary', () => {
  test('mobile snapshot requires GET, ID token, App Check, and an exact tenant scope', async () => {
    const missing = response();
    await getMobileProductSnapshotHandler({ method: 'GET', headers: {}, query: { tenantId: 'tenant-a' } }, missing.value, { store, resolveActor });
    expect(missing.state).toMatchObject({ status: 401, body: { error: { code: 'AUTH_REQUIRED' } } });
    const duplicate = response();
    await getMobileProductSnapshotHandler({ method: 'GET', headers: { authorization: 'Bearer id', 'x-firebase-appcheck': 'app', 'x-tenant-id': 'tenant-a' }, query: { tenantId: 'tenant-a' } }, duplicate.value, { store, resolveActor });
    expect(duplicate.state.status).toBe(400);
    expect(duplicate.state.body).toMatchObject({ error: { code: 'DUPLICATE_TENANT_SCOPE' } });
  });

  test('returns a private no-store mobile DTO without raw infrastructure fields', async () => {
    const output = response();
    await getMobileProductSnapshotHandler({ method: 'GET', headers: { authorization: 'Bearer id', 'x-firebase-appcheck': 'app' }, query: { tenantId: 'tenant-a' } }, output.value, { store, resolveActor });
    expect(output.state.status).toBe(200);
    expect(output.state.headers['Cache-Control']).toContain('no-store');
    expect(output.state.body).toEqual(mobile);
    const serialized = JSON.stringify(output.state.body);
    for (const banned of ['phone_e164', 'birth_date', 'address_text', 'firebase_uid', 'latitude', 'longitude', 'object_path']) expect(serialized).not.toContain(banned);
  });

  test('console endpoint accepts only a supported purpose projection', async () => {
    const ok = response();
    await getConsoleOperationsSnapshotHandler({ method: 'GET', headers: { authorization: 'Bearer id', 'x-firebase-appcheck': 'app', 'x-tenant-id': 'tenant-a' }, query: { projection: 'repairs' } }, ok.value, { store, resolveActor });
    expect(ok.state.body).toEqual({ projection: 'repairs' });
    const invalid = response();
    await getConsoleOperationsSnapshotHandler({ method: 'GET', headers: { authorization: 'Bearer id', 'x-firebase-appcheck': 'app', 'x-tenant-id': 'tenant-a' }, query: { projection: 'rawTrips' } }, invalid.value, { store, resolveActor });
    expect(invalid.state).toMatchObject({ status: 400, body: { error: { code: 'INVALID_PROJECTION' } } });
  });
});
