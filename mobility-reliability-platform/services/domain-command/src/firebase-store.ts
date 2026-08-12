import {
  FieldValue,
  Timestamp,
  getFirestore,
  type DocumentData,
  type DocumentSnapshot,
  type Firestore,
  type Transaction,
} from 'firebase-admin/firestore';
import { DomainCommandError, deterministicId, uuidV7 } from './canonical.js';
import { DomainCommandKernel } from './kernel.js';
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
import type { CommandStore } from './store.js';

const zeroSummary = (command: AppendSubsidyTransactionCommand): SubsidySummary => ({
  accountId: command.accountId,
  personId: command.personId,
  policyVersionId: command.policyVersionId,
  allocatedKrw: 0,
  adjustmentKrw: 0,
  reservedKrw: 0,
  executedKrw: 0,
  availableKrw: 0,
  reservedByWorkOrder: {},
});

export class FirestoreDomainCommandStore implements CommandStore {
  private readonly kernel = new DomainCommandKernel();

  constructor(private readonly db: Firestore = getFirestore()) {}

  async createRepair(input: { actor: ActorContext; command: CreateRepairRequestCommand; idempotencyKey: string; bodyHash: string }): Promise<CommandResult> {
    const proposedRepairId = uuidV7();
    const proposedEventId = uuidV7();
    const idemRef = this.idemRef(input.actor.tenantId, input.idempotencyKey);
    const tenantRef = this.tenantRef(input.actor.tenantId);
    return this.db.runTransaction(async (tx) => {
      const existing = await tx.get(idemRef);
      const replay = this.replay(existing, input.bodyHash, 'create_repair_request');
      if (replay) return replay;

      const beneficiaryRef = tenantRef.collection('people').doc(input.command.beneficiaryId);
      const deviceRef = tenantRef.collection('devices').doc(input.command.deviceId);
      const assignmentQuery = tenantRef.collection('deviceAssignments')
        .where('tenant_id', '==', input.actor.tenantId)
        .where('person_id', '==', input.command.beneficiaryId)
        .where('device_id', '==', input.command.deviceId)
        .where('status', '==', 'active')
        .limit(1);
      const [beneficiarySnapshot, deviceSnapshot, assignmentSnapshot] = await Promise.all([
        tx.get(beneficiaryRef),
        tx.get(deviceRef),
        tx.get(assignmentQuery),
      ]);
      this.assertTenantEntity(beneficiarySnapshot, input.actor.tenantId, 'person_id', input.command.beneficiaryId, 'BENEFICIARY_NOT_FOUND');
      this.assertTenantEntity(deviceSnapshot, input.actor.tenantId, 'device_id', input.command.deviceId, 'DEVICE_NOT_FOUND');
      if (assignmentSnapshot.empty) throw new DomainCommandError('DEVICE_ASSIGNMENT_NOT_FOUND', 'The device is not actively assigned to this beneficiary.', 409);
      await this.assertGuardianRelationship(tx, input.actor, input.command.beneficiaryId);

      const mutation = this.kernel.createRepair({ actor: input.actor, command: input.command, idempotencyKey: input.idempotencyKey, resourceId: proposedRepairId, eventId: proposedEventId });
      const repairRef = this.repairRef(input.actor.tenantId, mutation.workOrder.id);
      const eventId = mutation.event.eventId;
      const eventRef = this.eventRef(input.actor.tenantId, eventId);
      tx.create(repairRef, this.repairData(mutation.workOrder));
      tx.create(eventRef, this.eventData(mutation.event));
      tx.create(repairRef.collection('statusHistory').doc(eventId), this.eventData(mutation.event));
      tx.create(idemRef, this.idempotencyData(input.bodyHash, mutation.result));
      return mutation.result;
    });
  }

