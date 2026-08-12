import assert from 'node:assert/strict';
import test from 'node:test';

import {
  allowedRepairTransitions,
  assertRepairTransition,
  projectSubsidyLedger,
  resolveRepairStationAssignmentMode,
} from '../src/index.js';

test('a beneficiary requests or cancels but cannot author authoritative repair work', () => {
  assert.deepEqual(allowedRepairTransitions('requested', 'beneficiary'), ['cancelled']);
  assert.throws(
    () => assertRepairTransition({ from: 'in_progress', to: 'repairer_submitted', role: 'beneficiary', workOrder: {} }),
    /REPAIR_TRANSITION_FORBIDDEN/,
  );
});

test('repairer submission requires station, amount, and submission time', () => {
  const base = { repairStationId: 'station-a', billedAmountKrw: 110000, submittedAt: '2026-08-13T06:00:00Z' };
  assert.equal(assertRepairTransition({ from: 'in_progress', to: 'repairer_submitted', role: 'repairer', workOrder: base }), true);
  assert.throws(
    () => assertRepairTransition({ from: 'in_progress', to: 'repairer_submitted', role: 'repairer', workOrder: { ...base, billedAmountKrw: null } }),
    /BILLED_AMOUNT_REQUIRED/,
  );
});

test('publicly funded work needs an explicit subsidy decision before center verification', () => {
  assert.throws(
    () => assertRepairTransition({
      from: 'repairer_submitted',
      to: 'center_verified',
      role: 'case_worker',
      workOrder: { repairStationId: 'station-a', publicFundingInvolved: true },
    }),
    /SUBSIDY_DECISION_REQUIRED/,
  );
});

test('ledger keeps allocation, reservation, execution, release, and copay-independent balance auditable', () => {
  const result = projectSubsidyLedger([
    { transactionId: 't1', transactionType: 'allocation', amountKrw: 480000 },
    { transactionId: 't2', transactionType: 'reservation', amountKrw: 90000, workOrderId: 'w1' },
    { transactionId: 't3', transactionType: 'execution', amountKrw: 85000, workOrderId: 'w1' },
    { transactionId: 't4', transactionType: 'release', amountKrw: 5000, workOrderId: 'w1' },
  ]);
  assert.deepEqual(result, {
    allocatedKrw: 480000,
    adjustmentKrw: 0,
    reservedKrw: 0,
    executedKrw: 85000,
    availableKrw: 395000,
  });
});

test('ledger rejects duplicate transactions and spending beyond reservation', () => {
  assert.throws(
    () => projectSubsidyLedger([
      { transactionId: 't1', transactionType: 'allocation', amountKrw: 100000 },
      { transactionId: 't2', transactionType: 'reservation', amountKrw: 10000, workOrderId: 'w1' },
      { transactionId: 't3', transactionType: 'execution', amountKrw: 10001, workOrderId: 'w1' },
    ]),
    /LEDGER_EXECUTION_EXCEEDS_RESERVATION/,
  );
});

test('tenant assignment policy defaults to center coordination', () => {
  assert.equal(resolveRepairStationAssignmentMode(undefined), 'center_assigned');
  assert.equal(resolveRepairStationAssignmentMode({ repairStationAssignmentMode: 'user_selectable' }), 'user_selectable');
});
