import { bodyHash, deterministicId, isoNow } from './canonical.js';
import { appendSubsidyTransaction, createRepairWorkOrder, transitionRepairWorkOrder } from './workflow.js';
import type {
  ActorContext,
  AppendSubsidyTransactionCommand,
  CommandResult,
  CreateRepairRequestCommand,
  DomainEvent,
  RepairWorkOrder,
  SubsidySummary,
  TransitionRepairRequestCommand,
} from './types.js';

export interface CreateMutation {
  result: CommandResult;
  workOrder: RepairWorkOrder;
  event: DomainEvent;
}

export interface TransitionMutation {
  result: CommandResult;
  workOrder: RepairWorkOrder;
  event: DomainEvent;
}

export interface SubsidyMutation {
  result: CommandResult;
  transaction: {
    transactionId: string;
    tenantId: string;
    accountId: string;
    personId: string;
    policyVersionId: string;
    workOrderId?: string;
    transactionType: AppendSubsidyTransactionCommand['transactionType'];
    amountKrw: number;
    reversesTransactionId?: string;
    actorUid: string;
    reasonCode: string;
    createdAt: string;
  };
  summary: SubsidySummary;
  event: DomainEvent;
}

export class DomainCommandKernel {
  createRepair(input: { actor: ActorContext; command: CreateRepairRequestCommand; idempotencyKey: string; resourceId?: string; eventId?: string; now?: Date }): CreateMutation {
    const now = input.now ?? new Date();
    const repairRequestId = input.resourceId ?? deterministicId('repair', input.actor.tenantId, input.idempotencyKey);
    const built = createRepairWorkOrder({ actor: input.actor, command: input.command, repairRequestId, now });
    const eventId = input.eventId ?? deterministicId('event', input.actor.tenantId, input.idempotencyKey, 'create');
    const event = this.event({
      eventId,
      eventType: 'repair.requested',
      aggregateType: 'repair',
      aggregateId: repairRequestId,
      revision: built.workOrder.revision,
      actor: input.actor,
      actorRole: built.actorRole,
      occurredAt: isoNow(now),
      payload: { status: built.workOrder.status, requesterPersonId: built.workOrder.beneficiaryId, deviceId: built.workOrder.deviceId, publicFundingInvolved: built.workOrder.publicFundingInvolved, idempotencyKey: input.idempotencyKey },
    });
    return {
      workOrder: built.workOrder,
      event,
      result: { commandType: 'create_repair_request', tenantId: input.actor.tenantId, resourceId: repairRequestId, revision: 1, status: 'requested', eventId },
    };
  }

  transitionRepair(input: { actor: ActorContext; command: TransitionRepairRequestCommand; current: RepairWorkOrder; idempotencyKey: string; eventId?: string; now?: Date }): TransitionMutation {
    const now = input.now ?? new Date();
    const built = transitionRepairWorkOrder({ actor: input.actor, current: input.current, command: input.command, now });
    const eventId = input.eventId ?? deterministicId('event', input.actor.tenantId, input.idempotencyKey, 'transition');
    const event = this.event({ eventId, eventType: repairEventType(built.workOrder.status), aggregateType: 'repair', aggregateId: input.current.id, revision: built.workOrder.revision, actor: input.actor, actorRole: built.actorRole, occurredAt: isoNow(now), payload: { fromStatus: input.current.status, toStatus: built.workOrder.status, note: input.command.note, idempotencyKey: input.idempotencyKey } });
    return { workOrder: built.workOrder, event, result: { commandType: 'transition_repair_request', tenantId: input.actor.tenantId, resourceId: input.current.id, revision: built.workOrder.revision, status: built.workOrder.status, eventId } };
  }

  appendSubsidy(input: { actor: ActorContext; command: AppendSubsidyTransactionCommand; currentSummary: SubsidySummary; idempotencyKey: string; transactionId?: string; eventId?: string; now?: Date }): SubsidyMutation {
    const now = input.now ?? new Date();
    const transactionId = input.transactionId ?? deterministicId('ledger', input.actor.tenantId, input.idempotencyKey);
    const built = appendSubsidyTransaction({ actor: input.actor, command: input.command, transactionId, currentSummary: input.currentSummary });
    const eventId = input.eventId ?? deterministicId('event', input.actor.tenantId, input.idempotencyKey, 'ledger');
    const event = this.event({ eventId, eventType: subsidyEventType(input.command.transactionType), aggregateType: 'subsidy_account', aggregateId: input.command.accountId, actor: input.actor, actorRole: built.actorRole, occurredAt: isoNow(now), payload: { accountId: input.command.accountId, transactionId, personId: input.command.personId, policyVersionId: input.command.policyVersionId, reasonCode: input.command.reasonCode, idempotencyKey: input.idempotencyKey, ...(input.command.workOrderId === undefined ? {} : { workOrderId: input.command.workOrderId }), transactionType: input.command.transactionType, amountKrw: input.command.amountKrw } });
    const transaction = { transactionId, tenantId: input.actor.tenantId, accountId: input.command.accountId, personId: input.command.personId, policyVersionId: input.command.policyVersionId, reasonCode: input.command.reasonCode, ...(input.command.workOrderId === undefined ? {} : { workOrderId: input.command.workOrderId }), transactionType: input.command.transactionType, amountKrw: input.command.amountKrw, ...(input.command.reversesTransactionId === undefined ? {} : { reversesTransactionId: input.command.reversesTransactionId }), actorUid: input.actor.uid, createdAt: isoNow(now) };
    return { transaction, summary: built.summary, event, result: { commandType: 'append_subsidy_transaction', tenantId: input.actor.tenantId, resourceId: transactionId, transactionId, eventId } };
  }

  private event(input: Omit<DomainEvent, 'tenantId' | 'actorUid'> & { actor: ActorContext }): DomainEvent {
    return { ...input, actorUid: input.actor.uid, tenantId: input.actor.tenantId };
  }
}

function repairEventType(status: RepairWorkOrder['status']): string {
  if (status === 'assigned') return 'repair.assigned';
  if (status === 'repairer_submitted') return 'repair.submitted';
  if (status === 'center_verified') return 'repair.verified';
  if (status === 'completed') return 'repair.recorded';
  return 'repair.status_changed';
}

function subsidyEventType(type: AppendSubsidyTransactionCommand['transactionType']): string {
  if (type === 'reservation') return 'subsidy.reserved';
  if (type === 'execution') return 'subsidy.executed';
  if (type === 'release' || type === 'reversal') return 'subsidy.cancelled';
  return 'subsidy.adjusted';
}

export function commandBodyHash(command: unknown): string {
  return bodyHash(command);
}
