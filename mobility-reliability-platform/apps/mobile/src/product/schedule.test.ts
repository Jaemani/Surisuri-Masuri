import { describe, expect, it } from 'vitest';

import { createDefaultSchedule, formatScheduleSeoul, isScheduleInRange, scheduleBounds } from './schedule';

describe('repair visit scheduling', () => {
  const now = new Date('2026-08-13T03:00:00.000Z');

  it('creates a future default and exposes the same 180 day maximum as the server contract', () => {
    const candidate = createDefaultSchedule(now);
    const bounds = scheduleBounds(now);

    expect(candidate.toISOString()).toBe('2026-08-14T05:00:00.000Z');
    expect(bounds.minimum.toISOString()).toBe(now.toISOString());
    expect(bounds.maximum.toISOString()).toBe('2027-02-09T03:00:00.000Z');
    expect(isScheduleInRange(candidate, now)).toBe(true);
  });

  it('rejects stale, invalid, and over-180-day values', () => {
    expect(isScheduleInRange(new Date('2026-08-13T02:59:59.999Z'), now)).toBe(false);
    expect(isScheduleInRange(new Date('invalid'), now)).toBe(false);
    expect(isScheduleInRange(new Date('2027-02-09T03:00:00.001Z'), now)).toBe(false);
  });

  it('formats the submitted instant in the presentation timezone', () => {
    const label = formatScheduleSeoul(new Date('2026-08-20T05:00:00.000Z'));
    expect(label).toContain('8월 20일');
    expect(label).toContain('오후 2:00');
  });
});
