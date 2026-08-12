import {
  assertRepairTransition as assertPackageRepairTransition,
} from '@mobility-reliability/domain-workflows';
import { DomainCommandError, optionalKrw, positiveKrw, safeId, safeText } from './canonical.js';
import type {
  ActorContext,
  AppendSubsidyTransactionCommand,
  CreateRepairRequestCommand,
  RepairStatus,
  RepairWorkOrder,
  Role,
  SubsidySummary,
  TransitionRepairRequestCommand,
} from './types.js';

const createRoles = new Set<Role>(['beneficiary', 'guardian', 'case_worker', 'tenant_admin']);
const ledgerRoles = new Set<Role>(['case_worker', 'tenant_admin']);
const allStatuses = new Set<RepairStatus>([
  'requested', 'under_review', 'assigned', 'scheduled', 'in_progress', 'repairer_submitted',
  'needs_correction', 'center_verified', 'completed', 'reopened', 'rejected', 'cancelled',
]);

export function actorCanCreateRepair(actor: ActorContext): Role {
  const role = actor.roles.find((candidate) => createRoles.has(candidate));
  if (!role) throw new DomainCommandError('ROLE_FORBIDDEN', 'This membership cannot create a repair request.', 403);
  return role;
}

export function actorCanWriteLedger(actor: ActorContext): Role {
  const role = actor.roles.find((candidate) => ledgerRoles.has(candidate));
  if (!role) throw new DomainCommandError('ROLE_FORBIDDEN', 'Only institution operators can write subsidy ledger entries.', 403);
  return role;
}

export function normalizeCreateCommand(input: unknown): CreateRepairRequestCommand {
  if (!input || typeof input !== 'object') throw new DomainCommandError('INVALID_COMMAND', 'Request body must be an object.');
  const value = input as Record<string, unknown>;
  if (typeof value.publicFundingInvolved !== 'boolean') throw new DomainCommandError('INVALID_PUBLIC_FUNDING_FLAG', 'publicFundingInvolved must be boolean.');
  const command: CreateRepairRequestCommand = {
    beneficiaryId: safeId(value.beneficiaryId, 'beneficiaryId'),
    deviceId: safeId(value.deviceId, 'deviceId'),
    issueSummary: safeText(value.issueSummary, 'issueSummary', 500),
    publicFundingInvolved: value.publicFundingInvolved,
  };
  const amount = optionalKrw(value.requestedAmountKrw, 'requestedAmountKrw');
  if (amount !== undefined) command.requestedAmountKrw = amount;
  return command;
}

export function normalizeTransitionCommand(input: unknown): TransitionRepairRequestCommand {
  if (!input || typeof input !== 'object') throw new DomainCommandError('INVALID_COMMAND', 'Request body must be an object.');
  const value = input as Record<string, unknown>;
  const repairRequestId = safeId(value.repairRequestId, 'repairRequestId');
  const toStatus = value.toStatus;
  if (typeof toStatus !== 'string' || !allStatuses.has(toStatus as RepairStatus)) throw new DomainCommandError('INVALID_REPAIR_STATUS', 'toStatus is not a supported repair status.');
  assertTransitionFields(value, toStatus as RepairStatus);
  if (!Number.isSafeInteger(value.expectedRevision) || (value.expectedRevision as number) < 1) throw new DomainCommandError('INVALID_EXPECTED_REVISION', 'expectedRevision must be a positive integer.');
  const command: TransitionRepairRequestCommand = { repairRequestId, toStatus: toStatus as RepairStatus, expectedRevision: value.expectedRevision as number };
  for (const [key, field] of [['repairStationId', 'repairStationId'], ['repairerFirebaseUid', 'repairerFirebaseUid'], ['subsidyAccountId', 'subsidyAccountId'], ['subsidyDecisionId', 'subsidyDecisionId']] as const) {
    if (value[key] !== undefined) (command as unknown as Record<string, unknown>)[field] = safeId(value[key], field);
  }
  if (value.billedAmountKrw !== undefined) command.billedAmountKrw = positiveKrw(value.billedAmountKrw, 'billedAmountKrw');
  if (value.scheduledAt !== undefined) {
    if (typeof value.scheduledAt !== 'string' || Number.isNaN(Date.parse(value.scheduledAt))) throw new DomainCommandError('INVALID_SCHEDULED_AT', 'scheduledAt must be an ISO timestamp.');
    command.scheduledAt = new Date(value.scheduledAt).toISOString();
  }
  if (value.note !== undefined) command.note = safeText(value.note, 'note', 1000);
  return command;
}