  async transitionRepair(input: { actor: ActorContext; command: TransitionRepairRequestCommand; idempotencyKey: string; bodyHash: string }): Promise<CommandResult> {
    const proposedEventId = uuidV7();
    const idemRef = this.idemRef(input.actor.tenantId, input.idempotencyKey);
    const repairRef = this.repairRef(input.actor.tenantId, input.command.repairRequestId);
    return this.db.runTransaction(async (tx) => {
      const [existing, repairSnapshot] = await Promise.all([tx.get(idemRef), tx.get(repairRef)]);
      const replay = this.replay(existing, input.bodyHash, 'transition_repair_request');
      if (replay) return replay;
      if (!repairSnapshot.exists) throw new DomainCommandError('REPAIR_NOT_FOUND', 'Repair request not found.', 404);
      const current = this.decodeRepair(repairSnapshot.data());
      if (current.tenantId !== input.actor.tenantId) throw new DomainCommandError('REPAIR_NOT_FOUND', 'Repair request not found.', 404);
      await this.assertRepairActor(tx, input.actor, current);

      if (input.command.toStatus === 'assigned') {
        if (!input.command.repairerFirebaseUid) throw new DomainCommandError('REPAIRER_REQUIRED', 'An assigned repair needs a repairer identity.');
        await this.assertActiveRepairer(tx, input.actor.tenantId, input.command.repairerFirebaseUid);
      }
      if (input.command.toStatus === 'center_verified' && current.publicFundingInvolved && !input.command.subsidyAccountId) {
        throw new DomainCommandError('SUBSIDY_ACCOUNT_REQUIRED', 'Publicly funded verification requires the subsidy account used for the decision.');
      }
      if (input.command.subsidyAccountId) {
        const account = await tx.get(this.summaryRef(input.actor.tenantId, input.command.subsidyAccountId));
        const data = account.data();
        if (!account.exists || data?.tenant_id !== input.actor.tenantId || data.person_id !== current.beneficiaryId) {
          throw new DomainCommandError('SUBSIDY_ACCOUNT_MISMATCH', 'The subsidy decision does not belong to the repair beneficiary.', 409);
        }
      }

      const mutation = this.kernel.transitionRepair({ actor: input.actor, command: input.command, current, idempotencyKey: input.idempotencyKey, eventId: proposedEventId });
      const eventId = mutation.event.eventId;
      const eventRef = this.eventRef(input.actor.tenantId, eventId);
      tx.update(repairRef, this.repairData(mutation.workOrder));
      tx.create(eventRef, this.eventData(mutation.event));
      tx.create(repairRef.collection('statusHistory').doc(eventId), this.eventData(mutation.event));
      tx.create(idemRef, this.idempotencyData(input.bodyHash, mutation.result));
      return mutation.result;
    });
  }

