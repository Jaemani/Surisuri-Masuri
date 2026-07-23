import { describe, expect, it, vi } from 'vitest';

import type { BackgroundLocationInput } from './backgroundLocationProcessor';
import {
  createBackgroundLocationTaskHandler,
  MAX_BACKGROUND_LOCATION_BATCH_SIZE,
} from './backgroundLocationTaskHandler';

const sample: BackgroundLocationInput = {
  timestamp: 1_753_228_800_000,
  mocked: false,
  coords: {
    latitude: 37.5665,
    longitude: 126.978,
    accuracy: 8,
    altitude: null,
    speed: 1,
    heading: 90,
  },
};

function dependencies() {
  return {
    processBatch: vi.fn(async () => ({
      kind: 'processed' as const,
      acceptedCount: 1,
      rejectedCount: 0,
      ignoredReplayCount: 0,
    })),
    recordFailure: vi.fn(async () => undefined),
    clearFailure: vi.fn(async () => undefined),
  };
}

describe('background location task handler', () => {
  it('records malformed native deliveries without inspecting their values', async () => {
    const deps = dependencies();
    const handler = createBackgroundLocationTaskHandler(deps);

    await expect(handler({ data: {} })).rejects.toThrow(
      'BACKGROUND_TASK_PAYLOAD_INVALID',
    );
    expect(deps.recordFailure).toHaveBeenCalledWith('payload_invalid');
    expect(deps.processBatch).not.toHaveBeenCalled();
  });

  it('rejects an unbounded native batch before persistence', async () => {
    const deps = dependencies();
    const handler = createBackgroundLocationTaskHandler(deps);

    await expect(
      handler({
        data: {
          locations: Array.from(
            { length: MAX_BACKGROUND_LOCATION_BATCH_SIZE + 1 },
            () => sample,
          ),
        },
      }),
    ).rejects.toThrow('BACKGROUND_TASK_BATCH_TOO_LARGE');
    expect(deps.recordFailure).toHaveBeenCalledWith('batch_too_large');
    expect(deps.processBatch).not.toHaveBeenCalled();
  });

  it('serializes overlapping native deliveries', async () => {
    let releaseFirst!: () => void;
    const firstBlocked = new Promise<void>((resolve) => {
      releaseFirst = resolve;
    });
    const deps = dependencies();
    deps.processBatch
      .mockImplementationOnce(async () => {
        await firstBlocked;
        return {
          kind: 'processed',
          acceptedCount: 1,
          rejectedCount: 0,
          ignoredReplayCount: 0,
        };
      })
      .mockResolvedValueOnce({
        kind: 'processed',
        acceptedCount: 1,
        rejectedCount: 0,
        ignoredReplayCount: 0,
      });
    const handler = createBackgroundLocationTaskHandler(deps);

    const first = handler({ data: { locations: [sample] } });
    const second = handler({ data: { locations: [{ ...sample, timestamp: sample.timestamp + 1 }] } });
    await vi.waitFor(() => expect(deps.processBatch).toHaveBeenCalledTimes(1));
    releaseFirst();
    await Promise.all([first, second]);

    expect(deps.processBatch).toHaveBeenCalledTimes(2);
    expect(deps.processBatch.mock.invocationCallOrder[0]).toBeLessThan(
      deps.processBatch.mock.invocationCallOrder[1],
    );
  });

  it('replaces a processing failure with a coordinate-free durable error', async () => {
    const deps = dependencies();
    deps.processBatch.mockRejectedValueOnce(
      new Error(`failed near ${sample.coords.latitude},${sample.coords.longitude}`),
    );
    const handler = createBackgroundLocationTaskHandler(deps);

    const error = await handler({ data: { locations: [sample] } }).catch(
      (caught: unknown) => caught,
    );
    expect(error).toBeInstanceOf(Error);
    expect((error as Error).message).toBe('BACKGROUND_TASK_PROCESSING_FAILED');
    expect((error as Error).message).not.toContain(String(sample.coords.latitude));
    expect(deps.recordFailure).toHaveBeenCalledWith('batch_processing_failed');
  });

  it('clears a prior failure only after a successful batch', async () => {
    const deps = dependencies();
    const handler = createBackgroundLocationTaskHandler(deps);

    await handler({ data: { locations: [sample] } });
    expect(deps.clearFailure).toHaveBeenCalledTimes(1);
    expect(deps.recordFailure).not.toHaveBeenCalled();
  });
});
