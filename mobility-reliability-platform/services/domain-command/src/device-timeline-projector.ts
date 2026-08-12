import { createHash } from 'node:crypto';

const categories = {
  wheel_tire: '바퀴·타이어',
  battery: '배터리',
  brakes: '브레이크',
  controls: '조작부',
  seat_frame: '시트·프레임',
  other: '기타',
} as const;

const actions = {
  inspect: '점검',
  adjust: '조정',
  repair: '수리',
  replace: '교체',
} as const;

export type VerifiedRepairHeader = {
  repairId: string;
  tenantId: string;
  deviceId: string;
  occurredAt: string;
  status: 'completed';
  sourceQuality: 'verified';
};

export type VerifiedRepairItem = {
  repairItemId: string;
  tenantId: string;
  repairId: string;
  categoryCode: keyof typeof categories;
  actionCode: keyof typeof actions;
  quantity: number;
  sourceQuality: 'verified';
};

export type DeviceRepairTimelineEntry = {
  id: string;
  occurredAt: string;
  title: string;
  detail: string;
  itemCount: number;
};

export type DeviceRepairTimeline = {
  entries: DeviceRepairTimelineEntry[];
  categoryCounts: Record<keyof typeof categories, number>;
  canonicalChecksum: string;
};

export class DeviceTimelineProjectionError extends Error {
  constructor(readonly code: string) {
    super(code);
    this.name = 'DeviceTimelineProjectionError';
  }
}

function requiredIdentity(value: string, code: string): string {
  if (!value.trim()) throw new DeviceTimelineProjectionError(code);
  return value;
}

function timestamp(value: string, code: string): number {
  const parsed = Date.parse(value);
  if (!Number.isFinite(parsed)) throw new DeviceTimelineProjectionError(code);
  return parsed;
}

function canonical(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonical).join(',')}]`;
  if (value && typeof value === 'object') {
    return `{${Object.entries(value as Record<string, unknown>)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([key, child]) => `${JSON.stringify(key)}:${canonical(child)}`)
      .join(',')}}`;
  }
  return JSON.stringify(value);
}

export function replayDeviceRepairTimeline(input: {
  tenantId: string;
  deviceId: string;
  repairs: VerifiedRepairHeader[];
  items: VerifiedRepairItem[];
  asOf?: string;
}): DeviceRepairTimeline {
  const tenantId = requiredIdentity(input.tenantId, 'TENANT_ID_REQUIRED');
  const deviceId = requiredIdentity(input.deviceId, 'DEVICE_ID_REQUIRED');
  const asOf = input.asOf ? timestamp(input.asOf, 'AS_OF_INVALID') : Number.POSITIVE_INFINITY;
  const repairIds = new Set<string>();
  const eligible = input.repairs
    .map((repair) => {
      requiredIdentity(repair.repairId, 'REPAIR_ID_REQUIRED');
      if (repairIds.has(repair.repairId)) throw new DeviceTimelineProjectionError('REPAIR_ID_DUPLICATE');
      repairIds.add(repair.repairId);
      if (repair.tenantId !== tenantId) throw new DeviceTimelineProjectionError('REPAIR_TENANT_MISMATCH');
      if (repair.deviceId !== deviceId) throw new DeviceTimelineProjectionError('REPAIR_DEVICE_MISMATCH');
      if (repair.status !== 'completed' || repair.sourceQuality !== 'verified') {
        throw new DeviceTimelineProjectionError('REPAIR_NOT_VERIFIED');
      }
      const occurred = timestamp(repair.occurredAt, 'REPAIR_OCCURRED_AT_INVALID');
      return { ...repair, occurred };
    })
    .filter((repair) => repair.occurred <= asOf)
    .sort((left, right) => left.occurred - right.occurred || left.repairId.localeCompare(right.repairId));

  const itemIds = new Set<string>();
  const itemByRepair = new Map<string, VerifiedRepairItem[]>();
  for (const item of input.items) {
    requiredIdentity(item.repairItemId, 'REPAIR_ITEM_ID_REQUIRED');
    const scopedItemId = `${item.repairId}:${item.repairItemId}`;
    if (itemIds.has(scopedItemId)) throw new DeviceTimelineProjectionError('REPAIR_ITEM_ID_DUPLICATE');
    itemIds.add(scopedItemId);
    if (item.tenantId !== tenantId) throw new DeviceTimelineProjectionError('REPAIR_ITEM_TENANT_MISMATCH');
    if (!repairIds.has(item.repairId)) throw new DeviceTimelineProjectionError('REPAIR_ITEM_ORPHAN');
    if (!(item.categoryCode in categories) || !(item.actionCode in actions)) {
      throw new DeviceTimelineProjectionError('REPAIR_ITEM_CODE_INVALID');
    }
    if (!Number.isSafeInteger(item.quantity) || item.quantity < 1 || item.quantity > 100) {
      throw new DeviceTimelineProjectionError('REPAIR_ITEM_QUANTITY_INVALID');
    }
    if (item.sourceQuality !== 'verified') {
      throw new DeviceTimelineProjectionError('REPAIR_ITEM_NOT_VERIFIED');
    }
    const existing = itemByRepair.get(item.repairId) ?? [];
    existing.push(item);
    itemByRepair.set(item.repairId, existing);
  }

  const categoryCounts: DeviceRepairTimeline['categoryCounts'] = {
    wheel_tire: 0,
    battery: 0,
    brakes: 0,
    controls: 0,
    seat_frame: 0,
    other: 0,
  };
  const entries = eligible.map((repair) => {
    const repairItems = (itemByRepair.get(repair.repairId) ?? []).sort(
      (left, right) => left.repairItemId.localeCompare(right.repairItemId),
    );
    if (repairItems.length === 0) throw new DeviceTimelineProjectionError('VERIFIED_REPAIR_ITEMS_REQUIRED');
    for (const item of repairItems) categoryCounts[item.categoryCode] += item.quantity;
    const labels = repairItems.map(
      (item) => `${categories[item.categoryCode]} ${actions[item.actionCode]}${item.quantity > 1 ? ` ${item.quantity}개` : ''}`,
    );
    return {
      id: `repair-${repair.repairId}`,
      occurredAt: new Date(repair.occurred).toISOString(),
      title: '수리를 완료했어요',
      detail: labels.join(' · '),
      itemCount: repairItems.reduce((sum, item) => sum + item.quantity, 0),
    };
  });
  const checksumPayload = { tenantId, deviceId, entries, categoryCounts };
  return {
    entries,
    categoryCounts,
    canonicalChecksum: createHash('sha256').update(canonical(checksumPayload)).digest('hex'),
  };
}