  async appendSubsidy(input: { actor: ActorContext; command: AppendSubsidyTransactionCommand; idempotencyKey: string; bodyHash: string }): Promise<CommandResult> {
    const proposedTransactionId = uuidV7();
    const proposedEventId = uuidV7();
    const idemRef = this.idemRef(input.actor.tenantId, input.idempotencyKey);
    const summaryRef = this.summaryRef(input.actor.tenantId, input.command.accountId);
    return this.db.runTransaction(async (tx) => {
      const [existing, summarySnapshot, personSnapshot, policySnapshot] = await Promise.all([
        tx.get(idemRef),
        tx.get(summaryRef),
        tx.get(this.tenantRef(input.actor.tenantId).collection('people').doc(input.command.personId)),
        tx.get(this.tenantRef(input.actor.tenantId).collection('subsidyPolicies').doc(input.command.policyVersionId)),
      ]);
      const replay = this.replay(existing, input.bodyHash, 'append_subsidy_transaction');
      if (replay) return replay;
      this.assertTenantEntity(personSnapshot, input.actor.tenantId, 'person_id', input.command.personId, 'BENEFICIARY_NOT_FOUND');
      this.assertTenantEntity(policySnapshot, input.actor.tenantId, 'policy_version_id', input.command.policyVersionId, 'SUBSIDY_POLICY_NOT_FOUND');

      let workOrder: RepairWorkOrder | undefined;
      if (input.command.workOrderId) {
        const repairSnapshot = await tx.get(this.repairRef(input.actor.tenantId, input.command.workOrderId));
        if (!repairSnapshot.exists) throw new DomainCommandError('REPAIR_NOT_FOUND', 'Repair request not found.', 404);
        workOrder = this.decodeRepair(repairSnapshot.data());
        if (workOrder.beneficiaryId !== input.command.personId) throw new DomainCommandError('SUBSIDY_ACCOUNT_MISMATCH', 'The subsidy account does not belong to the repair beneficiary.', 409);
        if (!workOrder.publicFundingInvolved) throw new DomainCommandError('PUBLIC_FUNDING_NOT_ENABLED', 'This repair is not eligible for a public subsidy transaction.', 409);
        if (input.command.transactionType === 'reservation' && workOrder.requestedAmountKrw !== undefined && input.command.amountKrw > workOrder.requestedAmountKrw) throw new DomainCommandError('RESERVATION_EXCEEDS_REQUEST', 'The reservation exceeds the requested public-funding amount.', 409);
        if (input.command.transactionType === 'execution') {
          if (!['repairer_submitted', 'center_verified', 'completed'].includes(workOrder.status)) throw new DomainCommandError('EXECUTION_STATUS_INVALID', 'Subsidy execution requires submitted repair evidence.', 409);
          if (workOrder.billedAmountKrw !== undefined && input.command.amountKrw > workOrder.billedAmountKrw) throw new DomainCommandError('EXECUTION_EXCEEDS_BILL', 'The execution exceeds the repairer-submitted amount.', 409);
        }
      }

      const currentSummary = summarySnapshot.exists ? this.decodeSummary(summarySnapshot.data()) : zeroSummary(input.command);
      if (summarySnapshot.exists && (currentSummary.accountId !== input.command.accountId || currentSummary.personId !== input.command.personId || currentSummary.policyVersionId !== input.command.policyVersionId)) {
        throw new DomainCommandError('SUBSIDY_ACCOUNT_MISMATCH', 'The transaction scope does not match the existing subsidy account.', 409);
      }
      if (input.command.transactionType === 'reversal') {
        if (!input.command.reversesTransactionId || !input.command.workOrderId) throw new DomainCommandError('REVERSAL_ORIGINAL_REQUIRED', 'A reversal must reference an execution transaction.');
        const original = await tx.get(this.ledgerRef(input.actor.tenantId, input.command.accountId, input.command.reversesTransactionId));
        const data = original.data();
        if (!original.exists || data?.transaction_type !== 'execution' || data.amount_krw !== input.command.amountKrw || data.work_order_id !== input.command.workOrderId) {
          throw new DomainCommandError('INVALID_REVERSAL', 'Reversal must reference the exact execution transaction.', 409);
        }
        const reversalQuery = await tx.get(summaryRef.collection('transactions').where('reverses_transaction_id', '==', input.command.reversesTransactionId).limit(1));
        if (!reversalQuery.empty) throw new DomainCommandError('DUPLICATE_REVERSAL', 'The execution transaction has already been reversed.', 409);
      }

      const mutation = this.kernel.appendSubsidy({ actor: input.actor, command: input.command, currentSummary, idempotencyKey: input.idempotencyKey, transactionId: proposedTransactionId, eventId: proposedEventId });
      const ledgerRef = this.ledgerRef(input.actor.tenantId, input.command.accountId, mutation.transaction.transactionId);
      const eventId = mutation.event.eventId;
      tx.create(ledgerRef, this.transactionData(mutation.transaction));
      tx.set(summaryRef, this.summaryData(input.actor.tenantId, mutation.summary, input.actor.uid), { merge: true });
      tx.create(this.eventRef(input.actor.tenantId, eventId), this.eventData(mutation.event));
      tx.create(idemRef, this.idempotencyData(input.bodyHash, mutation.result));
      return mutation.result;
    });
  }