const transitionFieldAllowlist: Record<RepairStatus, ReadonlySet<string>> = {
  requested: new Set(['repairRequestId', 'toStatus', 'expectedRevision']),
  under_review: new Set(['repairRequestId', 'toStatus', 'expectedRevision', 'note']),
  assigned: new Set(['repairRequestId', 'toStatus', 'expectedRevision', 'repairStationId', 'repairerFirebaseUid', 'note']),
  scheduled: new Set(['repairRequestId', 'toStatus', 'expectedRevision', 'scheduledAt']),
  in_progress: new Set(['repairRequestId', 'toStatus', 'expectedRevision']),
  repairer_submitted: new Set(['repairRequestId', 'toStatus', 'expectedRevision', 'billedAmountKrw']),
  needs_correction: new Set(['repairRequestId', 'toStatus', 'expectedRevision', 'note']),
  center_verified: new Set(['repairRequestId', 'toStatus', 'expectedRevision', 'subsidyAccountId', 'subsidyDecisionId', 'note']),
  completed: new Set(['repairRequestId', 'toStatus', 'expectedRevision', 'note']),
  reopened: new Set(['repairRequestId', 'toStatus', 'expectedRevision', 'note']),
  rejected: new Set(['repairRequestId', 'toStatus', 'expectedRevision', 'note']),
  cancelled: new Set(['repairRequestId', 'toStatus', 'expectedRevision', 'note']),
};

function assertTransitionFields(value: Record<string, unknown>, toStatus: RepairStatus) {
  const allowed = transitionFieldAllowlist[toStatus];
  const disallowed = Object.keys(value).filter((key) => !allowed.has(key));
  if (disallowed.length > 0) throw new DomainCommandError('UNEXPECTED_COMMAND_FIELD', `The ${toStatus} transition contains unsupported fields.`);
}

export function normalizeSubsidyCommand(input: unknown): AppendSubsidyTransactionCommand {
  if (!input || typeof input !== 'object') throw new DomainCommandError('INVALID_COMMAND', 'Request body must be an object.');
  const value = input as Record<string, unknown>;
  const transactionType = value.transactionType;
  if (!['allocation', 'reservation', 'execution', 'release', 'adjustment', 'reversal'].includes(String(transactionType))) throw new DomainCommandError('INVALID_TRANSACTION_TYPE', 'Unsupported subsidy transaction type.');
  const accountId = safeId(value.accountId, 'accountId');
  const personId = safeId(value.personId, 'personId');
  const policyVersionId = safeId(value.policyVersionId, 'policyVersionId');
  const reasonCode = safeText(value.reasonCode, 'reasonCode', 120);
  const needsWorkOrder = ['reservation', 'execution', 'release', 'reversal'].includes(String(transactionType));
  if (needsWorkOrder && value.workOrderId === undefined) throw new DomainCommandError('WORK_ORDER_REQUIRED', 'This transaction type requires a work order.');
  const amountKrw = transactionType === 'adjustment'
    ? signedAdjustmentKrw(value.amountKrw)
    : positiveKrw(value.amountKrw);
  const command: AppendSubsidyTransactionCommand = {
    accountId,
    personId,
    policyVersionId,
    reasonCode,
    transactionType: transactionType as AppendSubsidyTransactionCommand['transactionType'],
    amountKrw,
    ...(value.workOrderId === undefined ? {} : { workOrderId: safeId(value.workOrderId, 'workOrderId') }),
  };
  if (value.reversesTransactionId !== undefined) command.reversesTransactionId = safeId(value.reversesTransactionId, 'reversesTransactionId');
  if (value.note !== undefined) command.note = safeText(value.note, 'note', 1000);
  return command;
}

function signedAdjustmentKrw(value: unknown): number {
  if (!Number.isSafeInteger(value) || value === 0 || Math.abs(value as number) > 100_000_000) throw new DomainCommandError('INVALID_AMOUNTKRW', 'An adjustment must be a non-zero integer within the supported limit.');
  return value as number;
}

export function createRepairWorkOrder(input: {
  actor: ActorContext;
  command: CreateRepairRequestCommand;
  repairRequestId: string;
  now: Date;
}): { workOrder: RepairWorkOrder; eventType: string; actorRole: Role } {
  const actorRole = actorCanCreateRepair(input.actor);
  if (input.actor.roles.includes('beneficiary') && !input.actor.roles.some((role) => role === 'case_worker' || role === 'tenant_admin') && input.actor.personId !== input.command.beneficiaryId) {
    throw new DomainCommandError('RESOURCE_FORBIDDEN', 'A beneficiary can only create a request for their own person record.', 403);
  }
  if (input.command.publicFundingInvolved && input.command.requestedAmountKrw === undefined) {
    throw new DomainCommandError('FUNDING_AMOUNT_REQUIRED', 'A publicly funded request needs an estimated amount.');
  }
  const now = input.now.toISOString();
  return {
    actorRole,
    eventType: 'repair_request_created',
    workOrder: {
      id: input.repairRequestId,
      tenantId: input.actor.tenantId,
      beneficiaryId: input.command.beneficiaryId,
      deviceId: input.command.deviceId,
      issueSummary: input.command.issueSummary,
      publicFundingInvolved: input.command.publicFundingInvolved,
      ...(input.command.requestedAmountKrw === undefined ? {} : { requestedAmountKrw: input.command.requestedAmountKrw }),
      status: 'requested',
      revision: 1,
      createdByUid: input.actor.uid,
      updatedByUid: input.actor.uid,
      createdAt: now,
      updatedAt: now,
    },
  };
}

