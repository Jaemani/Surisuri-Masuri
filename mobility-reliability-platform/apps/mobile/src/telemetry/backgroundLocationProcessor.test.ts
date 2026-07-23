import { describe, expect, it, vi } from 'vitest';

import {
  processBackgroundLocationBatch,
  type BackgroundLocationInput,
} from './backgroundLocationProcessor';
import type { TripSessionSummary } from './database';

const SESSION: TripSessionSummary = {
  sessionId: '018f47f2-4512-7d4a-8f7b-4e3fbecbb8b2',
  startedAt: '2026-07-23T00:00:00.000Z',
  endedAt: null,
  state: 'recording',
  nextEventSequence: 1,
  nextSampleSequence: 0,
  acceptedSampleCount: 0,
  rejectedSampleCount: 0,
  uploadEligibility: 'development_local_only',
  lastSampleAt: '2026-07-23T00:00:05.000Z',
};

function location(timestamp: number, overrides: Partial<BackgroundLocationInput['coords']> = {}) {
  return {
    timestamp,
    mocked: false,
    coords: {
      latitude: 37.5665,
      longitude: 126.978,
      accuracy: 8,
      altitude: null,
      speed: 1.2,
      heading: 90,
      ...overrides,
    },
  } satisfies BackgroundLocationInput;
}

describe('processBackgroundLocationBatch', () => {
  it('does not write when there is no active session', async () => {
    const appendLocationSample = vi.fn();
    const recordRejectedSample = vi.fn();

    await expect(
      processBackgroundLocationBatch([location(Date.parse(SESSION.startedAt) + 10_000)], {
        getActiveTripSession: async () => null,
        appendLocationSample,
        recordRejectedSample,
        now: () => Date.parse(SESSION.startedAt) + 60_000,
      }),
    ).resolves.toEqual({ kind: 'no_active_session' });
    expect(appendLocationSample).not.toHaveBeenCalled();
    expect(recordRejectedSample).not.toHaveBeenCalled();
  });

  it('sorts a native batch, ignores replayed timestamps and records bounded counts', async () => {
    const appendedTimestamps: number[] = [];
    const rejectedReasons: string[] = [];
    const appendLocationSample = vi.fn(async (_sessionId, sample) => {
      appendedTimestamps.push(sample.timestamp);
      return SESSION;
    });
    const recordRejectedSample = vi.fn(async (_sessionId, reason) => {
      rejectedReasons.push(reason);
      return SESSION;
    });
    const boundary = Date.parse(SESSION.lastSampleAt!);

    const result = await processBackgroundLocationBatch(
      [
        location(boundary + 3_000, { accuracy: 250 }),
        location(boundary),
        location(boundary + 2_000),
        location(boundary + 1_000),
      ],
      {
        getActiveTripSession: async () => SESSION,
        appendLocationSample,
        recordRejectedSample,
        now: () => boundary + 60_000,
      },
    );

    expect(result).toEqual({
      kind: 'processed',
      acceptedCount: 2,
      rejectedCount: 1,
      ignoredReplayCount: 1,
    });
    expect(appendedTimestamps).toEqual([boundary + 1_000, boundary + 2_000]);
    expect(rejectedReasons).toEqual(['poor_accuracy']);
    expect(JSON.stringify(result)).not.toContain('37.5665');
    expect(JSON.stringify(result)).not.toContain('126.978');
  });

  it('fails closed when persisted session time is malformed', async () => {
    const appendLocationSample = vi.fn();
    const recordRejectedSample = vi.fn();

    await expect(
      processBackgroundLocationBatch([location(Date.now())], {
        getActiveTripSession: async () => ({ ...SESSION, lastSampleAt: 'not-a-time' }),
        appendLocationSample,
        recordRejectedSample,
        now: () => Date.now(),
      }),
    ).rejects.toThrow('BACKGROUND_SESSION_TIMESTAMP_INVALID');
    expect(appendLocationSample).not.toHaveBeenCalled();
    expect(recordRejectedSample).not.toHaveBeenCalled();
  });

  it('fails closed when the runtime clock is malformed', async () => {
    const appendLocationSample = vi.fn();
    const recordRejectedSample = vi.fn();

    await expect(
      processBackgroundLocationBatch([location(Date.parse(SESSION.startedAt) + 10_000)], {
        getActiveTripSession: async () => SESSION,
        appendLocationSample,
        recordRejectedSample,
        now: () => Number.NaN,
      }),
    ).rejects.toThrow('BACKGROUND_CLOCK_INVALID');
    expect(appendLocationSample).not.toHaveBeenCalled();
    expect(recordRejectedSample).not.toHaveBeenCalled();
  });

  it('does not retain a cached fix from before an explicit trip start', async () => {
    const appendLocationSample = vi.fn();
    const recordRejectedSample = vi.fn();
    const startedAt = Date.parse(SESSION.startedAt);

    await expect(
      processBackgroundLocationBatch([location(startedAt - 1)], {
        getActiveTripSession: async () => ({ ...SESSION, lastSampleAt: null }),
        appendLocationSample,
        recordRejectedSample,
        now: () => startedAt + 60_000,
      }),
    ).resolves.toEqual({
      kind: 'processed',
      acceptedCount: 0,
      rejectedCount: 0,
      ignoredReplayCount: 1,
    });
    expect(appendLocationSample).not.toHaveBeenCalled();
    expect(recordRejectedSample).not.toHaveBeenCalled();
  });
});
