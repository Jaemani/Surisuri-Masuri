import { isRepairerProductSnapshot } from './types';
import type { ProductSnapshot, RepairRequestStatus, SubsidySummary } from './types';

export const repairProgressSteps: Array<{ status: RepairRequestStatus; label: string }> = [
  { status: 'received', label: '접수' },
  { status: 'assigned', label: '수리센터 배정' },
  { status: 'visit_scheduled', label: '방문 예정' },
  { status: 'completed', label: '완료' },
];

export function getRepairProgress(status: RepairRequestStatus) {
  const currentIndex = repairProgressSteps.findIndex((step) => step.status === status);
  return {
    currentIndex: Math.max(currentIndex, 0),
    steps: repairProgressSteps,
  };
}

export function getSubsidyRemaining(summary: SubsidySummary) {
  return Math.max(summary.total - summary.used, 0);
}

export function getSubsidyProgressPercent(summary: SubsidySummary) {
  if (summary.total <= 0) return 0;
  return Math.min(Math.round((summary.used / summary.total) * 100), 100);
}

export function formatMoney(value: number) {
  return `${value.toLocaleString('ko-KR')}원`;
}

/** Converts the role-discriminated server DTO into the screen-specific view. */
export function selectProductView(snapshot: ProductSnapshot) {
  if (isRepairerProductSnapshot(snapshot)) {
    return {
      role: 'repairer' as const,
      displayName: snapshot.roleSession.displayName,
      isDemo: snapshot.roleSession.isDemo,
      repairJobs: snapshot.repairJobs,
    };
  }
  return {
    role: 'user' as const,
    displayName: snapshot.roleSession.displayName,
    isDemo: snapshot.roleSession.isDemo,
    repairRequest: snapshot.repairRequest,
    device: snapshot.device,
    subsidy: snapshot.subsidy,
    repairJobs: snapshot.repairJobs ?? [],
  };
}