export function transitionRepairWorkOrder(input: {
  actor: ActorContext;
  current: RepairWorkOrder;
  command: TransitionRepairRequestCommand;
  now: Date;
}): { workOrder: RepairWorkOrder; eventType: string; actorRole: Role } {
  if (input.current.revision !== input.command.expectedRevision) throw new DomainCommandError('REVISION_CONFLICT', 'The repair request changed; reload before trying again.', 409);
  if (['scheduled', 'in_progress', 'repairer_submitted'].includes(input.command.toStatus)
    && (!input.actor.roles.includes('repairer') || input.current.repairerFirebaseUid !== input.actor.uid)) {
    throw new DomainCommandError('REPAIR_ASSIGNMENT_REQUIRED', 'This repair is not assigned to the authenticated repairer.', 403);
  }
  const now = input.now.toISOString();
  const roles = input.actor.roles.filter((role) => ['beneficiary', 'guardian', 'case_worker', 'repairer', 'tenant_admin'].includes(role));
  let actorRole: Role | undefined;
  let lastError: unknown;
  for (const role of roles) {
    try {
      assertPackageRepairTransition({
        from: input.current.status,
        to: input.command.toStatus,
        role,
        workOrder: {
          publicFundingInvolved: input.current.publicFundingInvolved,
          ...(input.command.repairStationId ?? input.current.repairStationId ? { repairStationId: input.command.repairStationId ?? input.current.repairStationId } : {}),
          ...(input.command.repairerFirebaseUid ?? input.current.repairerFirebaseUid ? { repairerFirebaseUid: input.command.repairerFirebaseUid ?? input.current.repairerFirebaseUid } : {}),
          ...(input.command.billedAmountKrw ?? input.current.billedAmountKrw ? { billedAmountKrw: input.command.billedAmountKrw ?? input.current.billedAmountKrw } : {}),
          ...(input.command.toStatus === 'repairer_submitted' || input.current.submittedAt ? { submittedAt: input.command.toStatus === 'repairer_submitted' ? now : input.current.submittedAt! } : {}),
          ...(input.command.subsidyDecisionId ?? input.current.subsidyDecisionId ? { subsidyDecisionId: input.command.subsidyDecisionId ?? input.current.subsidyDecisionId } : {}),
        },
      });
      actorRole = role;
      break;
    } catch (error) {
      lastError = error;
    }
  }
  if (!actorRole) {
    const message = lastError instanceof Error ? lastError.message : 'REPAIR_TRANSITION_FORBIDDEN';
    if (message === 'REPAIR_STATION_REQUIRED' || message === 'BILLED_AMOUNT_REQUIRED' || message === 'SUBMITTED_AT_REQUIRED' || message === 'SUBSIDY_DECISION_REQUIRED') throw new DomainCommandError(message, 'The transition is missing a required work-order field.');
    throw new DomainCommandError('REPAIR_TRANSITION_FORBIDDEN', 'This membership cannot perform that repair transition.', 403);
  }
  if (input.command.toStatus === 'scheduled') {
    if (!input.command.scheduledAt) throw new DomainCommandError('SCHEDULED_AT_REQUIRED', 'A scheduled repair needs an appointment time.');
    const scheduledAt = Date.parse(input.command.scheduledAt);
    const earliest = input.now.getTime() - 15 * 60 * 1000;
    const latest = input.now.getTime() + 180 * 24 * 60 * 60 * 1000;
    if (scheduledAt < earliest || scheduledAt > latest) throw new DomainCommandError('SCHEDULED_AT_OUT_OF_RANGE', 'The appointment time is outside the supported scheduling window.');
  }
  const next: RepairWorkOrder = {
    ...input.current,
    status: input.command.toStatus,
    revision: input.current.revision + 1,
    updatedByUid: input.actor.uid,
    updatedAt: now,
    ...(input.command.repairStationId === undefined ? {} : { repairStationId: input.command.repairStationId }),
    ...(input.command.repairerFirebaseUid === undefined ? {} : { repairerFirebaseUid: input.command.repairerFirebaseUid }),
    ...(input.command.scheduledAt === undefined ? {} : { scheduledAt: input.command.scheduledAt }),
    ...(input.command.subsidyAccountId === undefined ? {} : { subsidyAccountId: input.command.subsidyAccountId }),
    ...(input.command.billedAmountKrw === undefined ? {} : { billedAmountKrw: input.command.billedAmountKrw }),
    ...(input.command.toStatus === 'repairer_submitted' ? { submittedAt: now } : {}),
    ...(input.command.subsidyDecisionId === undefined ? {} : { subsidyDecisionId: input.command.subsidyDecisionId }),
    ...(input.command.subsidyAccountId === undefined ? {} : { subsidyAccountId: input.command.subsidyAccountId }),
  };
  return { actorRole, eventType: `repair_${input.command.toStatus}`, workOrder: next };
}

