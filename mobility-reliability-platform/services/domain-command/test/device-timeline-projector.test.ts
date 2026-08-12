import { describe, expect, test } from 'vitest';
import {
  DeviceTimelineProjectionError,
  replayDeviceRepairTimeline,
  type VerifiedRepairHeader,
  type VerifiedRepairItem,
} from '../src/device-timeline-projector.js';

const repairs: VerifiedRepairHeader[] = [
  { repairId: 'repair-b', tenantId: 'tenant-1', deviceId: 'device-1', occurredAt: '2026-08-02T00:00:00Z', status: 'completed', sourceQuality: 'verified' },
  { repairId: 'repair-a', tenantId: 'tenant-1', deviceId: 'device-1', occurredAt: '2026-08-01T00:00:00Z', status: 'completed', sourceQuality: 'verified' },
];
const items: VerifiedRepairItem[] = [
  { repairItemId: 'item-b', tenantId: 'tenant-1', repairId: 'repair-b', categoryCode: 'battery', actionCode: 'replace', quantity: 1, sourceQuality: 'verified' },
  { repairItemId: 'item-a', tenantId: 'tenant-1', repairId: 'repair-a', categoryCode: 'brakes', actionCode: 'repair', quantity: 1, sourceQuality: 'verified' },
];

describe('completed repair timeline replay', () => {
  test('is deterministic across unordered input and creates structured labels', () => {
    const first = replayDeviceRepairTimeline({ tenantId: 'tenant-1', deviceId: 'device-1', repairs, items });
    const second = replayDeviceRepairTimeline({ tenantId: 'tenant-1', deviceId: 'device-1', repairs: [...repairs].reverse(), items: [...items].reverse() });
    expect(first).toEqual(second);
    expect(first.entries.map((entry) => entry.detail)).toEqual(['브레이크 수리', '배터리 교체']);
    expect(first.categoryCounts).toMatchObject({ brakes: 1, battery: 1 });
    expect(first.canonicalChecksum).toMatch(/^[a-f0-9]{64}$/);
  });

  test('replays only repairs at or before an explicit as-of boundary', () => {
    const output = replayDeviceRepairTimeline({ tenantId: 'tenant-1', deviceId: 'device-1', repairs, items, asOf: '2026-08-01T12:00:00Z' });
    expect(output.entries).toHaveLength(1);
    expect(output.entries[0]?.id).toBe('repair-repair-a');
  });

  test.each([
    ['REPAIR_ID_DUPLICATE', [...repairs, repairs[0]!], items],
    ['REPAIR_DEVICE_MISMATCH', [{ ...repairs[0]!, deviceId: 'other-device' }], [items[0]!]],
    ['REPAIR_NOT_VERIFIED', [{ ...repairs[0]!, sourceQuality: 'imported' as never }], [items[0]!]],
    ['REPAIR_ITEM_ORPHAN', [repairs[0]!], [{ ...items[0]!, repairId: 'missing' }]],
    ['REPAIR_ITEM_ID_DUPLICATE', [repairs[0]!], [{ ...items[0]!, repairId: repairs[0]!.repairId }, { ...items[0]!, repairId: repairs[0]!.repairId }]],
    ['REPAIR_ITEM_QUANTITY_INVALID', [repairs[0]!], [{ ...items[0]!, quantity: 0 }]],
  ])('fails closed with %s', (code, candidateRepairs, candidateItems) => {
    try {
      replayDeviceRepairTimeline({ tenantId: 'tenant-1', deviceId: 'device-1', repairs: candidateRepairs, items: candidateItems });
      throw new Error('expected replay to fail');
    } catch (error) {
      expect(error).toBeInstanceOf(DeviceTimelineProjectionError);
      expect((error as DeviceTimelineProjectionError).code).toBe(code);
    }
  });
});
