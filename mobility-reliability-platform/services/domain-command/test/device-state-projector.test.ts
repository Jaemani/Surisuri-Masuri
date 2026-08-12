import { describe, expect, test } from 'vitest';
import {
  DeviceStateProjectionError,
  DEVICE_STATE_PROJECTOR_VERSION,
  projectDeviceCurrentState,
  type NormalizedDeviceEvent,
} from '../src/device-state-projector.js';

const scope = { tenantId: '09460fa5-dc3c-4c14-9c00-d1956206292c', deviceId: 'cd230e0f-a01c-47e5-830d-3a1330e591d6' } as const;
const base = {
  ...scope,
  schemaVersion: 'device-state-event.v1' as const,
  recordedAt: '2026-09-05T00:00:00Z',
  sourceQuality: 'verified' as const,
};
const event = (input: Omit<NormalizedDeviceEvent, keyof typeof base>): NormalizedDeviceEvent => ({ ...base, ...input } as NormalizedDeviceEvent);

const events: NormalizedDeviceEvent[] = [
  event({ eventId: 'f1a4ef7b-fb6f-47cf-919c-8734e469d2b2', eventType: 'trip.summarized', occurredAt: '2026-09-02T00:00:00Z', payload: { sessionId: '0a7299a5-3f18-4f47-b0c8-6f1d9ef5ee62', distanceMeters: 2300 } }),
  event({ eventId: 'f1a4ef7b-fb6f-47cf-919c-8734e469d2b1', eventType: 'repair.recorded', occurredAt: '2026-09-01T00:00:00Z', payload: { repairId: '0a7299a5-3f18-4f47-b0c8-6f1d9ef5ee60' } }),
  event({ eventId: 'f1a4ef7b-fb6f-47cf-919c-8734e469d2b3', eventType: 'part.replaced', occurredAt: '2026-09-03T00:00:00Z', payload: { repairId: '0a7299a5-3f18-4f47-b0c8-6f1d9ef5ee60', componentId: 'battery-main', category: 'battery', action: 'replaced' } }),
  event({ eventId: 'f1a4ef7b-fb6f-47cf-919c-8734e469d2b4', eventType: 'inspection.completed', occurredAt: '2026-09-04T00:00:00Z', payload: { inspectionId: '0a7299a5-3f18-4f47-b0c8-6f1d9ef5ee63', outcome: 'pass' } }),
  event({ eventId: 'f1a4ef7b-fb6f-47cf-919c-8734e469d2b5', eventType: 'trip.summarized', occurredAt: '2026-09-01T12:00:00Z', payload: { sessionId: '0a7299a5-3f18-4f47-b0c8-6f1d9ef5ee61', distanceMeters: 1200 } }),
];

