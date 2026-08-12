export const ROLES = ['beneficiary', 'guardian', 'case_worker', 'repairer', 'tenant_admin', 'auditor'] as const;
export type Role = (typeof ROLES)[number];

export type RepairStatus =
  | 'requested'
  | 'under_review'
  | 'assigned'
  | 'scheduled'
  | 'in_progress'
  | 'repairer_submitted'
  | 'needs_correction'
  | 'center_verified'
  | 'completed'
  | 'reopened'
  | 'rejected'
  | 'cancelled';

export type SubsidyTransactionType = 'allocation' | 'reservation' | 'execution' | 'release' | 'adjustment' | 'reversal';

export interface ActorContext {
  uid: string;
  tenantId: string;
  roles: Role[];
  appId?: string;
  personId?: string;
}

export interface CreateRepairRequestCommand {
  beneficiaryId: string;
  deviceId: string;
  issueSummary: string;
  publicFundingInvolved: boolean;
  requestedAmountKrw?: number;
}

export interface TransitionRepairRequestCommand {
  repairRequestId: string;
  toStatus: RepairStatus;
  expectedRevision: number;
  repairStationId?: string;
  repairerFirebaseUid?: string;
  subsidyAccountId?: string;
  billedAmountKrw?: number;
  submittedAt?: string;
  subsidyDecisionId?: string;
  note?: string;
}

export interface AppendSubsidyTransactionCommand {
  accountId: string;
  personId: string;
  policyVersionId: string;
  workOrderId?: string;
  transactionType: SubsidyTransactionType;
  amountKrw: number;
  reversesTransactionId?: string;
  reasonCode: string;
  note?: string;
}

export interface RepairWorkOrder {
  id: string;
  tenantId: string;
  beneficiaryId: string;
  deviceId: string;
  issueSummary: string;
  publicFundingInvolved: boolean;
  requestedAmountKrw?: number;
  repairStationId?: string;
  repairerFirebaseUid?: string;
  subsidyAccountId?: string;
  billedAmountKrw?: number;
  submittedAt?: string;
  subsidyDecisionId?: string;
  status: RepairStatus;
  revision: number;
  createdByUid: string;
  updatedByUid: string;
  createdAt: string;
  updatedAt: string;
}

export interface SubsidySummary {
  accountId: string;
  personId: string;
  policyVersionId: string;
  allocatedKrw: number;
  adjustmentKrw: number;
  reservedKrw: number;
  executedKrw: number;
  availableKrw: number;
  reservedByWorkOrder: Record<string, number>;
}

export interface CommandResult {
  commandType: 'create_repair_request' | 'transition_repair_request' | 'append_subsidy_transaction';
  tenantId: string;
  resourceId: string;
  revision?: number;
  status?: RepairStatus;
  eventId: string;
  transactionId?: string;
  idempotent?: boolean;
}

export interface DomainEvent {
  eventId: string;
  eventType: string;
  tenantId: string;
  aggregateType: 'repair' | 'subsidy_account';
  aggregateId: string;
  revision?: number;
  actorUid: string;
  actorRole: Role;
  occurredAt: string;
  payload: Record<string, unknown>;
}

export interface CommandErrorShape {
  code: string;
  message: string;
  status: number;
}
