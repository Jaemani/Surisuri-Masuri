import { useCallback, useEffect, useRef, useState } from 'react';
import * as Location from 'expo-location';

import {
  appendLocationSample,
  getActiveTripSession,
  getPendingOutboxCount,
  getTelemetryDatabase,
  recordRejectedSample,
  startTripSession,
  stopTripSession,
  type TripSessionSummary,
} from './database';
import { CaptureGuard } from './captureGuard';
import {
  toPermissionState,
  type LocationPermissionState,
} from './permissionState';
import {
  evaluateLocationSample,
  MAX_FUTURE_CLOCK_SKEW_MILLISECONDS,
} from './samplePolicy';

type RecorderPhase =
  | 'initializing'
  | 'idle'
  | 'ready_to_resume'
  | 'recording'
  | 'busy'
  | 'error';

export type TripRecorderState = {
  phase: RecorderPhase;
  permission: LocationPermissionState;
  activeSession: TripSessionSummary | null;
  pendingOutboxCount: number;
  errorCode: 'database_unavailable' | 'location_services_disabled' | 'capture_failed' | null;
};

const initialState: TripRecorderState = {
  phase: 'initializing',
  permission: 'checking',
  activeSession: null,
  pendingOutboxCount: 0,
  errorCode: null,
};

const FOREGROUND_LOCATION_OPTIONS: Location.LocationOptions = {
  accuracy: Location.Accuracy.High,
  timeInterval: 5_000,
  distanceInterval: 5,
};

