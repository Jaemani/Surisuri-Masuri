export interface MobileBeneficiarySnapshot {
  roleSession: { role: 'user'; displayName: string; isDemo: false };
  repairRequest: null | { id: string; title: string; createdAt: string; status: 'received' | 'assigned' | 'visit_scheduled' | 'completed'; repairer: string; visitAt: string };
  device: { id: string; name: string; registrationNumber: string; registeredAt: string; status: 'healthy' | 'attention'; timeline: Array<{ id: string; date: string; title: string; detail: string; tone: 'teal' | 'orange' | 'blue' }> };
  subsidy: { program: string; cycle: string; used: number; total: number; nextReview: string; note: string };
}

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
