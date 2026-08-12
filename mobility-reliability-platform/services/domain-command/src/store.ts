import { DomainCommandError, deterministicId } from './canonical.js';
import { DomainCommandKernel } from './kernel.js';
import type { ActorContext, AppendSubsidyTransactionCommand, CommandResult, CreateRepairRequestCommand, DomainEvent, RepairWorkOrder, SubsidySummary, TransitionRepairRequestCommand } from './types.js';

export interface CommandStore {
  createRepair(input: { actor: ActorContext; command: CreateRepairRequestCommand; idempotencyKey: string; bodyHash: string }): Promise<CommandResult>;
  transitionRepair(input: { actor: ActorContext; command: TransitionRepairRequestCommand; idempotencyKey: string; bodyHash: string }): Promise<CommandResult>;
  appendSubsidy(input: { actor: ActorContext; command: AppendSubsidyTransactionCommand; idempotencyKey: string; bodyHash: string }): Promise<CommandResult>;
}

interface IdempotencyRecord { commandType: CommandResult['commandType']; bodyHash: string; result: CommandResult; }

export class InMemoryCommandStore implements CommandStore {
  private readonly kernel = new DomainCommandKernel();
  private readonly idempotency = new Map<string, IdempotencyRecord>();
  private readonly repairs = new Map<string, RepairWorkOrder>();
  private readonly history: DomainEvent[] = [];
  private readonly ledger = new Map<string, { transactionId: string; workOrderId?: string; transactionType: AppendSubsidyTransactionCommand['transactionType']; amountKrw: number; reversesTransactionId?: string }>();
  private summary: SubsidySummary = { accountId: '', personId: '', policyVersionId: '', allocatedKrw: 0, adjustmentKrw: 0, reservedKrw: 0, executedKrw: 0, availableKrw: 0, reservedByWorkOrder: {} };

  async createRepair(input: { actor: ActorContext; command: CreateRepairRequestCommand; idempotencyKey: string; bodyHash: string }): Promise<CommandResult> {
    const commandType = 'create_repair_request' as const;
    const existing = this.getIdempotency(input.actor.tenantId, input.idempotencyKey, commandType, input.bodyHash);
    if (existing) return { ...existing, idempotent: true };
    const mutation = this.kernel.createRepair({ actor: input.actor, command: input.command, idempotencyKey: input.idempotencyKey });
    if (this.repairs.has(mutation.workOrder.id)) throw new DomainCommandError('REPAIR_ALREADY_EXISTS', 'The idempotency key maps to an existing repair request.', 409);
    this.repairs.set(mutation.workOrder.id, mutation.workOrder);
    this.history.push(mutation.event);
    this.idempotency.set(this.idemKey(input.actor.tenantId, input.idempotencyKey), { commandType, bodyHash: input.bodyHash, result: mutation.result });
    return mutation.result;
  }

  async transitionRepair(input: { actor: ActorContext; command: TransitionRepairRequestCommand; idempotencyKey: string; bodyHash: string }): Promise<CommandResult> {
    const commandType = 'transition_repair_request' as const;
    const existing = this.getIdempotency(input.actor.tenantId, input.idempotencyKey, commandType, input.bodyHash);
    if (existing) return { ...existing, idempotent: true };
    const current = this.repairs.get(input.command.repairRequestId);
    if (!current || current.tenantId !== input.actor.tenantId) throw new DomainCommandError('REPAIR_NOT_FOUND', 'Repair request not found.', 404);
    const mutation = this.kernel.transitionRepair({ actor: input.actor, command: input.command, current, idempotencyKey: input.idempotencyKey });
    this.repairs.set(current.id, mutation.workOrder);
    this.history.push(mutation.event);
    this.idempotency.set(this.idemKey(input.actor.tenantId, input.idempotencyKey), { commandType, bodyHash: input.bodyHash, result: mutation.result });
    return mutation.result;
  }

  async appendSubsidy(input: { actor: ActorContext; command: AppendSubsidyTransactionCommand; idempotencyKey: string; bodyHash: string }): Promise<CommandResult> {
    const commandType = 'append_subsidy_transaction' as const;
    const existing = this.getIdempotency(input.actor.tenantId, input.idempotencyKey, commandType, input.bodyHash);
    if (existing) return { ...existing, idempotent: true };
    const repair = input.command.workOrderId === undefined ? undefined : this.repairs.get(input.command.workOrderId);
    if (input.command.workOrderId !== undefined && !repair) throw new DomainCommandError('REPAIR_NOT_FOUND', 'Repair request not found.', 404);
    if (repair && repair.beneficiaryId !== input.command.personId) throw new DomainCommandError('SUBSIDY_ACCOUNT_MISMATCH', 'The subsidy account does not belong to the repair beneficiary.', 409);
    if (!this.summary.accountId) this.summary = { ...this.summary, accountId: input.command.accountId, personId: input.command.personId, policyVersionId: input.command.policyVersionId };
    if (this.summary.accountId !== input.command.accountId || this.summary.personId !== input.command.personId || this.summary.policyVersionId !== input.command.policyVersionId) throw new DomainCommandError('SUBSIDY_ACCOUNT_MISMATCH', 'The transaction scope does not match the subsidy account.', 409);
    if (input.command.transactionType === 'reversal') {
      const original = input.command.reversesTransactionId ? this.ledger.get(input.command.reversesTransactionId) : undefined;
      if (!input.command.workOrderId || !original || original.transactionType !== 'execution' || original.amountKrw !== input.command.amountKrw || original.workOrderId !== input.command.workOrderId) throw new DomainCommandError('INVALID_REVERSAL', 'Reversal must reference the exact execution transaction.', 409);
    }
    const mutation = this.kernel.appendSubsidy({ actor: input.actor, command: input.command, currentSummary: this.summary, idempotencyKey: input.idempotencyKey });
    this.summary = mutation.summary;
    this.ledger.set(mutation.transaction.transactionId, mutation.transaction);
    this.history.push(mutation.event);
    this.idempotency.set(this.idemKey(input.actor.tenantId, input.idempotencyKey), { commandType, bodyHash: input.bodyHash, result: mutation.result });
    return mutation.result;
  }

  getRepair(id: string): RepairWorkOrder | undefined { return this.repairs.get(id); }
  getEvents(): readonly DomainEvent[] { return this.history; }
  getSummary(): SubsidySummary { return this.summary; }
  getLedgerTransaction(id: string) { return this.ledger.get(id); }
  private idemKey(tenantId: string, key: string): string { return deterministicId('idem', tenantId, key); }
  private getIdempotency(tenantId: string, key: string, commandType: CommandResult['commandType'], hash: string): CommandResult | undefined {
    const record = this.idempotency.get(this.idemKey(tenantId, key));
    if (!record) return undefined;
    if (record.commandType !== commandType || record.bodyHash !== hash) throw new DomainCommandError('IDEMPOTENCY_CONFLICT', 'The idempotency key was already used for a different command.', 409);
    return record.result;
  }
}