export function useTripRecorder() {
  const [state, setState] = useState<TripRecorderState>(initialState);
  const subscriptionRef = useRef<Location.LocationSubscription | null>(null);
  const writeQueueRef = useRef<Promise<void>>(Promise.resolve());
  const captureGuardRef = useRef(new CaptureGuard());
  const lastAcceptedTimestampRef = useRef<number | null>(null);

  const refreshPendingCount = useCallback(async () => {
    const pendingOutboxCount = await getPendingOutboxCount();
    setState((current) => ({ ...current, pendingOutboxCount }));
  }, []);

  useEffect(() => {
    let cancelled = false;

    async function initialize() {
      try {
        await getTelemetryDatabase();
        const [permissionResponse, activeSession, pendingOutboxCount] = await Promise.all([
          Location.getForegroundPermissionsAsync(),
          getActiveTripSession(),
          getPendingOutboxCount(),
        ]);
        if (cancelled) return;

        setState({
          phase: activeSession ? 'ready_to_resume' : 'idle',
          permission: toPermissionState(permissionResponse),
          activeSession,
          pendingOutboxCount,
          errorCode: null,
        });
      } catch {
        if (!cancelled) {
          setState((current) => ({
            ...current,
            phase: 'error',
            errorCode: 'database_unavailable',
          }));
        }
      }
    }

    void initialize();
    return () => {
      cancelled = true;
      captureGuardRef.current.closeCapture();
      subscriptionRef.current?.remove();
      subscriptionRef.current = null;
    };
  }, []);

  const requestPermission = useCallback(async (): Promise<boolean> => {
    try {
      const current = await Location.getForegroundPermissionsAsync();
      let normalized = toPermissionState(current);

      if (normalized === 'undetermined' || normalized === 'denied_can_ask') {
        normalized = toPermissionState(await Location.requestForegroundPermissionsAsync());
      }

      setState((stateBeforeRequest) => ({
        ...stateBeforeRequest,
        permission: normalized,
      }));
      return normalized === 'granted';
    } catch {
      setState((current) => ({ ...current, errorCode: 'capture_failed' }));
      return false;
    }
  }, []);

  const beginWatching = useCallback(
    async (session: TripSessionSummary) => {
      if (subscriptionRef.current) {
        throw new Error('CAPTURE_ALREADY_RUNNING');
      }
      if (!(await Location.hasServicesEnabledAsync())) {
        setState((current) => ({
          ...current,
          phase: current.activeSession ? 'ready_to_resume' : 'idle',
          errorCode: 'location_services_disabled',
        }));
        throw new Error('LOCATION_SERVICES_DISABLED');
      }

      const captureGeneration = captureGuardRef.current.openCapture();
      lastAcceptedTimestampRef.current = session.lastSampleAt
        ? Date.parse(session.lastSampleAt)
        : Date.parse(session.startedAt) - 30_000;
      let runtimeFailed = false;
      let subscription: Location.LocationSubscription;
      try {
        subscription = await Location.watchPositionAsync(
          FOREGROUND_LOCATION_OPTIONS,
          (location) => {
            if (!captureGuardRef.current.acceptsCallback(captureGeneration)) return;
            const evaluation = evaluateLocationSample({
              latitude: location.coords.latitude,
              longitude: location.coords.longitude,
              timestamp: location.timestamp,
              accuracy: location.coords.accuracy,
              altitude: location.coords.altitude,
              speed: location.coords.speed,
              heading: location.coords.heading,
              isMockLocation: location.mocked ?? null,
            }, {
              minimumTimestamp: lastAcceptedTimestampRef.current ?? undefined,
              maximumTimestamp: Date.now() + MAX_FUTURE_CLOCK_SKEW_MILLISECONDS,
            });

            if (evaluation.accepted) {
              lastAcceptedTimestampRef.current = evaluation.sample.timestamp;
            }

            writeQueueRef.current = writeQueueRef.current
              .then(async () => {
                if (evaluation.accepted) {
                  const activeSession = await appendLocationSample(
                    session.sessionId,
                    evaluation.sample,
                  );
                  const pendingOutboxCount = await getPendingOutboxCount();
                  setState((current) => ({ ...current, activeSession, pendingOutboxCount }));
                } else {
                  const activeSession = await recordRejectedSample(
                    session.sessionId,
                    evaluation.reason,
                  );
                  const pendingOutboxCount = await getPendingOutboxCount();
                  setState((current) => ({ ...current, activeSession, pendingOutboxCount }));
                }
              })
              .catch(() => {
                captureGuardRef.current.closeCapture();
                subscriptionRef.current?.remove();
                subscriptionRef.current = null;
                setState((current) => ({
                  ...current,
                  phase: 'error',
                  errorCode: 'capture_failed',
                }));
              });
          },
          () => {
            if (!captureGuardRef.current.acceptsCallback(captureGeneration)) return;
            runtimeFailed = true;
            captureGuardRef.current.closeCapture();
            subscriptionRef.current?.remove();
            subscriptionRef.current = null;
            setState((current) => ({
              ...current,
              phase: current.activeSession ? 'ready_to_resume' : 'error',
              errorCode: 'capture_failed',
            }));
          },
        );
      } catch (error) {
        captureGuardRef.current.closeCapture();
        throw error;
      }

      if (runtimeFailed) {
        subscription.remove();
        throw new Error('LOCATION_WATCH_RUNTIME_FAILED');
      }

      subscriptionRef.current = subscription;
      setState((current) => ({
        ...current,
        phase: 'recording',
        activeSession: session,
        errorCode: null,
      }));
    },
    [],
  );

  const start = useCallback(async () => {
    if (state.phase === 'busy' || state.phase === 'recording') return;
    if (!captureGuardRef.current.tryBeginOperation()) return;
    setState((current) => ({ ...current, phase: 'busy', errorCode: null }));

    let createdSession: TripSessionSummary | null = null;
    try {
      if (!(await requestPermission())) {
        setState((current) => ({ ...current, phase: 'idle' }));
        return;
      }

      createdSession = await startTripSession();
      setState((current) => ({ ...current, activeSession: createdSession }));
      await beginWatching(createdSession);
      await refreshPendingCount();
    } catch (error) {
      let stoppedFailedSession = false;
      if (createdSession && error instanceof Error && error.message !== 'LOCATION_SERVICES_DISABLED') {
        await stopTripSession(createdSession.sessionId, 'watch_start_failed')
          .then(() => {
            stoppedFailedSession = true;
          })
          .catch(() => undefined);
      }
      setState((current) => ({
        ...current,
        activeSession: stoppedFailedSession ? null : current.activeSession,
        phase: stoppedFailedSession ? 'error' : current.activeSession ? 'ready_to_resume' : 'error',
        errorCode:
          current.errorCode === 'location_services_disabled'
            ? 'location_services_disabled'
            : 'capture_failed',
      }));
    } finally {
      captureGuardRef.current.endOperation();
    }
  }, [beginWatching, refreshPendingCount, requestPermission, state.phase]);

  const resume = useCallback(async () => {
    if (
      !state.activeSession ||
      state.phase === 'busy' ||
      state.phase === 'recording'
    ) {
      return;
    }
    if (!captureGuardRef.current.tryBeginOperation()) return;
    setState((current) => ({ ...current, phase: 'busy', errorCode: null }));

    try {
      if (!(await requestPermission())) {
        setState((current) => ({ ...current, phase: 'ready_to_resume' }));
        return;
      }
      await beginWatching(state.activeSession);
    } catch {
      setState((current) => ({
        ...current,
        phase: 'ready_to_resume',
        errorCode:
          current.errorCode === 'location_services_disabled'
            ? 'location_services_disabled'
            : 'capture_failed',
      }));
    } finally {
      captureGuardRef.current.endOperation();
    }
  }, [beginWatching, requestPermission, state.activeSession, state.phase]);

  const stop = useCallback(async () => {
    if (
      !state.activeSession ||
      state.phase === 'busy'
    ) {
      return;
    }
    if (!captureGuardRef.current.tryBeginOperation()) return;
    setState((current) => ({ ...current, phase: 'busy', errorCode: null }));
    captureGuardRef.current.closeCapture();
    subscriptionRef.current?.remove();
    subscriptionRef.current = null;

    try {
      await writeQueueRef.current;
      await stopTripSession(state.activeSession.sessionId, 'user_stopped');
      const pendingOutboxCount = await getPendingOutboxCount();
      setState((current) => ({
        ...current,
        phase: 'idle',
        activeSession: null,
        pendingOutboxCount,
        errorCode: null,
      }));
    } catch {
      setState((current) => ({ ...current, phase: 'error', errorCode: 'capture_failed' }));
    } finally {
      captureGuardRef.current.endOperation();
    }
  }, [state.activeSession, state.phase]);

  return { state, start, resume, stop };
}
