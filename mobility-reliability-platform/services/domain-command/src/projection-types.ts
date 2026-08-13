export interface MobileBeneficiarySnapshot {
  roleSession: { role: 'user'; displayName: string; isDemo: false };
  repairRequest: null | { id: string; title: string; createdAt: string; status: 'received' | 'assigned' | 'visit_scheduled' | 'completed'; repairer: string; visitAt: string };
  device: { id: string; name: string; registrationNumber: string; registeredAt: string; status: 'healthy' | 'attention'; timeline: DeviceTimelineEntry[] };
  subsidy: { program: string; cycle: string; used: number; total: number; nextReview: string; note: string };
}

export type DeviceTimelineEntry = {
  id: string;
  date: string;
  title: string;
  detail: string;
  tone: 'teal' | 'orange' | 'blue';
};

export type ConsoleDeviceRecord = {
  id: string;
  user: string;
  model: string;
  health: string;
  battery: string;
  mileage: string;
  inspection: string;
  state: string;
  /** Recent entries replayed from verified completed repair archive only. */
  timeline: ConsoleDeviceTimelineEntry[];
};

export type ConsoleDeviceTimelineEntry = {
  id: string;
  date: string;
  title: string;
  detail: string;
  tone: 'success' | 'warning' | 'info';
};

export type ConsoleRepairSubsidyExecutionState = 'verification_required' | 'execution_pending' | 'executed';

export type ConsoleRepairSubsidyContext = {
  /** Only emitted after the referenced, tenant-scoped account is validated. */
  accountId: string;
  personId: string;
  policyVersionId: string;
  decisionId?: string;
  /** Remaining reservation available for this exact work order. */
  reservedAmountKrw: number;
  /** Net immutable execution amount after exact reversals. */
  executedAmountKrw: number;
  executionState: ConsoleRepairSubsidyExecutionState;
};

export type ConsoleRepairRecord = {
  id: string;
  user: string;
  device: string;
  issue: string;
  request: string;
  partner: string;
  amount: string;
  workItems: Array<{ categoryCode: string; categoryLabel: string; actionCode: string; actionLabel: string; quantity: number; lineAmountKrw: number }>;
  stage: 'new' | 'assigned' | 'submitted' | 'verified';
  domainStatus: import('./types.js').RepairStatus;
  publicFundingInvolved: boolean;
  billedAmountKrw: number | null;
  subsidyContext: ConsoleRepairSubsidyContext | null;
  priority: string;
  revision: number;
};

export type ConsoleLedgerTransactionType = import('./types.js').SubsidyTransactionType;

export type ConsoleLedgerEntry = {
  date: string;
  id: string;
  transactionId: string;
  transactionType: ConsoleLedgerTransactionType;
  user: string;
  item: string;
  amount: string;
  amountKrw: number;
  state: '예약' | '집행 완료' | '예약 취소';
  actor: string;
};

export interface MobileRepairerSnapshot {
  roleSession: { role: 'repairer'; displayName: string; isDemo: false };
  repairJobs: Array<{
    id: string;
    revision: number;
    status: 'assigned' | 'scheduled' | 'in_progress' | 'repairer_submitted' | 'needs_correction' | 'center_verified';
    customerLabel: string;
    device: { publicCode: string; model: string };
    issue: string;
    scheduledAt: string | null;
    scheduleLabel: string;
    priority: 'today' | 'scheduled';
    billedAmountKrw: number | null;
    submittedAt: string | null;
    workItems: Array<{ categoryCode: string; categoryLabel: string; actionCode: string; actionLabel: string; quantity: number; lineAmountKrw: number }>;
    allowedActions: Array<'schedule' | 'start' | 'submit' | 'resume'>;
  }>;
}

export type MobileProductSnapshot = MobileBeneficiarySnapshot | MobileRepairerSnapshot;

export type ConsoleProjectionName = 'dashboard' | 'users' | 'devices' | 'repairs' | 'ledger' | 'inspections' | 'partners' | 'reports' | 'services';

export interface ProductProjectionStore {
  getMobileSnapshot(actor: import('./types.js').ActorContext): Promise<MobileProductSnapshot>;
  getConsoleProjection(actor: import('./types.js').ActorContext, projection: ConsoleProjectionName): Promise<unknown>;
}
