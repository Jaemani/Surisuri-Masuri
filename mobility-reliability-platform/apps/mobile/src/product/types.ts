export type UserTab = 'home' | 'repairs' | 'device' | 'support' | 'settings';

export type ProductRole = 'user' | 'repairer';

export type RepairRequestStatus = 'received' | 'assigned' | 'visit_scheduled' | 'completed';

export type RepairRequest = {
  id: string;
  title: string;
  createdAt: string;
  status: RepairRequestStatus;
  repairer: string;
  visitAt: string;
};

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

export type RepairJob = {
  id: string;
  customer: string;
  device: string;
  issue: string;
  due: string;
  priority: 'today' | 'scheduled';
};
