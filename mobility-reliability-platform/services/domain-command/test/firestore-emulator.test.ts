import { randomUUID } from 'node:crypto';
import { afterAll, beforeAll, describe, expect, test } from 'vitest';
import { initializeApp, deleteApp, type App } from 'firebase-admin/app';
import { Timestamp, getFirestore, type Firestore } from 'firebase-admin/firestore';
import { bodyHash, DomainCommandError } from '../src/canonical.js';
import { FirestoreDomainCommandStore } from '../src/firebase-store.js';
import type { ActorContext, AppendSubsidyTransactionCommand } from '../src/types.js';

const enabled = Boolean(process.env.FIRESTORE_EMULATOR_HOST);
const emulatorTest = enabled ? test : test.skip;
let app: App;
let db: Firestore;

beforeAll(() => {
  if (!enabled) return;
  app = initializeApp({ projectId: process.env.GCLOUD_PROJECT ?? 'demo-domain-command' }, `domain-command-${randomUUID()}`);
  db = getFirestore(app);
});

afterAll(async () => {
  if (app) await deleteApp(app);
});

function actor(tenantId: string, role: ActorContext['roles'][number], uid = `${role}-uid`, personId?: string): ActorContext {
  return { uid, tenantId, roles: [role], ...(personId ? { personId } : {}) };
}

async function seed(tenantId: string) {
  const tenant = db.collection('tenants').doc(tenantId);
  await tenant.set({ tenant_id: tenantId, status: 'active' });
  await tenant.collection('people').doc('person-1').set({ tenant_id: tenantId, person_id: 'person-1' });
  await tenant.collection('devices').doc('device-1').set({ tenant_id: tenantId, device_id: 'device-1' });
  await tenant.collection('deviceAssignments').doc('assignment-1').set({ tenant_id: tenantId, person_id: 'person-1', device_id: 'device-1', status: 'active' });
  await tenant.collection('memberships').doc('repairer-1').set({ tenant_id: tenantId, firebase_uid: 'repairer-1', roles: ['repairer'], status: 'active', valid_from: Timestamp.fromMillis(Date.now() - 60_000) });
  await tenant.collection('repairStations').doc('station-1').set({ tenant_id: tenantId, repair_station_id: 'station-1', status: 'active' });
  await tenant.collection('subsidyPolicies').doc('policy-1').set({ tenant_id: tenantId, policy_version_id: 'policy-1', status: 'active' });
}

async function create(store: FirestoreDomainCommandStore, tenantId: string, key = 'create-emulator-001') {
  const command = { beneficiaryId: 'person-1', deviceId: 'device-1', issueSummary: '브레이크가 늦게 잡혀요', publicFundingInvolved: true, requestedAmountKrw: 180000 };
  return store.createRepair({ actor: actor(tenantId, 'case_worker'), command, idempotencyKey: key, bodyHash: bodyHash(command) });
}

