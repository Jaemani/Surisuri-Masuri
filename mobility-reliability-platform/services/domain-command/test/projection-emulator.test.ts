import { randomUUID } from 'node:crypto';
import { afterAll, beforeAll, describe, expect, test } from 'vitest';
import { deleteApp, initializeApp, type App } from 'firebase-admin/app';
import { FieldValue, Timestamp, getFirestore, type Firestore } from 'firebase-admin/firestore';
import { FirestoreProductProjectionStore } from '../src/projection-store.js';
import type { ActorContext } from '../src/types.js';

const enabled = Boolean(process.env.FIRESTORE_EMULATOR_HOST);
const emulatorTest = enabled ? test : test.skip;
let app: App;
let db: Firestore;

beforeAll(() => { if (enabled) { app = initializeApp({ projectId: process.env.GCLOUD_PROJECT ?? 'demo-domain-command' }, `projection-${randomUUID()}`); db = getFirestore(app); } });
afterAll(async () => { if (app) await deleteApp(app); });
const actor = (tenantId: string, roles: ActorContext['roles'], uid: string, personId?: string): ActorContext => ({ tenantId, roles, uid, ...(personId ? { personId } : {}) });

async function seed(tenantId: string) {
  const tenant = db.collection('tenants').doc(tenantId);
  const now = Timestamp.fromDate(new Date('2026-08-13T00:00:00.000Z'));
  await tenant.set({ tenant_id: tenantId, display_name: '서울서부 복지관', legal_name: 'SENSITIVE-ORG-LEGAL', status: 'active' });
  await tenant.collection('people').doc('person-1').set({ tenant_id: tenantId, person_id: 'person-1', public_code: 'C-1042', status: 'active', updated_at: now });
  await tenant.collection('privatePeople').doc('person-1').set({ tenant_id: tenantId, person_id: 'person-1', legal_name: 'SENSITIVE-NAME', phone_e164: 'SENSITIVE-PHONE', address_text: 'SENSITIVE-ADDRESS', birth_date: '1940-01-01' });
  await tenant.collection('devices').doc('device-1').set({ tenant_id: tenantId, device_id: 'device-1', public_code: 'MOB-1', manufacturer: '나래', model_name: 'EV-2', status: 'active', created_at: now, object_path: 'SENSITIVE-STORAGE', latitude: 37.1 });
  await tenant.collection('deviceAssignments').doc('assignment-1').set({ tenant_id: tenantId, assignment_id: 'assignment-1', person_id: 'person-1', device_id: 'device-1', status: 'active', valid_from: Timestamp.fromMillis(Date.now() - 60_000) });
  await tenant.collection('repairStations').doc('station-1').set({ tenant_id: tenantId, repair_station_id: 'station-1', display_name: '한마음 모빌리티', contact_label: 'SENSITIVE-PARTNER-CONTACT', status: 'active' });
  await tenant.collection('repairWorkOrders').doc('work-1').set({ tenant_id: tenantId, work_order_id: 'work-1', requester_person_id: 'person-1', device_id: 'device-1', issue_summary: 'SENSITIVE-RAW-ISSUE', issue_category_label: '브레이크', status: 'assigned', public_funding_involved: true, repair_station_id: 'station-1', repairer_firebase_uid: 'repairer-1', requested_amount_krw: 180000, subsidy_account_id: 'account-1', subsidy_decision_id: 'decision-1', revision: 3, created_at: now, updated_at: now });
  await tenant.collection('repairs').doc('history-1').set({ tenant_id: tenantId, repair_id: 'history-1', work_order_id: 'work-old', device_id: 'device-1', occurred_at: Timestamp.fromDate(new Date('2026-07-30T00:00:00.000Z')), recorded_at: now, status: 'completed', source_quality: 'verified', billed_amount_krw: 70000 });
  await tenant.collection('repairs').doc('history-1').collection('items').doc('item-01').set({ tenant_id: tenantId, repair_id: 'history-1', repair_item_id: 'item-01', category_code: 'brakes', action_code: 'adjust', quantity: 1, line_amount_krw: 70000, source_quality: 'verified' });
  await tenant.collection('repairs').doc('repair-verified-1').set({ tenant_id: tenantId, repair_id: 'repair-verified-1', work_order_id: 'work-completed', device_id: 'device-1', status: 'completed', occurred_at: now, source_quality: 'verified', issue_summary: 'SENSITIVE-COMPLETED-ISSUE', repairer_membership_uid: 'SENSITIVE-REPAIRER-UID' });
  await tenant.collection('repairs').doc('repair-verified-1').collection('items').doc('item-01').set({ tenant_id: tenantId, repair_id: 'repair-verified-1', repair_item_id: 'item-01', category_code: 'brakes', action_code: 'repair', quantity: 1, source_quality: 'verified', detail_text: 'SENSITIVE-ITEM-TEXT' });
  await tenant.collection('subsidyAccounts').doc('account-1').set({ tenant_id: tenantId, account_id: 'account-1', person_id: 'person-1', policy_version_id: 'policy-1', status: 'active', allocated_krw: 300000, adjustment_krw: 0, reserved_krw: 180000, executed_krw: 0, available_krw: 120000, reserved_by_work_order: { 'work-1': 180000 } });
  await tenant.collection('subsidyAccounts').doc('account-1').collection('transactions').doc('tx-1').set({ tenant_id: tenantId, transaction_id: 'tx-1', account_id: 'account-1', person_id: 'person-1', policy_version_id: 'policy-1', work_order_id: 'work-1', transaction_type: 'reservation', amount_krw: 180000, actor_label: 'SENSITIVE-ACTOR-NAME', occurred_at: now });
  await tenant.collection('inspections').doc('inspection-1').set({ tenant_id: tenantId, inspection_id: 'inspection-1', person_id: 'person-1', device_id: 'device-1', reason_code: 'routine_cycle', reason_summary: 'SENSITIVE-INSPECTION-NOTE', decision_code: 'review', confidence_band: 'low', scheduled_at: now });
  await tenant.collection('reportRuns').doc('report-1').set({ tenant_id: tenantId, report_run_id: 'report-1', report_type: 'monthly_operations', title: 'SENSITIVE-REPORT-TITLE', report_type_label: 'SENSITIVE-REPORT-TYPE', status: 'completed', fact_count: 2, completed_at: now });
  await tenant.collection('trips').doc('trip-secret').set({ tenant_id: tenantId, person_id: 'person-1', latitude: 37.5, longitude: 127.1, object_path: 'SENSITIVE-RAW-GPS' });
}

