import { useCallback, useEffect, useRef, useState } from 'react';
import * as Location from 'expo-location';
import { AppState } from 'react-native';

import {
  isBackgroundLocationAvailable,
  isBackgroundLocationRunning,
  restartBackgroundLocation,
  startBackgroundLocation,
  stopBackgroundLocation,
} from './backgroundLocationRuntime';
import { CaptureGuard } from './captureGuard';
import {
  appendLocationSample,
  clearBackgroundTaskFailure,
  getActiveTripSession,
  getPendingUploadCount,
  getTelemetryDatabase,
  hasBackgroundTaskFailure,
  recordRejectedSample,
  startTripSession,
  stopTripSession,
  type TripSessionSummary,
} from './database';
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

export type CaptureMode = 'foreground' | 'background';

export type TripRecorderState = {
  phase: RecorderPhase;
  permission: LocationPermissionState;
  backgroundPermission: LocationPermissionState;
  backgroundAvailable: boolean;
  captureMode: CaptureMode | null;
  activeSession: TripSessionSummary | null;
  pendingUploadCount: number;
  errorCode: 'database_unavailable' | 'location_services_disabled' | 'capture_failed' | null;
};

const initialState: TripRecorderState = {
  phase: 'initializing',
  permission: 'checking',
  backgroundPermission: 'checking',
  backgroundAvailable: false,
  captureMode: null,
  activeSession: null,
  pendingUploadCount: 0,
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

  const refreshRuntimeState = useCallback(async (recoverColdLaunch = false) => {
    const [
      permissionResponse,
      activeSession,
      pendingUploadCount,
      backgroundAvailable,
      backgroundTaskFailed,
    ] =
      await Promise.all([
        Location.getForegroundPermissionsAsync(),
        getActiveTripSession(),
        getPendingUploadCount(),
        isBackgroundLocationAvailable(),
        hasBackgroundTaskFailure(),
      ]);
    const [backgroundPermissionResponse, detectedBackgroundRunning] = backgroundAvailable
      ? await Promise.all([
          Location.getBackgroundPermissionsAsync(),
          isBackgroundLocationRunning(),
        ])
      : [null, false] as const;
    const normalizedBackgroundPermission = backgroundAvailable
      ? toPermissionState(backgroundPermissionResponse)
      : 'checking';
    let backgroundRunning = detectedBackgroundRunning;
    let backgroundRecoveryFailed = false;

    if (
      recoverColdLaunch &&
      backgroundRunning &&
      activeSession &&
      normalizedBackgroundPermission === 'granted' &&
      !backgroundTaskFailed
    ) {
      try {
        await restartBackgroundLocation();
        backgroundRunning = true;
      } catch {
        backgroundRunning = false;
        backgroundRecoveryFailed = true;
      }
    }

    if (
      backgroundRunning &&
      (!activeSession ||
        normalizedBackgroundPermission !== 'granted' ||
        backgroundTaskFailed)
    ) {
      await stopBackgroundLocation();
      backgroundRunning = false;
    }

    setState((current) => {
      const foregroundRunning = subscriptionRef.current !== null;
      const captureMode = activeSession
        ? backgroundRunning
          ? 'background'
          : foregroundRunning
            ? 'foreground'
            : null
        : null;
      return {
        ...current,
        phase: activeSession
          ? captureMode
            ? 'recording'
            : 'ready_to_resume'
          : 'idle',
        permission: toPermissionState(permissionResponse),
        backgroundPermission: normalizedBackgroundPermission,
        backgroundAvailable,
        captureMode,
        activeSession,
        pendingUploadCount,
        errorCode:
          backgroundTaskFailed || backgroundRecoveryFailed ? 'capture_failed' : null,
      };
    });
  }, []);

  const refreshPendingCount = useCallback(async () => {
    const pendingUploadCount = await getPendingUploadCount();
    setState((current) => ({ ...current, pendingUploadCount }));
  }, []);

  useEffect(() => {
    let cancelled = false;

    async function initialize() {
      try {
        await getTelemetryDatabase();
        if (cancelled) return;
        await refreshRuntimeState(true);
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
    const appStateSubscription = AppState.addEventListener('change', (nextState) => {
      if (nextState !== 'active') return;
      void refreshRuntimeState().catch(() => {
        setState((current) => ({ ...current, errorCode: 'capture_failed' }));
      });
    });

    return () => {
      cancelled = true;
      appStateSubscription.remove();
      captureGuardRef.current.closeCapture();
      subscriptionRef.current?.remove();
      subscriptionRef.current = null;
    };
  }, [refreshRuntimeState]);

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

  const beginForegroundCapture = useCallback(
    async (session: TripSessionSummary) => {
      if (subscriptionRef.current) {
        throw new Error('CAPTURE_ALREADY_RUNNING');
      }

      const captureGeneration = captureGuardRef.current.openCapture();
      lastAcceptedTimestampRef.current = session.lastSampleAt
        ? Date.parse(session.lastSampleAt)
        : Date.parse(session.startedAt);
      let runtimeFailed = false;
      let subscription: Location.LocationSubscription;
      try {
        subscription = await Location.watchPositionAsync(
          FOREGROUND_LOCATION_OPTIONS,
          (location) => {
            if (!captureGuardRef.current.acceptsCallback(captureGeneration)) return;
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
                minimumTimestamp:
                  lastAcceptedTimestampRef.current === null
                    ? undefined
                    : lastAcceptedTimestampRef.current + 1,
                maximumTimestamp: Date.now() + MAX_FUTURE_CLOCK_SKEW_MILLISECONDS,
              },
            );

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
                  const pendingUploadCount = await getPendingUploadCount();
                  setState((current) => ({ ...current, activeSession, pendingUploadCount }));
                } else {
                  const activeSession = await recordRejectedSample(
                    session.sessionId,
                    evaluation.reason,
                  );
                  const pendingUploadCount = await getPendingUploadCount();
                  setState((current) => ({ ...current, activeSession, pendingUploadCount }));
                }
              })
              .catch(() => {
                captureGuardRef.current.closeCapture();
                subscriptionRef.current?.remove();
                subscriptionRef.current = null;
                setState((current) => ({
                  ...current,
                  phase: 'error',
                  captureMode: null,
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
              captureMode: null,
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
        captureMode: 'foreground',
        activeSession: session,
        errorCode: null,
      }));
    },
    [],
  );

  const beginCapture = useCallback(
    async (session: TripSessionSummary) => {
      if (!(await Location.hasServicesEnabledAsync())) {
        setState((current) => ({
          ...current,
          phase: current.activeSession ? 'ready_to_resume' : 'idle',
          captureMode: null,
          errorCode: 'location_services_disabled',
        }));
        throw new Error('LOCATION_SERVICES_DISABLED');
      }

      const backgroundAvailable = await isBackgroundLocationAvailable();
      const backgroundPermission = backgroundAvailable
        ? toPermissionState(await Location.getBackgroundPermissionsAsync())
        : 'checking';
      setState((current) => ({
        ...current,
        backgroundAvailable,
        backgroundPermission,
      }));

      if (backgroundAvailable && backgroundPermission === 'granted') {
        await clearBackgroundTaskFailure();
        await startBackgroundLocation();
        setState((current) => ({
          ...current,
          phase: 'recording',
          captureMode: 'background',
          activeSession: session,
          errorCode: null,
        }));
        return;
      }

      await beginForegroundCapture(session);
    },
    [beginForegroundCapture],
  );

  const enableBackground = useCallback(async () => {
    if (state.phase === 'busy') return;
    if (!captureGuardRef.current.tryBeginOperation()) return;
    setState((current) => ({ ...current, phase: 'busy', errorCode: null }));

    try {
      if (!(await requestPermission())) {
        await refreshRuntimeState();
        return;
      }
      if (!(await isBackgroundLocationAvailable())) {
        await refreshRuntimeState();
        return;
      }

      const current = await Location.getBackgroundPermissionsAsync();
      const response = current.granted
        ? current
        : await Location.requestBackgroundPermissionsAsync();
      const backgroundPermission = toPermissionState(response);
      setState((beforeRequest) => ({
        ...beforeRequest,
        backgroundAvailable: true,
        backgroundPermission,
      }));

      if (backgroundPermission !== 'granted' || !state.activeSession) {
        await refreshRuntimeState();
        return;
      }

      captureGuardRef.current.closeCapture();
      subscriptionRef.current?.remove();
      subscriptionRef.current = null;
      await writeQueueRef.current;
      await clearBackgroundTaskFailure();
      await startBackgroundLocation();
      await refreshRuntimeState();
    } catch {
      setState((currentState) => ({
        ...currentState,
        phase: currentState.activeSession ? 'ready_to_resume' : 'idle',
        captureMode: null,
        errorCode: 'capture_failed',
      }));
    } finally {
      captureGuardRef.current.endOperation();
    }
  }, [refreshRuntimeState, requestPermission, state.activeSession, state.phase]);

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
      await beginCapture(createdSession);
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
        captureMode: null,
        errorCode:
          current.errorCode === 'location_services_disabled'
            ? 'location_services_disabled'
            : 'capture_failed',
      }));
    } finally {
      captureGuardRef.current.endOperation();
    }
  }, [beginCapture, refreshPendingCount, requestPermission, state.phase]);

  const resume = useCallback(async () => {
    if (!state.activeSession || state.phase === 'busy' || state.phase === 'recording') {
      return;
    }
    if (!captureGuardRef.current.tryBeginOperation()) return;
    setState((current) => ({ ...current, phase: 'busy', errorCode: null }));

    try {
      if (!(await requestPermission())) {
        setState((current) => ({ ...current, phase: 'ready_to_resume' }));
        return;
      }
      await beginCapture(state.activeSession);
    } catch {
      setState((current) => ({
        ...current,
        phase: 'ready_to_resume',
        captureMode: null,
        errorCode:
          current.errorCode === 'location_services_disabled'
            ? 'location_services_disabled'
            : 'capture_failed',
      }));
    } finally {
      captureGuardRef.current.endOperation();
    }
  }, [beginCapture, requestPermission, state.activeSession, state.phase]);

  const stop = useCallback(async () => {
    if (!state.activeSession || state.phase === 'busy') return;
    if (!captureGuardRef.current.tryBeginOperation()) return;
    setState((current) => ({ ...current, phase: 'busy', errorCode: null }));
    captureGuardRef.current.closeCapture();
    subscriptionRef.current?.remove();
    subscriptionRef.current = null;

    try {
      await stopBackgroundLocation();
      await writeQueueRef.current;
      await stopTripSession(state.activeSession.sessionId, 'user_stopped');
      const pendingUploadCount = await getPendingUploadCount();
      setState((current) => ({
        ...current,
        phase: 'idle',
        captureMode: null,
        activeSession: null,
        pendingUploadCount,
        errorCode: null,
      }));
    } catch {
      setState((current) => ({
        ...current,
        phase: 'error',
        captureMode: null,
        errorCode: 'capture_failed',
      }));
    } finally {
      captureGuardRef.current.endOperation();
    }
  }, [state.activeSession, state.phase]);

  return { state, start, resume, stop, enableBackground, refresh: refreshRuntimeState };
}
