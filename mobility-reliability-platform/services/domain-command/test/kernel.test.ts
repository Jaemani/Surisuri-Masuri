import assert from 'node:assert/strict';
import { test } from 'vitest';
import { DomainCommandError, bodyHash } from '../src/canonical.js';
import { InMemoryCommandStore } from '../src/store.js';
import type { ActorContext } from '../src/types.js';

const worker: ActorContext = { uid: 'worker-1', tenantId: 'tenant-a', roles: ['case_worker'] };
const repairer: ActorContext = { uid: 'repairer-1', tenantId: 'tenant-a', roles: ['repairer'] };
const ledgerScope = { accountId: 'account-1', personId: 'person-1', policyVersionId: 'policy-2026', reasonCode: 'repair_support' };
const futureAppointment = () => new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString();

function createCommand(publicFundingInvolved = true) {
  return { beneficiaryId: 'person-1', deviceId: 'device-1', issueSummary: '주행 중 좌측 쏠림', publicFundingInvolved, ...(publicFundingInvolved ? { requestedAmountKrw: 180000 } : {}) };
}

async function createRequest(store: InMemoryCommandStore) {
  const command = createCommand();
  return store.createRepair({ actor: worker, command, idempotencyKey: 'create-001', bodyHash: bodyHash(command) });
}

test('create is server-scoped, auditable, and idempotent', async () => {
  const store = new InMemoryCommandStore();
  const command = createCommand(false);
  const first = await store.createRepair({ actor: worker, command, idempotencyKey: 'create-002', bodyHash: bodyHash(command) });
  const replay = await store.createRepair({ actor: worker, command, idempotencyKey: 'create-002', bodyHash: bodyHash(command) });
  assert.equal(first.revision, 1);
  assert.equal(replay.idempotent, true);
  assert.equal(store.getEvents().length, 1);
  assert.equal(store.getRepair(first.resourceId)?.createdByUid, 'worker-1');
});

test('idempotency key cannot be reused for a different body', async () => {
  const store = new InMemoryCommandStore();
  const first = createCommand(false);
  await store.createRepair({ actor: worker, command: first, idempotencyKey: 'create-003', bodyHash: bodyHash(first) });
  const different = { ...first, issueSummary: '다른 고장' };
  await assert.rejects(() => store.createRepair({ actor: worker, command: different, idempotencyKey: 'create-003', bodyHash: bodyHash(different) }), (error: unknown) => error instanceof DomainCommandError && error.code === 'IDEMPOTENCY_CONFLICT');
});

test('transition requires an exact revision and follows the shared workflow contract', async () => {
  const store = new InMemoryCommandStore();
  const created = await createRequest(store);
  const reviewed = await store.transitionRepair({ actor: worker, command: { repairRequestId: created.resourceId, toStatus: 'under_review', expectedRevision: 1, note: '접수 내용 확인' }, idempotencyKey: 'transition-001', bodyHash: bodyHash({ toStatus: 'under_review', expectedRevision: 1 }) });
  assert.equal(reviewed.status, 'under_review');
  await assert.rejects(() => store.transitionRepair({ actor: worker, command: { repairRequestId: created.resourceId, toStatus: 'assigned', expectedRevision: 1, repairStationId: 'station-1' }, idempotencyKey: 'transition-002', bodyHash: bodyHash({ expectedRevision: 1 }) }), (error: unknown) => error instanceof DomainCommandError && error.code === 'REVISION_CONFLICT');
  const assigned = await store.transitionRepair({ actor: worker, command: { repairRequestId: created.resourceId, toStatus: 'assigned', expectedRevision: 2, repairStationId: 'station-1' }, idempotencyKey: 'transition-003', bodyHash: bodyHash({ expectedRevision: 2 }) });
  assert.equal(assigned.status, 'assigned');
});