  private replay(snapshot: DocumentSnapshot, hash: string, commandType: CommandResult['commandType']): CommandResult | undefined {
    if (!snapshot.exists) return undefined;
    const data = snapshot.data();
    if (data?.body_hash !== hash || data.command_type !== commandType || !data.result) throw new DomainCommandError('IDEMPOTENCY_CONFLICT', 'The idempotency key was already used for a different command.', 409);
    return { ...this.decodeResult(data.result), idempotent: true };
  }

  private assertTenantEntity(snapshot: DocumentSnapshot, tenantId: string, idField: string, id: string, code: string) {
    const data = snapshot.data();
    if (!snapshot.exists || data?.tenant_id !== tenantId || (data[idField] !== undefined && data[idField] !== id)) throw new DomainCommandError(code, 'The referenced entity is not part of this institution.', 404);
  }

  private async assertGuardianRelationship(tx: Transaction, actor: ActorContext, beneficiaryId: string) {
    if (!actor.roles.includes('guardian') || actor.roles.some((role) => role === 'case_worker' || role === 'tenant_admin')) return;
    if (!actor.personId) throw new DomainCommandError('GUARDIAN_RELATIONSHIP_REQUIRED', 'A guardian membership must identify its person.', 403);
    const relationships = this.tenantRef(actor.tenantId).collection('personRelationships');
    const [forward, reverse] = await Promise.all([
      tx.get(relationships.where('tenant_id', '==', actor.tenantId).where('from_person_id', '==', actor.personId).where('to_person_id', '==', beneficiaryId).where('status', '==', 'active').limit(1)),
      tx.get(relationships.where('tenant_id', '==', actor.tenantId).where('from_person_id', '==', beneficiaryId).where('to_person_id', '==', actor.personId).where('status', '==', 'active').limit(1)),
    ]);
    if (forward.empty && reverse.empty) throw new DomainCommandError('GUARDIAN_RELATIONSHIP_REQUIRED', 'No active guardian relationship authorizes this request.', 403);
  }

  private async assertRepairActor(tx: Transaction, actor: ActorContext, workOrder: RepairWorkOrder) {
    if (actor.roles.some((role) => role === 'case_worker' || role === 'tenant_admin')) return;
    if (actor.roles.includes('repairer')) {
      if (workOrder.repairerFirebaseUid !== actor.uid) throw new DomainCommandError('REPAIR_ASSIGNMENT_REQUIRED', 'This repair is not assigned to the authenticated repairer.', 403);
      return;
    }
    if (actor.roles.includes('beneficiary') && actor.personId === workOrder.beneficiaryId) return;
    if (actor.roles.includes('guardian')) {
      await this.assertGuardianRelationship(tx, actor, workOrder.beneficiaryId);
      return;
    }
    throw new DomainCommandError('RESOURCE_FORBIDDEN', 'This membership cannot change the repair request.', 403);
  }

  private async assertActiveRepairer(tx: Transaction, tenantId: string, uid: string) {
    const snapshot = await tx.get(this.tenantRef(tenantId).collection('memberships').doc(uid));
    const data = snapshot.data();
    if (!snapshot.exists || data?.tenant_id !== tenantId || data.status !== 'active' || !Array.isArray(data.roles) || !data.roles.includes('repairer')) throw new DomainCommandError('REPAIRER_NOT_FOUND', 'The selected repairer is not an active institution repairer.', 409);
  }

