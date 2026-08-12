export type UserTab = 'home' | 'repairs' | 'device' | 'support' | 'settings';

export type ProductRole = 'user' | 'repairer';

export type RepairRequestStatus = 'received' | 'assigned' | 'visit_scheduled' | 'completed';

export type RepairWorkOrder = {
  id: string;
  title: string;
  createdAt: string;
  status: RepairRequestStatus;
  repairer: string;
  visitAt: string;
};

/** Kept as a product-language alias for screens that call a work order a request. */
export type RepairRequest = RepairWorkOrder;

export type DeviceTimelineItem = {
  id: string;
  date: string;
  title: string;
  detail: string;
  tone: 'teal' | 'orange' | 'blue';
};

export type SubsidySummary = {
  program: string;
  cycle: string;
  used: number;
  total: number;
  nextReview: string;
  note: string;
};

export type DeviceSummary = {
  id: string;
  name: string;
  registrationNumber: string;
  registeredAt: string;
  status: 'healthy' | 'attention';
  timeline: DeviceTimelineItem[];
};

export type RepairJob = {
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
  allowedActions: Array<'schedule' | 'start' | 'submit' | 'resume'>;
};

export type RoleSession = {
  role: ProductRole;
  displayName: string;
  isDemo: boolean;
};

export type UserRoleSession = { role: 'user'; displayName: string; isDemo: boolean };
export type RepairerRoleSession = { role: 'repairer'; displayName: string; isDemo: boolean };

export type BeneficiaryProductSnapshot = {
  roleSession: UserRoleSession;
  repairRequest: RepairWorkOrder | null;
  device: DeviceSummary;
  subsidy: SubsidySummary;
  /** Demo may include repair jobs for the role switch preview; production omits this field. */
  repairJobs?: RepairJob[];
};

export type RepairerProductSnapshot = {
  roleSession: RepairerRoleSession;
  repairJobs: RepairJob[];
};

export type ProductSnapshot = BeneficiaryProductSnapshot | RepairerProductSnapshot;

/** The deterministic preview seed keeps its optional demo jobs populated. */
export type DemoProductSnapshot = BeneficiaryProductSnapshot & { repairJobs: RepairJob[] };

export function isRepairerProductSnapshot(snapshot: ProductSnapshot): snapshot is RepairerProductSnapshot {
  return snapshot.roleSession.role === 'repairer';
}

export function isBeneficiaryProductSnapshot(snapshot: ProductSnapshot): snapshot is BeneficiaryProductSnapshot {
  return snapshot.roleSession.role === 'user';
}

export type CreateRepairRequestInput = {
  title: string;
  /** Optional command fields used by the production Domain Command adapter. */
  publicFundingInvolved?: boolean;
  requestedAmountKrw?: number;
  /** Reuse this key when a caller retries after an ambiguous network result. */
  idempotencyKey?: string;
};

export type RepairerJobCommand =
  | { action: 'schedule'; repairRequestId: string; expectedRevision: number; scheduledAt: string; idempotencyKey: string }
  | { action: 'start' | 'resume'; repairRequestId: string; expectedRevision: number; idempotencyKey: string }
  | { action: 'submit'; repairRequestId: string; expectedRevision: number; billedAmountKrw: number; idempotencyKey: string };
