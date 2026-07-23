import type { BackgroundTaskFailureCode } from './database';
import type {
  BackgroundLocationBatchResult,
  BackgroundLocationInput,
} from './backgroundLocationProcessor';

export const MAX_BACKGROUND_LOCATION_BATCH_SIZE = 100;

export type BackgroundLocationTaskBody = {
  data?: { locations?: readonly BackgroundLocationInput[] };
  error?: unknown;
};

type BackgroundLocationTaskDependencies = {
  processBatch: (
    locations: readonly BackgroundLocationInput[],
  ) => Promise<BackgroundLocationBatchResult>;
  recordFailure: (code: BackgroundTaskFailureCode) => Promise<void>;
  hasFailure: () => Promise<boolean>;
  clearFailure: () => Promise<void>;
};

/**
 * Serializes native task deliveries and converts every failure into a bounded,
 * coordinate-free durable status before returning a safe task error.
 */
export function createBackgroundLocationTaskHandler(
  dependencies: BackgroundLocationTaskDependencies,
) {
  let processingQueue: Promise<void> = Promise.resolve();

  function enqueue(operation: () => Promise<void>): Promise<void> {
    const scheduled = processingQueue.then(operation);
    processingQueue = scheduled.catch(() => undefined);
    return scheduled;
  }

  async function fail(
    code: BackgroundTaskFailureCode,
    publicErrorCode: string,
  ): Promise<never> {
    try {
      await dependencies.recordFailure(code);
    } catch {
      throw new Error('BACKGROUND_TASK_FAILURE_UNRECORDED');
    }
    throw new Error(publicErrorCode);
  }

  return async function handleBackgroundLocationTask(
    body: BackgroundLocationTaskBody,
  ): Promise<void> {
    return enqueue(async () => {
      let held: boolean;
      try {
        held = await dependencies.hasFailure();
      } catch {
        throw new Error('BACKGROUND_TASK_FAILURE_STATUS_UNAVAILABLE');
      }
      if (held) {
        throw new Error('BACKGROUND_TASK_HELD');
      }

      if (body.error) {
        await fail('native_task_error', 'BACKGROUND_NATIVE_TASK_FAILED');
      }

      const locations = body.data?.locations;
      if (!Array.isArray(locations)) {
        return fail('payload_invalid', 'BACKGROUND_TASK_PAYLOAD_INVALID');
      }
      if (locations.length > MAX_BACKGROUND_LOCATION_BATCH_SIZE) {
        return fail('batch_too_large', 'BACKGROUND_TASK_BATCH_TOO_LARGE');
      }

      try {
        await dependencies.processBatch(locations);
        await dependencies.clearFailure();
      } catch {
        await fail('batch_processing_failed', 'BACKGROUND_TASK_PROCESSING_FAILED');
      }
    });
  };
}