  private decodeRepair(data: DocumentData | undefined): RepairWorkOrder {
    if (!data || typeof data.work_order_id !== 'string' || typeof data.tenant_id !== 'string' || typeof data.status !== 'string' || !Number.isSafeInteger(data.revision)) throw new DomainCommandError('CORRUPT_REPAIR_DOCUMENT', 'The repair work-order document is invalid.', 500);
    return {
      id: data.work_order_id,
      tenantId: data.tenant_id,
      beneficiaryId: data.requester_person_id,
      deviceId: data.device_id,
      issueSummary: data.issue_summary,
      publicFundingInvolved: data.public_funding_involved,
      ...(data.requested_amount_krw === undefined ? {} : { requestedAmountKrw: data.requested_amount_krw }),
      ...(data.repair_station_id === undefined ? {} : { repairStationId: data.repair_station_id }),
      ...(data.repairer_firebase_uid === undefined ? {} : { repairerFirebaseUid: data.repairer_firebase_uid }),
      ...(data.subsidy_account_id === undefined ? {} : { subsidyAccountId: data.subsidy_account_id }),
      ...(data.billed_amount_krw === undefined ? {} : { billedAmountKrw: data.billed_amount_krw }),
      ...(data.submitted_at === undefined ? {} : { submittedAt: this.iso(data.submitted_at) }),
      ...(data.subsidy_decision_id === undefined ? {} : { subsidyDecisionId: data.subsidy_decision_id }),
      status: data.status,
      revision: data.revision,
      createdByUid: data.created_by,
      updatedByUid: data.updated_by,
      createdAt: this.iso(data.created_at),
      updatedAt: this.iso(data.updated_at),
    } as RepairWorkOrder;
  }

  private decodeSummary(data: DocumentData | undefined): SubsidySummary {
    if (!data || typeof data.account_id !== 'string' || typeof data.person_id !== 'string' || typeof data.policy_version_id !== 'string') throw new DomainCommandError('CORRUPT_LEDGER_SUMMARY', 'The subsidy account is invalid.', 500);
    const amounts = [data.allocated_krw, data.adjustment_krw, data.reserved_krw, data.executed_krw, data.available_krw];
    if (!amounts.every((value) => Number.isSafeInteger(value))) throw new DomainCommandError('CORRUPT_LEDGER_SUMMARY', 'The subsidy account balance is invalid.', 500);
    return { accountId: data.account_id, personId: data.person_id, policyVersionId: data.policy_version_id, allocatedKrw: data.allocated_krw, adjustmentKrw: data.adjustment_krw, reservedKrw: data.reserved_krw, executedKrw: data.executed_krw, availableKrw: data.available_krw, reservedByWorkOrder: data.reserved_by_work_order ?? {} };
  }

  private repairData(workOrder: RepairWorkOrder): Record<string, unknown> {
    return compact({ schema_version: 1, work_order_id: workOrder.id, tenant_id: workOrder.tenantId, requester_person_id: workOrder.beneficiaryId, device_id: workOrder.deviceId, issue_summary: workOrder.issueSummary, public_funding_involved: workOrder.publicFundingInvolved, requested_amount_krw: workOrder.requestedAmountKrw, repair_station_id: workOrder.repairStationId, repairer_firebase_uid: workOrder.repairerFirebaseUid, subsidy_account_id: workOrder.subsidyAccountId, billed_amount_krw: workOrder.billedAmountKrw, submitted_at: workOrder.submittedAt ? Timestamp.fromDate(new Date(workOrder.submittedAt)) : undefined, subsidy_decision_id: workOrder.subsidyDecisionId, status: workOrder.status, revision: workOrder.revision, created_by: workOrder.createdByUid, updated_by: workOrder.updatedByUid, created_at: Timestamp.fromDate(new Date(workOrder.createdAt)), updated_at: Timestamp.fromDate(new Date(workOrder.updatedAt)), source: 'native' });
  }

  private eventData(event: DomainEvent): Record<string, unknown> {
    const payload = snakeObject(event.payload);
    return compact({ schema_version: 1, event_id: event.eventId, event_type: event.eventType, tenant_id: event.tenantId, aggregate_type: event.aggregateType, aggregate_id: event.aggregateId, aggregate_revision: event.revision, actor_uid: event.actorUid, actor_role: event.actorRole, occurred_at: Timestamp.fromDate(new Date(event.occurredAt)), received_at: this.serverTimestamp(), idempotency_key: payload.idempotency_key, payload, processing_status: 'pending', source: 'native' });
  }

