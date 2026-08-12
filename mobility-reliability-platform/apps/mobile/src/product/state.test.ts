import { describe, expect, it } from 'vitest';

import { demoProductSnapshot } from './repository';
import { isBeneficiaryProductSnapshot } from './types';
import {
  formatMoney,
  getRepairProgress,
  getSubsidyProgressPercent,
  getSubsidyRemaining,
  selectProductView,
} from './state';

describe('product state transformations', () => {
  it('maps work-order status to a stable progress index', () => {
    expect(getRepairProgress('received').currentIndex).toBe(0);
    expect(getRepairProgress('assigned').currentIndex).toBe(1);
    expect(getRepairProgress('completed').currentIndex).toBe(3);
    expect(getRepairProgress('assigned').steps.map((step) => step.label)).toEqual([
      '접수',
      '수리센터 배정',
      '방문 예정',
      '완료',
    ]);
  });

  it('derives bounded subsidy values for presentation', () => {
    expect(getSubsidyRemaining(demoProductSnapshot.subsidy)).toBe(120000);
    expect(getSubsidyProgressPercent(demoProductSnapshot.subsidy)).toBe(60);
    expect(getSubsidyRemaining({ ...demoProductSnapshot.subsidy, used: 400000 })).toBe(0);
    expect(getSubsidyProgressPercent({ ...demoProductSnapshot.subsidy, total: 0 })).toBe(0);
    expect(formatMoney(120000)).toBe('120,000원');
  });

  it('selects the role-safe screen view without changing repository data', () => {
    if (!isBeneficiaryProductSnapshot(demoProductSnapshot)) throw new Error('expected beneficiary snapshot');
    const view = selectProductView(demoProductSnapshot);
    expect(view).toMatchObject({ role: 'user', displayName: '김정자 님', isDemo: true });
    if (view.role !== 'user') throw new Error('expected beneficiary view');
    expect(view.device.id).toBe('demo-device-2208');
    expect(view.repairJobs).toHaveLength(3);
  });
});
