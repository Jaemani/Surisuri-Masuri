import { createHash } from 'node:crypto';

export const DEVICE_STATE_PROJECTOR_VERSION = 'device-current-state@1';
export const MAX_DEVICE_EVENTS = 10_000;
export const MAX_TRIP_DISTANCE_METERS = 1_000_000;
export const MAX_TOTAL_DISTANCE_METERS = 1_000_000_000;

const componentCategories = ['battery', 'brake', 'controller', 'motor', 'seat_frame', 'wheel_tire'] as const;
const inspectionOutcomes = ['pass', 'attention_required', 'action_required'] as const;

export type ComponentCategory = (typeof componentCategories)[number];
export type InspectionOutcome = (typeof inspectionOutcomes)[number];

type DeviceEventBase = {
  eventId: string;
  schemaVersion: 'device-state-event.v1';
  tenantId: string;
  deviceId: string;
  occurredAt: string;
  recordedAt: string;
  sourceQuality: 'verified';
};

export type RepairRecordedEvent = DeviceEventBase & {
  eventType: 'repair.recorded';
  payload: {
    repairId: string;
  };
};

export type PartReplacedEvent = DeviceEventBase & {
  eventType: 'part.replaced';
  payload: {
    componentId: string;
    repairId: string;
    category: ComponentCategory;
    action: 'replaced';
  };
};

export type InspectionCompletedEvent = DeviceEventBase & {
  eventType: 'inspection.completed';
  payload: {
    inspectionId: string;
    outcome: InspectionOutcome;
  };
};

export type TripSummarizedEvent = DeviceEventBase & {
  eventType: 'trip.summarized';
  payload: {
    sessionId: string;
    distanceMeters: number;
  };
};

export type NormalizedDeviceEvent =
  | RepairRecordedEvent
  | PartReplacedEvent
  | InspectionCompletedEvent
  | TripSummarizedEvent;

export type DeviceCurrentState = {
  tenantId: string;
  deviceId: string;
  projectorVersion: typeof DEVICE_STATE_PROJECTOR_VERSION;
  checkpoint: {
    asOf: string | null;
    eventCount: number;
    lastOccurredAt: string | null;
    lastEventId: string | null;
  };
  repairs: {
    count: number;
    last: {
      eventId: string;
      repairId: string;
      occurredAt: string;
    } | null;
  };
  components: Record<string, {
    componentId: string;
    repairId: string;
    category: ComponentCategory;
    replacedAt: string;
    sourceEventId: string;
  }>;
  lastInspection: {
    inspectionId: string;
    outcome: InspectionOutcome;
    completedAt: string;
    sourceEventId: string;
  } | null;
  usage: {
    tripCount: number;
    distanceMeters: number;
    last: {
      eventId: string;
      occurredAt: string;
      sessionId: string;
      distanceMeters: number;
    } | null;
  };
  canonicalChecksum: string;
};

export class DeviceStateProjectionError extends Error {
  constructor(readonly code: string) {
    super(code);
    this.name = 'DeviceStateProjectionError';
  }
}

function asRecord(value: unknown, code: string): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new DeviceStateProjectionError(code);
  }
  return value as Record<string, unknown>;
}

function exactKeys(value: Record<string, unknown>, required: readonly string[], code: string): void {
  const actual = Object.keys(value).sort();
  const expected = [...required].sort();
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) {
    throw new DeviceStateProjectionError(code);
  }
}

function requiredString(value: unknown, code: string, maxLength = 128): string {
  if (typeof value !== 'string' || !value.trim() || value.length > maxLength || /\s/.test(value)) {
    throw new DeviceStateProjectionError(code);
  }
  return value;
}

function uuidIdentity(value: unknown, code: string): string {
  const identity = requiredString(value, code);
  if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(identity)) {
    throw new DeviceStateProjectionError(code);
  }
  return identity;
}

function componentIdentity(value: unknown, code: string): string {
  const identity = requiredString(value, code, 80);
  if (!/^[a-z0-9][a-z0-9_-]*$/.test(identity)) throw new DeviceStateProjectionError(code);
  return identity;
}

function normalizedTimestamp(value: unknown, code: string): { millis: number; iso: string } {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/.test(value)) {
    throw new DeviceStateProjectionError(code);
  }
  const millis = Date.parse(value);
  if (!Number.isFinite(millis)) throw new DeviceStateProjectionError(code);
  return { millis, iso: new Date(millis).toISOString() };
}