describe('Firestore purpose-limited projection adapter', () => {
  emulatorTest('beneficiary receives own product DTO without PII, UID, or raw GPS', async () => {
    const tenantId = `tenant-${randomUUID()}`; await seed(tenantId);
    const store = new FirestoreProductProjectionStore(db);
    const snapshot = await store.getMobileSnapshot(actor(tenantId, ['beneficiary'], 'beneficiary-1', 'person-1'));
    if (!('device' in snapshot)) throw new Error('expected beneficiary projection');
    expect(snapshot.repairRequest).toMatchObject({ id: 'work-1', title: 'SENSITIVE-RAW-ISSUE', status: 'assigned' });
    expect(snapshot.device).toMatchObject({ id: 'device-1', registrationNumber: 'MOB-1' });
    expect(snapshot.device.timeline).toEqual(expect.arrayContaining([expect.objectContaining({ title: '수리를 완료했어요', detail: '브레이크 조정 · 수리 항목 1개' })]));
    expect(snapshot.device.timeline[0]).toMatchObject({ title: '수리를 완료했어요', detail: '브레이크 수리 · 수리 항목 1개', tone: 'teal' });
    expect(snapshot.subsidy).toMatchObject({ used: 180000, total: 300000 });
    const serialized = JSON.stringify(snapshot);
    for (const sentinel of ['SENSITIVE-NAME', 'SENSITIVE-PHONE', 'SENSITIVE-ADDRESS', 'SENSITIVE-STORAGE', 'SENSITIVE-RAW-GPS', 'SENSITIVE-COMPLETED-ISSUE', 'SENSITIVE-REPAIRER-UID', 'SENSITIVE-ITEM-TEXT', 'repairer_firebase_uid']) expect(serialized).not.toContain(sentinel);
  });

  emulatorTest('repairer receives only its assigned job and no subsidy detail', async () => {
    const tenantId = `tenant-${randomUUID()}`; await seed(tenantId);
    await db.doc(`tenants/${tenantId}/repairWorkOrders/work-other`).set({ tenant_id: tenantId, work_order_id: 'work-other', requester_person_id: 'person-1', device_id: 'device-1', issue_summary: '다른 작업', status: 'assigned', repairer_firebase_uid: 'repairer-other', revision: 1, created_at: Timestamp.now(), updated_at: Timestamp.now() });
    const snapshot = await new FirestoreProductProjectionStore(db).getMobileSnapshot(actor(tenantId, ['repairer'], 'repairer-1', 'repairer-person'));
    expect(snapshot.roleSession.role).toBe('repairer');
    if (!('repairJobs' in snapshot)) throw new Error('expected repairer projection');
    expect(snapshot.repairJobs).toHaveLength(1);
    expect(snapshot.repairJobs[0]?.id).toBe('work-1');
    expect(snapshot.repairJobs[0]).toEqual({
      id: 'work-1', revision: 3, status: 'assigned', customerLabel: '이용자 C-1042',
      device: { publicCode: 'MOB-1', model: '나래 EV-2' }, issue: '브레이크',
      scheduledAt: null, scheduleLabel: '일정 협의 필요', priority: 'scheduled',
      billedAmountKrw: null, submittedAt: null, workItems: [], allowedActions: ['schedule'],
    });
    expect(Object.keys(snapshot.repairJobs[0]!).sort()).toEqual(['allowedActions', 'billedAmountKrw', 'customerLabel', 'device', 'id', 'issue', 'priority', 'revision', 'scheduleLabel', 'scheduledAt', 'status', 'submittedAt', 'workItems'].sort());
    expect(JSON.stringify(snapshot)).not.toContain('account-1');
    expect(snapshot).not.toHaveProperty('device');
    expect(snapshot).not.toHaveProperty('subsidy');
    for (const sentinel of ['SENSITIVE-RAW-ISSUE', 'SENSITIVE-NAME', 'SENSITIVE-PHONE', 'SENSITIVE-ADDRESS', 'repairer-1', 'person-1', '180000']) expect(JSON.stringify(snapshot)).not.toContain(sentinel);
  });

  emulatorTest('operator sees bounded institution DTO while non-operator is denied', async () => {
    const tenantId = `tenant-${randomUUID()}`; await seed(tenantId);
    const store = new FirestoreProductProjectionStore(db);
    const repairs = await store.getConsoleProjection(actor(tenantId, ['case_worker'], 'worker-1', 'worker-person'), 'repairs') as unknown[];
    expect(repairs).toHaveLength(1);
    expect(repairs[0]).toMatchObject({ id: 'work-1', user: '이용자 C-1042', revision: 3, domainStatus: 'assigned', publicFundingInvolved: true, billedAmountKrw: null, subsidyContext: { accountId: 'account-1', personId: 'person-1', policyVersionId: 'policy-1', decisionId: 'decision-1', executionState: 'verification_required' } });
    expect(JSON.stringify(repairs)).not.toContain('SENSITIVE-NAME');
    expect(JSON.stringify(repairs)).not.toContain('SENSITIVE-RAW-ISSUE');
    await expect(store.getConsoleProjection(actor(tenantId, ['beneficiary'], 'beneficiary-1', 'person-1'), 'dashboard')).rejects.toMatchObject({ code: 'ROLE_FORBIDDEN' });
  });

  emulatorTest('operator receives the only validated beneficiary subsidy account before center verification', async () => {
    const tenantId = `tenant-${randomUUID()}`; await seed(tenantId);
    await db.doc(`tenants/${tenantId}/repairWorkOrders/work-1`).update({ subsidy_account_id: FieldValue.delete(), subsidy_decision_id: FieldValue.delete(), status: 'repairer_submitted', billed_amount_krw: 180000 });
    const repairs = await new FirestoreProductProjectionStore(db).getConsoleProjection(actor(tenantId, ['case_worker'], 'worker-1'), 'repairs') as Array<Record<string, unknown>>;
    expect(repairs[0]).toMatchObject({ domainStatus: 'repairer_submitted', subsidyContext: { accountId: 'account-1', personId: 'person-1', policyVersionId: 'policy-1', executionState: 'verification_required' } });
  });

  emulatorTest('console ledger exposes stable transaction identity and typed transaction fields', async () => {
    const tenantId = `tenant-${randomUUID()}`; await seed(tenantId);
    const output = await new FirestoreProductProjectionStore(db).getConsoleProjection(actor(tenantId, ['case_worker'], 'worker-1'), 'ledger') as Array<Record<string, unknown>>;
    expect(output).toEqual([expect.objectContaining({ id: 'work-1', transactionId: 'tx-1', transactionType: 'reservation', amountKrw: 180000 })]);
  });

  emulatorTest('repair completion is not presented as subsidy execution without an execution transaction', async () => {
    const tenantId = `tenant-${randomUUID()}`; await seed(tenantId);
    const workOrder = db.doc(`tenants/${tenantId}/repairWorkOrders/work-1`);
    await workOrder.update({ status: 'completed', billed_amount_krw: 180000 });
    const otherAccount = db.doc(`tenants/${tenantId}/subsidyAccounts/account-other`);
    await otherAccount.set({ tenant_id: tenantId, account_id: 'account-other', person_id: 'person-1', policy_version_id: 'policy-1', status: 'active' });
    await otherAccount.collection('transactions').doc('tx-wrong-account').set({ tenant_id: tenantId, transaction_id: 'tx-wrong-account', account_id: 'account-other', person_id: 'person-1', policy_version_id: 'policy-1', work_order_id: 'work-1', transaction_type: 'execution', amount_krw: 180000, occurred_at: Timestamp.now() });
    const store = new FirestoreProductProjectionStore(db);
    const operator = actor(tenantId, ['case_worker'], 'worker-1');
    const before = await store.getConsoleProjection(operator, 'repairs') as Array<Record<string, unknown>>;
    expect(before[0]).toMatchObject({ domainStatus: 'completed', subsidyContext: { executionState: 'execution_pending' } });

    await db.doc(`tenants/${tenantId}/subsidyAccounts/account-1/transactions/tx-execution`).set({ tenant_id: tenantId, transaction_id: 'tx-execution', account_id: 'account-1', person_id: 'person-1', policy_version_id: 'policy-1', work_order_id: 'work-1', transaction_type: 'execution', amount_krw: 180000, occurred_at: Timestamp.now() });
    const after = await store.getConsoleProjection(operator, 'repairs') as Array<Record<string, unknown>>;
    expect(after[0]).toMatchObject({ subsidyContext: { executionState: 'executed' } });

    await db.doc(`tenants/${tenantId}/subsidyAccounts/account-1/transactions/tx-reversal`).set({ tenant_id: tenantId, transaction_id: 'tx-reversal', account_id: 'account-1', person_id: 'person-1', policy_version_id: 'policy-1', work_order_id: 'work-1', transaction_type: 'reversal', amount_krw: 180000, reverses_transaction_id: 'tx-execution', occurred_at: Timestamp.now() });
    const reversed = await store.getConsoleProjection(operator, 'repairs') as Array<Record<string, unknown>>;
    expect(reversed[0]).toMatchObject({ subsidyContext: { executedAmountKrw: 0, executionState: 'execution_pending' } });
  });

  emulatorTest('fails closed when ledger transaction identities are not unique', async () => {
    const tenantId = `tenant-${randomUUID()}`; await seed(tenantId);
    const account = db.doc(`tenants/${tenantId}/subsidyAccounts/account-2`);
    await account.set({ tenant_id: tenantId, account_id: 'account-2', person_id: 'person-1', policy_version_id: 'policy-1', status: 'active' });
    await account.collection('transactions').doc('tx-2').set({ tenant_id: tenantId, transaction_id: 'tx-1', account_id: 'account-2', person_id: 'person-1', policy_version_id: 'policy-1', transaction_type: 'allocation', amount_krw: 1, occurred_at: Timestamp.now() });
    await expect(new FirestoreProductProjectionStore(db).getConsoleProjection(actor(tenantId, ['case_worker'], 'worker-1'), 'ledger')).rejects.toMatchObject({ code: 'DUPLICATE_LEDGER_TRANSACTION' });
  });

  emulatorTest('console devices include only compact verified repair archive timeline entries', async () => {
    const tenantId = `tenant-${randomUUID()}`; await seed(tenantId);
    const output = await new FirestoreProductProjectionStore(db).getConsoleProjection(actor(tenantId, ['case_worker'], 'worker-1'), 'devices') as Array<Record<string, unknown>>;
    expect(output).toHaveLength(1);
    const device = output[0]!;
    expect(device).toMatchObject({ id: 'MOB-1', user: '이용자 C-1042', model: '나래 EV-2', timeline: expect.any(Array) });
    expect(device.timeline).toEqual([
      { id: 'repair-repair-verified-1', date: '2026. 08. 13.', title: '수리를 완료했어요', detail: '브레이크 수리 · 수리 항목 1개', tone: 'success' },
      { id: 'repair-history-1', date: '2026. 07. 30.', title: '수리를 완료했어요', detail: '브레이크 조정 · 수리 항목 1개', tone: 'success' },
    ]);
    const serialized = JSON.stringify(device);
    for (const sentinel of ['SENSITIVE-COMPLETED-ISSUE', 'SENSITIVE-REPAIRER-UID', 'SENSITIVE-ITEM-TEXT', 'SENSITIVE-STORAGE', 'SENSITIVE-RAW-GPS', '180000', '70000']) expect(serialized).not.toContain(sentinel);
  });

  emulatorTest('console projections do not expose operational free text or contact labels', async () => {
    const tenantId = `tenant-${randomUUID()}`; await seed(tenantId);
    const store = new FirestoreProductProjectionStore(db);
    const operator = actor(tenantId, ['case_worker'], 'worker-1');
    const output = await Promise.all(['ledger', 'inspections', 'partners', 'reports'].map((name) => store.getConsoleProjection(operator, name as 'ledger' | 'inspections' | 'partners' | 'reports')));
    const serialized = JSON.stringify(output);
    for (const sentinel of ['SENSITIVE-ACTOR-NAME', 'SENSITIVE-PARTNER-CONTACT', 'SENSITIVE-INSPECTION-NOTE', 'SENSITIVE-REPORT-TITLE', 'SENSITIVE-REPORT-TYPE']) expect(serialized).not.toContain(sentinel);
  });

  emulatorTest('fails closed when a nested projection document has a mismatched tenant scope', async () => {
    const tenantId = `tenant-${randomUUID()}`; await seed(tenantId);
    await db.doc(`tenants/${tenantId}/devices/corrupt-device`).set({ tenant_id: 'other-tenant', device_id: 'corrupt-device', status: 'active' });
    const store = new FirestoreProductProjectionStore(db);
    await expect(store.getConsoleProjection(actor(tenantId, ['case_worker'], 'worker-1'), 'devices')).rejects.toMatchObject({ code: 'CORRUPT_TENANT_SCOPE' });
  });
});
