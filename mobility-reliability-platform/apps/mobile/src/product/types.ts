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
  customer: string;
  device: string;
  issue: string;
  due: string;
  priority: 'today' | 'scheduled';
};

export type RoleSession = {
  role: ProductRole;
  displayName: string;
  isDemo: boolean;
};

export type ProductSnapshot = {
  roleSession: RoleSession;
  repairRequest: RepairWorkOrder | null;
  device: DeviceSummary;
  subsidy: SubsidySummary;
  repairJobs: RepairJob[];
};

export type CreateRepairRequestInput = {
  title: string;
  /** Optional command fields used by the production Domain Command adapter. */
  publicFundingInvolved?: boolean;
  requestedAmountKrw?: number;
  /** Reuse this key when a caller retries after an ambiguous network result. */
  idempotencyKey?: string;
};
