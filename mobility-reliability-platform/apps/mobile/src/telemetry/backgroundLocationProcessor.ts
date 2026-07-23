import type { TripSessionSummary } from './database';
import {
  evaluateLocationSample,
  MAX_FUTURE_CLOCK_SKEW_MILLISECONDS,
  type NormalizedLocationSample,
  type SampleRejectionReason,
} from './samplePolicy';

export type BackgroundLocationInput = {
  coords: {
    latitude: number;
    longitude: number;
    accuracy: number | null;
    altitude: number | null;
    speed: number | null;
    heading: number | null;
  };
  timestamp: number;
  mocked?: boolean;
};

export type BackgroundLocationBatchResult =
  | { kind: 'no_active_session' }
  | {
      kind: 'processed';
      acceptedCount: number;
      rejectedCount: number;
      ignoredReplayCount: number;
    };

type BackgroundLocationProcessorDependencies = {
  getActiveTripSession: () => Promise<TripSessionSummary | null>;
  appendLocationSample: (
    sessionId: string,
    sample: NormalizedLocationSample,
  ) => Promise<TripSessionSummary>;
  recordRejectedSample: (
    sessionId: string,
    reason: SampleRejectionReason,
  ) => Promise<TripSessionSummary>;
  now: () => number;
};

/**
 * Converts a native background callback into the same append-only SQLite
 * events used by foreground capture. The result deliberately contains counts
 * only, so task failures cannot leak coordinates through logs or telemetry.
 */
export async function processBackgroundLocationBatch(
  locations: readonly BackgroundLocationInput[],
  dependencies: BackgroundLocationProcessorDependencies,
): Promise<BackgroundLocationBatchResult> {
  const session = await dependencies.getActiveTripSession();
  if (!session) return { kind: 'no_active_session' };

  const sessionBoundary = Date.parse(session.lastSampleAt ?? session.startedAt);
  if (!Number.isFinite(sessionBoundary)) {
    throw new Error('BACKGROUND_SESSION_TIMESTAMP_INVALID');
  }

  const ordered = locations
    .map((location, index) => ({ location, index }))
    .sort((left, right) => {
      const byTimestamp = left.location.timestamp - right.location.timestamp;
      return Number.isFinite(byTimestamp) && byTimestamp !== 0
        ? byTimestamp
        : left.index - right.index;
    });
  const observedNow = dependencies.now();
  if (!Number.isFinite(observedNow) || observedNow <= 0) {
    throw new Error('BACKGROUND_CLOCK_INVALID');
  }
  const maximumTimestamp = observedNow + MAX_FUTURE_CLOCK_SKEW_MILLISECONDS;
  let lastAcceptedTimestamp = sessionBoundary;
  let acceptedCount = 0;
  let rejectedCount = 0;
  let ignoredReplayCount = 0;

  for (const { location } of ordered) {
    if (
      Number.isFinite(location.timestamp) &&
      location.timestamp <= lastAcceptedTimestamp
    ) {
      ignoredReplayCount += 1;
      continue;
    }

    const evaluation = evaluateLocationSample(
      {
        latitude: location.coords.latitude,
        longitude: location.coords.longitude,
        timestamp: location.timestamp,
        accuracy: location.coords.accuracy,
        altitude: location.coords.altitude,
        speed: location.coords.speed,
        heading: location.coords.heading,
        isMockLocation: location.mocked ?? null,
      },
      {
        minimumTimestamp: lastAcceptedTimestamp + 1,
        maximumTimestamp,
      },
    );

    if (evaluation.accepted) {
      await dependencies.appendLocationSample(session.sessionId, evaluation.sample);
      lastAcceptedTimestamp = evaluation.sample.timestamp;
      acceptedCount += 1;
    } else {
      await dependencies.recordRejectedSample(session.sessionId, evaluation.reason);
      rejectedCount += 1;
    }
  }

  return {
    kind: 'processed',
    acceptedCount,
    rejectedCount,
    ignoredReplayCount,
  };
}
