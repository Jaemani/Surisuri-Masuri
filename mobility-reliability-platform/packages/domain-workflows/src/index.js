const transitions = Object.freeze({
  requested: Object.freeze({
    under_review: ['case_worker', 'tenant_admin'],
    cancelled: ['beneficiary', 'guardian', 'case_worker', 'tenant_admin'],
    rejected: ['case_worker', 'tenant_admin'],
  }),
  under_review: Object.freeze({
    assigned: ['case_worker', 'tenant_admin'],
    cancelled: ['beneficiary', 'guardian', 'case_worker', 'tenant_admin'],
    rejected: ['case_worker', 'tenant_admin'],
  }),
  assigned: Object.freeze({
    scheduled: ['repairer', 'case_worker', 'tenant_admin'],
    cancelled: ['case_worker', 'tenant_admin'],
    reopened: ['case_worker', 'tenant_admin'],
  }),
  scheduled: Object.freeze({
    in_progress: ['repairer'],
    cancelled: ['case_worker', 'tenant_admin'],
    reopened: ['case_worker', 'tenant_admin'],
  }),
  in_progress: Object.freeze({
    repairer_submitted: ['repairer'],
    needs_correction: ['repairer'],
  }),
  repairer_submitted: Object.freeze({
    center_verified: ['case_worker', 'tenant_admin'],
    needs_correction: ['case_worker', 'tenant_admin'],
  }),
  needs_correction: Object.freeze({
    in_progress: ['repairer'],
    cancelled: ['case_worker', 'tenant_admin'],
  }),
  center_verified: Object.freeze({
    completed: ['beneficiary', 'guardian', 'case_worker', 'tenant_admin'],
    reopened: ['beneficiary', 'guardian', 'case_worker', 'tenant_admin'],
  }),
  completed: Object.freeze({
    reopened: ['beneficiary', 'guardian', 'case_worker', 'tenant_admin'],
  }),
  reopened: Object.freeze({
    under_review: ['case_worker', 'tenant_admin'],
  }),
  rejected: Object.freeze({
    reopened: ['case_worker', 'tenant_admin'],
  }),
  cancelled: Object.freeze({
    reopened: ['case_worker', 'tenant_admin'],
  }),
});

export function allowedRepairTransitions(status, role) {
  const candidates = transitions[status] ?? {};
  return Object.entries(candidates)
    .filter(([, roles]) => roles.includes(role))
    .map(([next]) => next);
}

export function assertRepairTransition({ from, to, role, workOrder }) {
  if (!allowedRepairTransitions(from, role).includes(to)) {
    throw new Error(`REPAIR_TRANSITION_FORBIDDEN:${from}:${to}:${role}`);
  }

  const nextNeedsStation = ['assigned', 'scheduled', 'in_progress', 'repairer_submitted', 'center_verified', 'completed'];
  if (nextNeedsStation.includes(to) && !workOrder.repairStationId) {
    throw new Error('REPAIR_STATION_REQUIRED');
  }
  if (to === 'repairer_submitted') {
    if (!Number.isInteger(workOrder.billedAmountKrw) || workOrder.billedAmountKrw < 0) {
      throw new Error('BILLED_AMOUNT_REQUIRED');
    }
    if (!workOrder.submittedAt) throw new Error('SUBMITTED_AT_REQUIRED');
  }
  if (to === 'center_verified' && workOrder.publicFundingInvolved && !workOrder.subsidyDecisionId) {
    throw new Error('SUBSIDY_DECISION_REQUIRED');
  }
  return true;
}

function assertPositiveAmount(transaction) {
  if (!Number.isInteger(transaction.amountKrw) || transaction.amountKrw <= 0) {
    throw new Error(`LEDGER_POSITIVE_AMOUNT_REQUIRED:${transaction.transactionId}`);
  }
}

export function projectSubsidyLedger(transactions) {
  const seen = new Map();
  const reservations = new Map();
  let allocatedKrw = 0;
  let executedKrw = 0;
  let adjustmentKrw = 0;

  for (const transaction of transactions) {
    if (seen.has(transaction.transactionId)) {
      throw new Error(`LEDGER_DUPLICATE_TRANSACTION:${transaction.transactionId}`);
    }
    seen.set(transaction.transactionId, transaction);

    const workOrderId = transaction.workOrderId;
    switch (transaction.transactionType) {
      case 'allocation':
        assertPositiveAmount(transaction);
        allocatedKrw += transaction.amountKrw;
        break;
      case 'reservation':
        assertPositiveAmount(transaction);
        if (!workOrderId) throw new Error('LEDGER_WORK_ORDER_REQUIRED');
        reservations.set(workOrderId, (reservations.get(workOrderId) ?? 0) + transaction.amountKrw);
        break;
      case 'execution': {
        assertPositiveAmount(transaction);
        if (!workOrderId) throw new Error('LEDGER_WORK_ORDER_REQUIRED');
        const reserved = reservations.get(workOrderId) ?? 0;
        if (transaction.amountKrw > reserved) throw new Error(`LEDGER_EXECUTION_EXCEEDS_RESERVATION:${workOrderId}`);
        reservations.set(workOrderId, reserved - transaction.amountKrw);
        executedKrw += transaction.amountKrw;
        break;
      }
      case 'release': {
        assertPositiveAmount(transaction);
        if (!workOrderId) throw new Error('LEDGER_WORK_ORDER_REQUIRED');
        const reserved = reservations.get(workOrderId) ?? 0;
        if (transaction.amountKrw > reserved) throw new Error(`LEDGER_RELEASE_EXCEEDS_RESERVATION:${workOrderId}`);
        reservations.set(workOrderId, reserved - transaction.amountKrw);
        break;
      }
      case 'adjustment':
        if (!Number.isInteger(transaction.amountKrw) || transaction.amountKrw === 0) {
          throw new Error(`LEDGER_NON_ZERO_ADJUSTMENT_REQUIRED:${transaction.transactionId}`);
        }
        adjustmentKrw += transaction.amountKrw;
        break;
      case 'reversal': {
        assertPositiveAmount(transaction);
        const original = seen.get(transaction.reversesTransactionId);
        if (!original || original.transactionType !== 'execution' || original.amountKrw !== transaction.amountKrw) {
          throw new Error(`LEDGER_INVALID_REVERSAL:${transaction.transactionId}`);
        }
        executedKrw -= transaction.amountKrw;
        break;
      }
      default:
        throw new Error(`LEDGER_UNKNOWN_TRANSACTION:${transaction.transactionType}`);
    }
  }

  const reservedKrw = [...reservations.values()].reduce((sum, amount) => sum + amount, 0);
  const availableKrw = allocatedKrw + adjustmentKrw - executedKrw - reservedKrw;
  if (availableKrw < 0) throw new Error('LEDGER_OVER_LIMIT');

  return Object.freeze({ allocatedKrw, adjustmentKrw, reservedKrw, executedKrw, availableKrw });
}

export function resolveRepairStationAssignmentMode(tenantPolicy) {
  return tenantPolicy?.repairStationAssignmentMode === 'user_selectable'
    ? 'user_selectable'
    : 'center_assigned';
}