describe('Firestore command adapter', () => {
  emulatorTest('writes canonical work-order paths and replays an identical command', async () => {
    const tenantId = `tenant-${randomUUID()}`;
    await seed(tenantId);
    const store = new FirestoreDomainCommandStore(db);
    const first = await create(store, tenantId);
    const replay = await create(store, tenantId);
    expect(replay.idempotent).toBe(true);
    const workOrder = await db.doc(`tenants/${tenantId}/repairWorkOrders/${first.resourceId}`).get();
    expect(workOrder.data()).toMatchObject({ tenant_id: tenantId, requester_person_id: 'person-1', work_order_id: first.resourceId, status: 'requested' });
    expect(workOrder.data()).not.toHaveProperty('tenantId');
    expect((await workOrder.ref.collection('statusHistory').get()).size).toBe(1);
    expect((await db.collection(`tenants/${tenantId}/domainEvents`).get()).size).toBe(1);
  });

  emulatorTest('rejects body conflict and concurrent stale transitions', async () => {
    const tenantId = `tenant-${randomUUID()}`;
    await seed(tenantId);
    const store = new FirestoreDomainCommandStore(db);
    const created = await create(store, tenantId, 'create-emulator-002');
    const different = { beneficiaryId: 'person-1', deviceId: 'device-1', issueSummary: '다른 증상', publicFundingInvolved: true, requestedAmountKrw: 180000 };
    await expect(store.createRepair({ actor: actor(tenantId, 'case_worker'), command: different, idempotencyKey: 'create-emulator-002', bodyHash: bodyHash(different) })).rejects.toMatchObject({ code: 'IDEMPOTENCY_CONFLICT' });
    const transitions = ['transition-emulator-a', 'transition-emulator-b'].map((key) => store.transitionRepair({ actor: actor(tenantId, 'case_worker'), command: { repairRequestId: created.resourceId, toStatus: 'under_review', expectedRevision: 1 }, idempotencyKey: key, bodyHash: bodyHash({ key }) }));
    const outcomes = await Promise.allSettled(transitions);
    expect(outcomes.filter((value) => value.status === 'fulfilled')).toHaveLength(1);
    const rejected = outcomes.find((value): value is PromiseRejectedResult => value.status === 'rejected');
    expect(rejected?.reason).toMatchObject({ code: 'REVISION_CONFLICT' });
  });

  emulatorTest('enforces repairer assignment and stores a person-scoped subsidy ledger', async () => {
    const tenantId = `tenant-${randomUUID()}`;
    await seed(tenantId);
    const store = new FirestoreDomainCommandStore(db);
    const created = await create(store, tenantId, 'create-emulator-003');
    await store.transitionRepair({ actor: actor(tenantId, 'case_worker'), command: { repairRequestId: created.resourceId, toStatus: 'under_review', expectedRevision: 1 }, idempotencyKey: 'transition-emulator-01', bodyHash: '1' });
    await store.transitionRepair({ actor: actor(tenantId, 'case_worker'), command: { repairRequestId: created.resourceId, toStatus: 'assigned', expectedRevision: 2, repairStationId: 'station-1', repairerFirebaseUid: 'repairer-1' }, idempotencyKey: 'transition-emulator-02', bodyHash: '2' });
    await expect(store.transitionRepair({ actor: actor(tenantId, 'repairer', 'repairer-other'), command: { repairRequestId: created.resourceId, toStatus: 'scheduled', expectedRevision: 3 }, idempotencyKey: 'transition-emulator-03', bodyHash: '3' })).rejects.toMatchObject({ code: 'REPAIR_ASSIGNMENT_REQUIRED' });
    const scheduledAt = new Date(Date.now() + 86_400_000).toISOString();
    await store.transitionRepair({ actor: actor(tenantId, 'repairer', 'repairer-1'), command: { repairRequestId: created.resourceId, toStatus: 'scheduled', expectedRevision: 3, scheduledAt }, idempotencyKey: 'transition-emulator-04', bodyHash: '4' });
    const scheduledWorkOrder = await db.doc(`tenants/${tenantId}/repairWorkOrders/${created.resourceId}`).get();
    expect((scheduledWorkOrder.get('scheduled_at') as Timestamp).toMillis()).toBe(new Date(scheduledAt).getTime());

    const scope = { accountId: 'account-1', personId: 'person-1', policyVersionId: 'policy-1', reasonCode: 'repair_support' };
    const append = async (command: AppendSubsidyTransactionCommand, key: string) => store.appendSubsidy({ actor: actor(tenantId, 'case_worker'), command, idempotencyKey: key, bodyHash: bodyHash(command) });
    await append({ ...scope, transactionType: 'allocation', amountKrw: 500000 }, 'ledger-emulator-01');
    await append({ ...scope, workOrderId: created.resourceId, transactionType: 'reservation', amountKrw: 180000 }, 'ledger-emulator-02');
    const summary = await db.doc(`tenants/${tenantId}/subsidyAccounts/account-1`).get();
    expect(summary.data()).toMatchObject({ tenant_id: tenantId, person_id: 'person-1', available_krw: 320000, reserved_krw: 180000 });
    const entries = await summary.ref.collection('transactions').get();
    expect(entries.size).toBe(2);
    expect(entries.docs[0]?.data()).not.toHaveProperty('transactionId');
  });

  emulatorTest('fails closed across tenant and account-person boundaries', async () => {
    const tenantId = `tenant-${randomUUID()}`;
    await seed(tenantId);
    const otherTenantId = `tenant-${randomUUID()}`;
    await seed(otherTenantId);
    const store = new FirestoreDomainCommandStore(db);
    const command = { beneficiaryId: 'person-1', deviceId: 'device-1', issueSummary: '점검 필요', publicFundingInvolved: false };
    await db.doc(`tenants/${tenantId}/devices/device-1`).delete();
    await expect(store.createRepair({ actor: actor(tenantId, 'case_worker'), command, idempotencyKey: 'cross-tenant-001', bodyHash: bodyHash(command) })).rejects.toSatisfy((error: unknown) => error instanceof DomainCommandError && error.code === 'DEVICE_NOT_FOUND');
  });

  emulatorTest('validates the repair station and materializes an immutable completed repair', async () => {
    const tenantId = `tenant-${randomUUID()}`;
    await seed(tenantId);
    const store = new FirestoreDomainCommandStore(db);
    const created = await create(store, tenantId, 'create-emulator-complete');
    const worker = actor(tenantId, 'case_worker');
    const repairer = actor(tenantId, 'repairer', 'repairer-1');
    const transition = (currentActor: ActorContext, command: Parameters<FirestoreDomainCommandStore['transitionRepair']>[0]['command'], key: string) => store.transitionRepair({ actor: currentActor, command, idempotencyKey: key, bodyHash: bodyHash(command) });

    await transition(worker, { repairRequestId: created.resourceId, toStatus: 'under_review', expectedRevision: 1 }, 'complete-01');
    await expect(transition(worker, { repairRequestId: created.resourceId, toStatus: 'assigned', expectedRevision: 2, repairStationId: 'missing-station', repairerFirebaseUid: 'repairer-1' }, 'complete-invalid-station')).rejects.toMatchObject({ code: 'REPAIR_STATION_NOT_FOUND' });
    await transition(worker, { repairRequestId: created.resourceId, toStatus: 'assigned', expectedRevision: 2, repairStationId: 'station-1', repairerFirebaseUid: 'repairer-1' }, 'complete-02');
    const scheduledAt = new Date(Date.now() + 86_400_000).toISOString();
    await transition(repairer, { repairRequestId: created.resourceId, toStatus: 'scheduled', expectedRevision: 3, scheduledAt }, 'complete-03');
    await transition(repairer, { repairRequestId: created.resourceId, toStatus: 'in_progress', expectedRevision: 4 }, 'complete-04');
    await transition(repairer, { repairRequestId: created.resourceId, toStatus: 'repairer_submitted', expectedRevision: 5, billedAmountKrw: 170000, workItems: [{ categoryCode: 'brakes', actionCode: 'repair', quantity: 1, lineAmountKrw: 170000 }] }, 'complete-05');
    await db.doc(`tenants/${tenantId}/subsidyAccounts/account-1`).set({ tenant_id: tenantId, account_id: 'account-1', person_id: 'person-1', policy_version_id: 'policy-1', allocated_krw: 500000, adjustment_krw: 0, reserved_krw: 0, executed_krw: 0, available_krw: 500000, reserved_by_work_order: {}, status: 'active' });
    await transition(worker, { repairRequestId: created.resourceId, toStatus: 'center_verified', expectedRevision: 6, subsidyDecisionId: 'decision-1', subsidyAccountId: 'account-1' }, 'complete-06');
    const completed = await transition(worker, { repairRequestId: created.resourceId, toStatus: 'completed', expectedRevision: 7 }, 'complete-07');

    const repairs = await db.collection(`tenants/${tenantId}/repairs`).get();
    expect(repairs.size).toBe(1);
    expect(repairs.docs[0]?.data()).toMatchObject({ repair_id: completed.eventId, work_order_id: created.resourceId, tenant_id: tenantId, device_id: 'device-1', repair_station_id: 'station-1', repairer_membership_uid: 'repairer-1', status: 'completed', billed_amount_krw: 170000, source_quality: 'verified' });
    expect(repairs.docs[0]?.data()).not.toHaveProperty('issue_summary');
    expect(repairs.docs[0]?.data()).not.toHaveProperty('memo');
    const items = await repairs.docs[0]!.ref.collection('items').get();
    expect(items.size).toBe(1);
    expect(items.docs[0]?.data()).toMatchObject({ tenant_id: tenantId, repair_id: completed.eventId, category_code: 'brakes', action_code: 'repair', quantity: 1, line_amount_krw: 170000, source_quality: 'verified' });
    expect(items.docs[0]?.data()).not.toHaveProperty('detail_text');
  });
});