test('repairer submission and center verification preserve required evidence', async () => {
  const store = new InMemoryCommandStore();
  const created = await createRequest(store);
  await store.transitionRepair({ actor: worker, command: { repairRequestId: created.resourceId, toStatus: 'under_review', expectedRevision: 1 }, idempotencyKey: 'transition-010', bodyHash: 'a' });
  await store.transitionRepair({ actor: worker, command: { repairRequestId: created.resourceId, toStatus: 'assigned', expectedRevision: 2, repairStationId: 'station-1', repairerFirebaseUid: 'repairer-1' }, idempotencyKey: 'transition-011', bodyHash: 'b' });
  await store.transitionRepair({ actor: repairer, command: { repairRequestId: created.resourceId, toStatus: 'scheduled', expectedRevision: 3, scheduledAt: futureAppointment() }, idempotencyKey: 'transition-012', bodyHash: 'c' });
  await store.transitionRepair({ actor: repairer, command: { repairRequestId: created.resourceId, toStatus: 'in_progress', expectedRevision: 4 }, idempotencyKey: 'transition-013', bodyHash: 'd' });
  const submitted = await store.transitionRepair({ actor: repairer, command: { repairRequestId: created.resourceId, toStatus: 'repairer_submitted', expectedRevision: 5, billedAmountKrw: 175000 }, idempotencyKey: 'transition-014', bodyHash: 'e' });
  assert.equal(submitted.status, 'repairer_submitted');
  assert.ok(store.getRepair(created.resourceId)?.submittedAt);
  await assert.rejects(() => store.transitionRepair({ actor: worker, command: { repairRequestId: created.resourceId, toStatus: 'center_verified', expectedRevision: 6 }, idempotencyKey: 'transition-015', bodyHash: 'f' }), (error: unknown) => error instanceof DomainCommandError && error.code === 'SUBSIDY_DECISION_REQUIRED');
  const verified = await store.transitionRepair({ actor: worker, command: { repairRequestId: created.resourceId, toStatus: 'center_verified', expectedRevision: 6, subsidyDecisionId: 'decision-1', subsidyAccountId: 'account-1' }, idempotencyKey: 'transition-016', bodyHash: 'g' });
  assert.equal(verified.status, 'center_verified');
  assert.equal(store.getEvents().length, 7);
});

test('repairer-only transitions cannot use a mixed operational role to bypass assignment', async () => {
  const store = new InMemoryCommandStore();
  const created = await createRequest(store);
  await store.transitionRepair({ actor: worker, command: { repairRequestId: created.resourceId, toStatus: 'under_review', expectedRevision: 1 }, idempotencyKey: 'mixed-001', bodyHash: 'a' });
  await store.transitionRepair({ actor: worker, command: { repairRequestId: created.resourceId, toStatus: 'assigned', expectedRevision: 2, repairStationId: 'station-1', repairerFirebaseUid: 'repairer-1' }, idempotencyKey: 'mixed-002', bodyHash: 'b' });
  const mixedActor: ActorContext = { uid: 'repairer-other', tenantId: 'tenant-a', roles: ['case_worker', 'repairer'] };
  await assert.rejects(() => store.transitionRepair({ actor: mixedActor, command: { repairRequestId: created.resourceId, toStatus: 'scheduled', expectedRevision: 3, scheduledAt: futureAppointment() }, idempotencyKey: 'mixed-003', bodyHash: 'c' }), (error: unknown) => error instanceof DomainCommandError && error.code === 'REPAIR_ASSIGNMENT_REQUIRED');
});

test('subsidy reservation and execution update one auditable balance', async () => {
  const store = new InMemoryCommandStore();
  const created = await createRequest(store);
  await store.appendSubsidy({ actor: worker, command: { ...ledgerScope, transactionType: 'allocation', amountKrw: 500000 }, idempotencyKey: 'ledger-001', bodyHash: 'allocation' });
  await store.appendSubsidy({ actor: worker, command: { ...ledgerScope, workOrderId: created.resourceId, transactionType: 'reservation', amountKrw: 180000 }, idempotencyKey: 'ledger-002', bodyHash: 'reservation' });
  await store.appendSubsidy({ actor: worker, command: { ...ledgerScope, workOrderId: created.resourceId, transactionType: 'execution', amountKrw: 175000 }, idempotencyKey: 'ledger-003', bodyHash: 'execution' });
  assert.deepEqual(store.getSummary(), { accountId: 'account-1', personId: 'person-1', policyVersionId: 'policy-2026', allocatedKrw: 500000, adjustmentKrw: 0, reservedKrw: 5000, executedKrw: 175000, availableKrw: 320000, reservedByWorkOrder: { [created.resourceId]: 5000 } });
  await assert.rejects(() => store.appendSubsidy({ actor: worker, command: { ...ledgerScope, workOrderId: created.resourceId, transactionType: 'execution', amountKrw: 6000 }, idempotencyKey: 'ledger-004', bodyHash: 'too-much' }), (error: unknown) => error instanceof DomainCommandError && error.code === 'LEDGER_EXECUTION_EXCEEDS_RESERVATION');
});
