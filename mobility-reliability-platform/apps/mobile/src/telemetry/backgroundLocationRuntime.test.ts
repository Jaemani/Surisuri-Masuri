import { beforeEach, describe, expect, it, vi } from 'vitest';

const taskManager = vi.hoisted(() => ({
  isAvailableAsync: vi.fn(),
  isTaskDefined: vi.fn(),
}));
const location = vi.hoisted(() => ({
  hasStartedLocationUpdatesAsync: vi.fn(),
  startLocationUpdatesAsync: vi.fn(),
  stopLocationUpdatesAsync: vi.fn(),
}));

vi.mock('expo-task-manager', () => taskManager);
vi.mock('expo-location', () => ({
  ...location,
  Accuracy: { High: 4 },
  ActivityType: { OtherNavigation: 1 },
}));

import {
  BACKGROUND_LOCATION_TASK_NAME,
  isBackgroundLocationAvailable,
  isBackgroundLocationRunning,
  startBackgroundLocation,
  stopBackgroundLocation,
} from './backgroundLocationRuntime';

describe('background location runtime', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    taskManager.isAvailableAsync.mockResolvedValue(true);
    taskManager.isTaskDefined.mockReturnValue(true);
    location.hasStartedLocationUpdatesAsync.mockResolvedValue(false);
    location.startLocationUpdatesAsync.mockResolvedValue(undefined);
    location.stopLocationUpdatesAsync.mockResolvedValue(undefined);
  });

  it('fails closed when native task management is unavailable', async () => {
    taskManager.isAvailableAsync.mockRejectedValue(new Error('native unavailable'));

    await expect(isBackgroundLocationAvailable()).rejects.toThrow('native unavailable');
    await expect(isBackgroundLocationRunning()).rejects.toThrow('native unavailable');
    await expect(startBackgroundLocation()).rejects.toThrow('BACKGROUND_TASK_UNAVAILABLE');
    expect(location.startLocationUpdatesAsync).not.toHaveBeenCalled();
  });

  it('does not treat an unknown running state as a stopped task', async () => {
    location.hasStartedLocationUpdatesAsync.mockRejectedValue(
      new Error('native status unavailable'),
    );

    await expect(isBackgroundLocationRunning()).rejects.toThrow(
      'native status unavailable',
    );
    await expect(stopBackgroundLocation()).rejects.toThrow(
      'native status unavailable',
    );
    expect(location.stopLocationUpdatesAsync).not.toHaveBeenCalled();
  });

  it('refuses to start before the global task is defined', async () => {
    taskManager.isTaskDefined.mockReturnValue(false);

    await expect(startBackgroundLocation()).rejects.toThrow('BACKGROUND_TASK_UNDEFINED');
    expect(location.startLocationUpdatesAsync).not.toHaveBeenCalled();
  });

  it('starts one explicit foreground service and remains idempotent', async () => {
    await startBackgroundLocation();

    expect(location.startLocationUpdatesAsync).toHaveBeenCalledTimes(1);
    expect(location.startLocationUpdatesAsync).toHaveBeenCalledWith(
      BACKGROUND_LOCATION_TASK_NAME,
      expect.objectContaining({
        timeInterval: 5_000,
        distanceInterval: 5,
        showsBackgroundLocationIndicator: true,
        foregroundService: expect.objectContaining({ killServiceOnDestroy: false }),
      }),
    );

    location.hasStartedLocationUpdatesAsync.mockResolvedValue(true);
    await startBackgroundLocation();
    expect(location.startLocationUpdatesAsync).toHaveBeenCalledTimes(1);
  });

  it('stops only a running task', async () => {
    await stopBackgroundLocation();
    expect(location.stopLocationUpdatesAsync).not.toHaveBeenCalled();

    location.hasStartedLocationUpdatesAsync.mockResolvedValue(true);
    await stopBackgroundLocation();
    expect(location.stopLocationUpdatesAsync).toHaveBeenCalledWith(
      BACKGROUND_LOCATION_TASK_NAME,
    );
  });
});
