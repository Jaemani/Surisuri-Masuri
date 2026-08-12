import { useCallback, useState } from 'react';

type LocationPermissionState =
  | 'checking'
  | 'granted'
  | 'undetermined'
  | 'denied_can_ask'
  | 'denied_blocked';

type TripSessionSummary = {
  sessionId: string;
  startedAt: string;
  endedAt: string | null;
  state: 'recording' | 'stopped';
  nextEventSequence: number;
  nextSampleSequence: number;
  acceptedSampleCount: number;
  rejectedSampleCount: number;
  uploadEligibility: 'development_local_only' | 'server_bound';
  lastSampleAt: string | null;
};

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

const previewInitialState: TripRecorderState = {
  phase: 'idle',
  permission: 'granted',
  backgroundPermission: 'checking',
  backgroundAvailable: false,
  captureMode: null,
  activeSession: null,
  pendingUploadCount: 0,
  errorCode: null,
};

function createPreviewSession(): TripSessionSummary {
  const now = '2026-08-13T01:40:00+09:00';
  return {
    sessionId: 'web-preview-session',
    startedAt: now,
    endedAt: null,
    state: 'recording',
    nextEventSequence: 1,
    nextSampleSequence: 0,
    acceptedSampleCount: 0,
    rejectedSampleCount: 0,
    uploadEligibility: 'development_local_only',
    lastSampleAt: null,
  };
}

export function useTripRecorder() {
  const [state, setState] = useState<TripRecorderState>(previewInitialState);

  const start = useCallback(async () => {
    setState({
      ...previewInitialState,
      phase: 'recording',
      captureMode: 'foreground',
      activeSession: createPreviewSession(),
    });
  }, []);

  const resume = useCallback(async () => {
    setState((current) => ({
      ...current,
      phase: current.activeSession ? 'recording' : 'idle',
      captureMode: current.activeSession ? 'foreground' : null,
      errorCode: null,
    }));
  }, []);

  const stop = useCallback(async () => {
    setState(previewInitialState);
  }, []);

  const enableBackground = useCallback(async () => undefined, []);
  const refresh = useCallback(async () => undefined, []);

  return { state, start, resume, stop, enableBackground, refresh };
}