export function appendSubsidyTransaction(input: {
  actor: ActorContext;
  command: AppendSubsidyTransactionCommand;
  transactionId: string;
  currentSummary: SubsidySummary;
}): { summary: SubsidySummary; actorRole: Role } {
  const actorRole = actorCanWriteLedger(input.actor);
  const amount = input.command.amountKrw;
  const byWorkOrder = { ...input.currentSummary.reservedByWorkOrder };
  if (input.currentSummary.accountId && input.currentSummary.accountId !== input.command.accountId) throw new DomainCommandError('LEDGER_SCOPE_CONFLICT', 'The account scope does not match the current ledger.', 409);
  if (input.currentSummary.personId && input.currentSummary.personId !== input.command.personId) throw new DomainCommandError('LEDGER_SCOPE_CONFLICT', 'The person scope does not match the current ledger.', 409);
  if (input.currentSummary.policyVersionId && input.currentSummary.policyVersionId !== input.command.policyVersionId) throw new DomainCommandError('LEDGER_POLICY_CONFLICT', 'The policy version does not match the current ledger.', 409);
  const available = input.currentSummary.availableKrw;
  const reserved = input.command.workOrderId === undefined ? 0 : (byWorkOrder[input.command.workOrderId] ?? 0);
  let next: SubsidySummary = { ...input.currentSummary, accountId: input.command.accountId, personId: input.command.personId, policyVersionId: input.command.policyVersionId, reservedByWorkOrder: byWorkOrder };
  switch (input.command.transactionType) {
    case 'allocation':
      next = { ...next, allocatedKrw: next.allocatedKrw + amount, availableKrw: available + amount };
      break;
    case 'adjustment':
      next = { ...next, adjustmentKrw: next.adjustmentKrw + amount, availableKrw: available + amount };
      break;
    case 'reservation':
      if (input.command.workOrderId === undefined) throw new DomainCommandError('WORK_ORDER_REQUIRED', 'A reservation requires a work order.');
      if (amount > available) throw new DomainCommandError('LEDGER_OVER_LIMIT', 'This reservation exceeds available subsidy.', 409);
      byWorkOrder[input.command.workOrderId] = reserved + amount;
      next = { ...next, reservedKrw: next.reservedKrw + amount, availableKrw: available - amount };
      break;
    case 'execution':
      if (input.command.workOrderId === undefined) throw new DomainCommandError('WORK_ORDER_REQUIRED', 'An execution requires a work order.');
      if (amount > reserved) throw new DomainCommandError('LEDGER_EXECUTION_EXCEEDS_RESERVATION', 'Execution exceeds the work order reservation.', 409);
      byWorkOrder[input.command.workOrderId] = reserved - amount;
      next = { ...next, reservedKrw: next.reservedKrw - amount, executedKrw: next.executedKrw + amount };
      break;
    case 'release':
      if (input.command.workOrderId === undefined) throw new DomainCommandError('WORK_ORDER_REQUIRED', 'A release requires a work order.');
      if (amount > reserved) throw new DomainCommandError('LEDGER_RELEASE_EXCEEDS_RESERVATION', 'Release exceeds the work order reservation.', 409);
      byWorkOrder[input.command.workOrderId] = reserved - amount;
      next = { ...next, reservedKrw: next.reservedKrw - amount, availableKrw: available + amount };
      break;
    case 'reversal':
      if (input.command.workOrderId === undefined) throw new DomainCommandError('WORK_ORDER_REQUIRED', 'A reversal requires a work order.');
      if (!input.command.reversesTransactionId) throw new DomainCommandError('REVERSAL_ORIGINAL_REQUIRED', 'A reversal must reference an execution transaction.');
      next = { ...next, executedKrw: next.executedKrw - amount, availableKrw: available + amount };
      break;
    default:
      throw new DomainCommandError('INVALID_TRANSACTION_TYPE', 'Unsupported subsidy transaction type.');
  }
  if (next.availableKrw < 0 || next.reservedKrw < 0 || next.executedKrw < 0) throw new DomainCommandError('LEDGER_INVALID_BALANCE', 'The resulting subsidy balance is invalid.', 409);
  return { summary: next, actorRole };
}