describe('device current-state projector', () => {
  test('replays the frozen event contract out of order with one deterministic checksum', () => {
    const first = projectDeviceCurrentState({ ...scope, events });
    const second = projectDeviceCurrentState({ ...scope, events: [...events].reverse() });

    expect(first).toEqual(second);
    expect(first.projectorVersion).toBe(DEVICE_STATE_PROJECTOR_VERSION);
    expect(first.checkpoint).toMatchObject({ eventCount: 5, lastEventId: 'f1a4ef7b-fb6f-47cf-919c-8734e469d2b4', lastOccurredAt: '2026-09-04T00:00:00.000Z' });
    expect(first.repairs).toEqual({ count: 1, last: { eventId: 'f1a4ef7b-fb6f-47cf-919c-8734e469d2b1', repairId: '0a7299a5-3f18-4f47-b0c8-6f1d9ef5ee60', occurredAt: '2026-09-01T00:00:00.000Z' } });
    expect(first.usage).toMatchObject({ tripCount: 2, distanceMeters: 3500, last: { eventId: 'f1a4ef7b-fb6f-47cf-919c-8734e469d2b2', sessionId: '0a7299a5-3f18-4f47-b0c8-6f1d9ef5ee62' } });
    expect(first.components).toEqual({
      'battery-main': { componentId: 'battery-main', repairId: '0a7299a5-3f18-4f47-b0c8-6f1d9ef5ee60', category: 'battery', replacedAt: '2026-09-03T00:00:00.000Z', sourceEventId: 'f1a4ef7b-fb6f-47cf-919c-8734e469d2b3' },
    });
    expect(first.canonicalChecksum).toMatch(/^[a-f0-9]{64}$/);
  });

  test('uses occurredAt then eventId ordering and asOf excludes later events', () => {
    const sameTime: NormalizedDeviceEvent[] = [
      event({ eventId: 'f1a4ef7b-fb6f-47cf-919c-8734e469d2bf', eventType: 'inspection.completed', occurredAt: '2026-09-01T00:00:00Z', payload: { inspectionId: '0a7299a5-3f18-4f47-b0c8-6f1d9ef5ee6f', outcome: 'pass' } }),
      event({ eventId: 'f1a4ef7b-fb6f-47cf-919c-8734e469d2ba', eventType: 'inspection.completed', occurredAt: '2026-09-01T00:00:00Z', payload: { inspectionId: '0a7299a5-3f18-4f47-b0c8-6f1d9ef5ee6a', outcome: 'attention_required' } }),
    ];
    const output = projectDeviceCurrentState({ ...scope, events: sameTime, asOf: '2026-09-01T00:00:00Z' });
    expect(output.lastInspection).toMatchObject({ inspectionId: '0a7299a5-3f18-4f47-b0c8-6f1d9ef5ee6f', sourceEventId: 'f1a4ef7b-fb6f-47cf-919c-8734e469d2bf' });
    expect(output.checkpoint).toMatchObject({ asOf: '2026-09-01T00:00:00.000Z', eventCount: 2 });
  });

  test('does not infer a component from a repair event without explicit component linkage', () => {
    const output = projectDeviceCurrentState({ ...scope, events: [event({ eventId: 'f1a4ef7b-fb6f-47cf-919c-8734e469d2bc', eventType: 'repair.recorded', occurredAt: '2026-09-01T00:00:00Z', payload: { repairId: '0a7299a5-3f18-4f47-b0c8-6f1d9ef5ee6c' } })] });
    expect(output.repairs.last).toMatchObject({ repairId: '0a7299a5-3f18-4f47-b0c8-6f1d9ef5ee6c' });
    expect(output.components).toEqual({});
  });

  test.each([
    ['EVENT_ID_DUPLICATE', [events[0], events[0]]],
    ['EVENT_TENANT_MISMATCH', [{ ...events[0], tenantId: '19460fa5-dc3c-4c14-9c00-d1956206292c' }]],
    ['EVENT_DEVICE_MISMATCH', [{ ...events[0], deviceId: 'dd230e0f-a01c-47e5-830d-3a1330e591d6' }]],
    ['EVENT_SOURCE_NOT_VERIFIED', [{ ...events[0], sourceQuality: 'unverified' }]],
    ['EVENT_SCHEMA_VERSION_INVALID', [{ ...events[0], schemaVersion: 'device-state-event.v2' }]],
    ['PART_PAYLOAD_KEYS_INVALID', [{ ...events[2], payload: { ...events[2]!.payload, note: 'free text' } }]],
    ['COMPONENT_ID_INVALID', [{ ...events[2], payload: { ...events[2]!.payload, componentId: 'battery main' } }]],
    ['TRIP_DISTANCE_INVALID', [{ ...events[0], payload: { sessionId: '0a7299a5-3f18-4f47-b0c8-6f1d9ef5ee62', distanceMeters: 1_000_001 } }]],
    ['EVENT_RECORDED_BEFORE_OCCURRED', [{ ...events[0], recordedAt: '2026-08-01T00:00:00Z' }]],
  ])('fails closed with %s', (code, candidate) => {
    expect(() => projectDeviceCurrentState({ ...scope, events: candidate as NormalizedDeviceEvent[] })).toThrowError(new DeviceStateProjectionError(code));
  });
});
