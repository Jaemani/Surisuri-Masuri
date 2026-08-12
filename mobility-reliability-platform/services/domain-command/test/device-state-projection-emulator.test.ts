import { randomUUID } from 'node:crypto';
import { afterAll, beforeAll, describe, expect, test } from 'vitest';
import { deleteApp, initializeApp, type App } from 'firebase-admin/app';
import { Timestamp, getFirestore, type Firestore } from 'firebase-admin/firestore';
import { DomainCommandError } from '../src/canonical.js';
import { FirestoreDeviceStateProjectionStore } from '../src/device-state-projection-store.js';

const enabled = Boolean(process.env.FIRESTORE_EMULATOR_HOST);
const emulatorTest = enabled ? test : test.skip;
let app: App;
let db: Firestore;

beforeAll(() => { if (enabled) { app = initializeApp({ projectId: process.env.GCLOUD_PROJECT ?? 'demo-domain-command' }, `device-state-${randomUUID()}`); db = getFirestore(app); } });
afterAll(async () => { if (app) await deleteApp(app); });

const uuids = {
  tenant: '09460fa5-dc3c-4c14-9c00-d1956206292c',
  device: 'cd230e0f-a01c-47e5-830d-3a1330e591d6',
  event: 'f1a4ef7b-fb6f-47cf-919c-8734e469d2b1',
  repair: '0a7299a5-3f18-4f47-b0c8-6f1d9ef5ee60',
};

async function seed() {
  const tenantId = randomUUID();
  const deviceId = randomUUID();
  const tenant = db.collection('tenants').doc(tenantId);
  await tenant.set({ tenant_id: tenantId, status: 'active' });
  await tenant.collection('devices').doc(deviceId).set({ tenant_id: tenantId, device_id: deviceId, status: 'active' });
  await tenant.collection('deviceStateEvents').doc(uuids.event).set({
    schema_version: 'device-state-event.v1', event_id: uuids.event, event_type: 'repair.recorded', tenant_id: tenantId, device_id: deviceId,
    occurred_at: Timestamp.fromDate(new Date('2026-08-13T09:00:00Z')), recorded_at: Timestamp.fromDate(new Date('2026-08-13T09:12:30Z')),
    source_quality: 'verified', payload: { repair_id: uuids.repair },
  });
  return { tenantId, deviceId, replayRunId: `run-${randomUUID()}` };
}

describe('Firestore device current-state shadow promotion', () => {
  emulatorTest('promotes current state and checkpoint atomically and exact replay converges', async () => {
    const scope = await seed();
    const store = new FirestoreDeviceStateProjectionStore(db);
    const first = await store.rebuild(scope);
    expect(first).toMatchObject({ status: 'promoted', revision: 1 });

    const tenant = db.collection('tenants').doc(scope.tenantId);
    const current = await tenant.collection('devices').doc(scope.deviceId).collection('state').doc('current').get();
    const checkpoint = await tenant.collection('projectionCheckpoints').doc(`device-current-state--${scope.deviceId}`).get();
    expect(current.data()).toMatchObject({ revision: 1, replay_run_id: scope.replayRunId, input_hash: first.inputHash, output_checksum: first.state.canonicalChecksum });
    expect(checkpoint.data()).toMatchObject({ revision: 1, replay_run_id: scope.replayRunId, input_hash: first.inputHash, output_checksum: first.state.canonicalChecksum });

    const second = await store.rebuild(scope);
    expect(second).toMatchObject({ status: 'replayed', revision: 1, shadowId: first.shadowId });
  });

  emulatorTest('rejects the same replay run with changed input and preserves authoritative state', async () => {
    const scope = await seed();
    const store = new FirestoreDeviceStateProjectionStore(db);
    const prepared = await store.prepare(scope);
    await db.collection('tenants').doc(scope.tenantId).collection('deviceStateEvents').doc(randomUUID()).set({
      schema_version: 'device-state-event.v1', event_id: randomUUID(), event_type: 'repair.recorded', tenant_id: scope.tenantId, device_id: scope.deviceId,
      occurred_at: Timestamp.fromDate(new Date('2026-08-14T09:00:00Z')), recorded_at: Timestamp.fromDate(new Date('2026-08-14T09:12:30Z')),
      source_quality: 'verified', payload: { repair_id: randomUUID() },
    });
    await expect(store.promote(scope, prepared.shadowId)).rejects.toMatchObject({ code: 'CORRUPT_SHADOW' } satisfies Partial<DomainCommandError>);
    const current = await db.doc(`tenants/${scope.tenantId}/devices/${scope.deviceId}/state/current`).get();
    const checkpoint = await db.doc(`tenants/${scope.tenantId}/projectionCheckpoints/device-current-state--${scope.deviceId}`).get();
    expect(current.exists).toBe(false);
    expect(checkpoint.exists).toBe(false);
  });

  emulatorTest('rejects a corrupt shadow and leaves current and checkpoint absent', async () => {
    const scope = await seed();
    const store = new FirestoreDeviceStateProjectionStore(db);
    const prepared = await store.prepare(scope);
    await db.doc(`tenants/${scope.tenantId}/devices/${scope.deviceId}/stateVersions/${prepared.shadowId}`).update({ output_checksum: 'tampered' });
    await expect(store.promote(scope, prepared.shadowId)).rejects.toMatchObject({ code: 'CORRUPT_SHADOW' });
    expect((await db.doc(`tenants/${scope.tenantId}/devices/${scope.deviceId}/state/current`).get()).exists).toBe(false);
    expect((await db.doc(`tenants/${scope.tenantId}/projectionCheckpoints/device-current-state--${scope.deviceId}`).get()).exists).toBe(false);
  });
});