function enumValue<T extends string>(value: unknown, allowed: readonly T[], code: string): T {
  if (typeof value !== 'string' || !allowed.includes(value as T)) {
    throw new DeviceStateProjectionError(code);
  }
  return value as T;
}

function boundedDistance(value: unknown, code: string): number {
  if (!Number.isSafeInteger(value) || (value as number) < 0 || (value as number) > MAX_TRIP_DISTANCE_METERS) {
    throw new DeviceStateProjectionError(code);
  }
  return value as number;
}

function canonical(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonical).join(',')}]`;
  if (value && typeof value === 'object') {
    return `{${Object.entries(value as Record<string, unknown>)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([key, child]) => `${JSON.stringify(key)}:${canonical(child)}`)
      .join(',')}}`;
  }
  const serialized = JSON.stringify(value);
  if (serialized === undefined) throw new DeviceStateProjectionError('CANONICAL_VALUE_INVALID');
  return serialized;
}

function normalizeEvent(value: unknown): NormalizedDeviceEvent {
  const record = asRecord(value, 'EVENT_INVALID');
  exactKeys(record, ['schemaVersion', 'eventId', 'eventType', 'tenantId', 'deviceId', 'occurredAt', 'recordedAt', 'sourceQuality', 'payload'], 'EVENT_KEYS_INVALID');
  if (record.schemaVersion !== 'device-state-event.v1') throw new DeviceStateProjectionError('EVENT_SCHEMA_VERSION_INVALID');
  if (record.sourceQuality !== 'verified') throw new DeviceStateProjectionError('EVENT_SOURCE_NOT_VERIFIED');
  const eventId = uuidIdentity(record.eventId, 'EVENT_ID_INVALID');
  const tenantId = uuidIdentity(record.tenantId, 'EVENT_TENANT_ID_INVALID');
  const deviceId = uuidIdentity(record.deviceId, 'EVENT_DEVICE_ID_INVALID');
  const occurredAt = normalizedTimestamp(record.occurredAt, 'EVENT_OCCURRED_AT_INVALID').iso;
  const recordedAt = normalizedTimestamp(record.recordedAt, 'EVENT_RECORDED_AT_INVALID').iso;
  if (Date.parse(recordedAt) < Date.parse(occurredAt)) throw new DeviceStateProjectionError('EVENT_RECORDED_BEFORE_OCCURRED');
  const payload = asRecord(record.payload, 'EVENT_PAYLOAD_INVALID');
  const base = { eventId, schemaVersion: 'device-state-event.v1' as const, tenantId, deviceId, occurredAt, recordedAt, sourceQuality: 'verified' as const };

  switch (record.eventType) {
    case 'repair.recorded': {
      exactKeys(payload, ['repairId'], 'REPAIR_PAYLOAD_KEYS_INVALID');
      return {
        ...base,
        eventType: record.eventType,
        payload: { repairId: uuidIdentity(payload.repairId, 'REPAIR_ID_INVALID') },
      };
    }
    case 'part.replaced': {
      exactKeys(payload, ['repairId', 'componentId', 'category', 'action'], 'PART_PAYLOAD_KEYS_INVALID');
      if (payload.action !== 'replaced') throw new DeviceStateProjectionError('PART_ACTION_INVALID');
      return {
        ...base,
        eventType: record.eventType,
        payload: {
          repairId: uuidIdentity(payload.repairId, 'PART_REPAIR_ID_INVALID'),
          componentId: componentIdentity(payload.componentId, 'COMPONENT_ID_INVALID'),
          category: enumValue(payload.category, componentCategories, 'COMPONENT_CATEGORY_INVALID'),
          action: 'replaced',
        },
      };
    }
    case 'inspection.completed': {
      exactKeys(payload, ['inspectionId', 'outcome'], 'INSPECTION_PAYLOAD_KEYS_INVALID');
      return {
        ...base,
        eventType: record.eventType,
        payload: {
          inspectionId: uuidIdentity(payload.inspectionId, 'INSPECTION_ID_INVALID'),
          outcome: enumValue(payload.outcome, inspectionOutcomes, 'INSPECTION_OUTCOME_INVALID'),
        },
      };
    }
    case 'trip.summarized': {
      exactKeys(payload, ['sessionId', 'distanceMeters'], 'TRIP_PAYLOAD_KEYS_INVALID');
      return {
        ...base,
        eventType: record.eventType,
        payload: { sessionId: uuidIdentity(payload.sessionId, 'TRIP_SESSION_ID_INVALID'), distanceMeters: boundedDistance(payload.distanceMeters, 'TRIP_DISTANCE_INVALID') },
      };
    }
    default:
      throw new DeviceStateProjectionError('EVENT_TYPE_INVALID');
  }
}

export function projectDeviceCurrentState(input: {
  tenantId: string;
  deviceId: string;
  events: readonly NormalizedDeviceEvent[];
  asOf?: string;
}): DeviceCurrentState {
  const tenantId = uuidIdentity(input.tenantId, 'TENANT_ID_REQUIRED');
  const deviceId = uuidIdentity(input.deviceId, 'DEVICE_ID_REQUIRED');
  if (!Array.isArray(input.events) || input.events.length > MAX_DEVICE_EVENTS) {
    throw new DeviceStateProjectionError('EVENTS_INVALID');
  }
  const asOf = input.asOf === undefined ? null : normalizedTimestamp(input.asOf, 'AS_OF_INVALID');
  const normalized = input.events.map(normalizeEvent);
  const eventIds = new Set<string>();
  for (const event of normalized) {
    if (event.tenantId !== tenantId) throw new DeviceStateProjectionError('EVENT_TENANT_MISMATCH');
    if (event.deviceId !== deviceId) throw new DeviceStateProjectionError('EVENT_DEVICE_MISMATCH');
    if (eventIds.has(event.eventId)) throw new DeviceStateProjectionError('EVENT_ID_DUPLICATE');
    eventIds.add(event.eventId);
  }
  const ordered = normalized
    .filter((event) => asOf === null || Date.parse(event.occurredAt) <= asOf.millis)
    .sort((left, right) => Date.parse(left.occurredAt) - Date.parse(right.occurredAt) || left.eventId.localeCompare(right.eventId));

  const components: DeviceCurrentState['components'] = {};
  let lastRepair: DeviceCurrentState['repairs']['last'] = null;
  let lastInspection: DeviceCurrentState['lastInspection'] = null;
  let distanceMeters = 0;
  let tripCount = 0;
  let lastTrip: DeviceCurrentState['usage']['last'] = null;

  for (const event of ordered) {
    if (event.eventType === 'repair.recorded') {
      lastRepair = { eventId: event.eventId, occurredAt: event.occurredAt, ...event.payload };
    } else if (event.eventType === 'part.replaced') {
      components[event.payload.componentId] = {
        componentId: event.payload.componentId,
        repairId: event.payload.repairId,
        category: event.payload.category,
        replacedAt: event.occurredAt,
        sourceEventId: event.eventId,
      };
    } else if (event.eventType === 'inspection.completed') {
      lastInspection = {
        inspectionId: event.payload.inspectionId,
        outcome: event.payload.outcome,
        completedAt: event.occurredAt,
        sourceEventId: event.eventId,
      };
    } else {
      if (distanceMeters > MAX_TOTAL_DISTANCE_METERS - event.payload.distanceMeters) {
        throw new DeviceStateProjectionError('TOTAL_DISTANCE_INVALID');
      }
      distanceMeters += event.payload.distanceMeters;
      tripCount += 1;
      lastTrip = { eventId: event.eventId, occurredAt: event.occurredAt, sessionId: event.payload.sessionId, distanceMeters: event.payload.distanceMeters };
    }
  }

  const last = ordered.at(-1);
  const stateWithoutChecksum = {
    tenantId,
    deviceId,
    projectorVersion: DEVICE_STATE_PROJECTOR_VERSION,
    checkpoint: {
      asOf: asOf?.iso ?? null,
      eventCount: ordered.length,
      lastOccurredAt: last?.occurredAt ?? null,
      lastEventId: last?.eventId ?? null,
    },
    repairs: { count: ordered.filter((event) => event.eventType === 'repair.recorded').length, last: lastRepair },
    components,
    lastInspection,
    usage: { tripCount, distanceMeters, last: lastTrip },
  } satisfies Omit<DeviceCurrentState, 'canonicalChecksum'>;
  const canonicalChecksum = createHash('sha256').update(canonical(stateWithoutChecksum)).digest('hex');
  return { ...stateWithoutChecksum, canonicalChecksum };
}