  private transactionData(transaction: ReturnType<DomainCommandKernel['appendSubsidy']>['transaction']): Record<string, unknown> {
    return compact({ schema_version: 1, transaction_id: transaction.transactionId, tenant_id: transaction.tenantId, account_id: transaction.accountId, person_id: transaction.personId, policy_version_id: transaction.policyVersionId, work_order_id: transaction.workOrderId, transaction_type: transaction.transactionType, amount_krw: transaction.amountKrw, reverses_transaction_id: transaction.reversesTransactionId, reason_code: transaction.reasonCode, recorded_by: transaction.actorUid, occurred_at: Timestamp.fromDate(new Date(transaction.createdAt)), created_at: this.serverTimestamp(), source: 'native' });
  }

  private summaryData(tenantId: string, summary: SubsidySummary, actorUid: string): Record<string, unknown> {
    return { schema_version: 1, account_id: summary.accountId, tenant_id: tenantId, person_id: summary.personId, policy_version_id: summary.policyVersionId, allocated_krw: summary.allocatedKrw, adjustment_krw: summary.adjustmentKrw, reserved_krw: summary.reservedKrw, executed_krw: summary.executedKrw, available_krw: summary.availableKrw, reserved_by_work_order: summary.reservedByWorkOrder, status: 'active', updated_by: actorUid, updated_at: this.serverTimestamp(), revision: FieldValue.increment(1), source: 'system_projection' };
  }

  private idempotencyData(hash: string, result: CommandResult): Record<string, unknown> { return { body_hash: hash, command_type: result.commandType, result: snakeObject({ ...result }), committed_at: this.serverTimestamp() }; }
  private decodeResult(data: DocumentData): CommandResult { return { commandType: data.command_type, tenantId: data.tenant_id, resourceId: data.resource_id, eventId: data.event_id, ...(data.revision === undefined ? {} : { revision: data.revision }), ...(data.status === undefined ? {} : { status: data.status }), ...(data.transaction_id === undefined ? {} : { transactionId: data.transaction_id }) } as CommandResult; }
  private iso(value: unknown): string { return value instanceof Timestamp ? value.toDate().toISOString() : new Date(String(value)).toISOString(); }
  private tenantRef(tenantId: string) { return this.db.collection('tenants').doc(tenantId); }
  private repairRef(tenantId: string, repairId: string) { return this.tenantRef(tenantId).collection('repairWorkOrders').doc(repairId); }
  private eventRef(tenantId: string, eventId: string) { return this.tenantRef(tenantId).collection('domainEvents').doc(eventId); }
  private idemRef(tenantId: string, key: string) { return this.tenantRef(tenantId).collection('commandIdempotency').doc(deterministicId('key', key)); }
  private summaryRef(tenantId: string, accountId: string) { return this.tenantRef(tenantId).collection('subsidyAccounts').doc(accountId); }
  private ledgerRef(tenantId: string, accountId: string, transactionId: string) { return this.summaryRef(tenantId, accountId).collection('transactions').doc(transactionId); }
  private serverTimestamp() { return FieldValue.serverTimestamp(); }
}

function compact(input: Record<string, unknown>): Record<string, unknown> {
  return Object.fromEntries(Object.entries(input).filter(([, value]) => value !== undefined));
}

function snakeObject(input: Record<string, unknown>): Record<string, unknown> {
  return Object.fromEntries(Object.entries(input).filter(([, value]) => value !== undefined).map(([key, value]) => [key.replace(/[A-Z]/g, (letter) => `_${letter.toLowerCase()}`), value && typeof value === 'object' && !Array.isArray(value) ? snakeObject(value as Record<string, unknown>) : value]));
}
