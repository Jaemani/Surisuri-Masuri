import * as Location from 'expo-location';
import * as TaskManager from 'expo-task-manager';

export const BACKGROUND_LOCATION_TASK_NAME =
  'mobility-reliability.background-location.v1';

const BACKGROUND_LOCATION_OPTIONS: Location.LocationTaskOptions = {
  accuracy: Location.Accuracy.High,
  timeInterval: 5_000,
  distanceInterval: 5,
  deferredUpdatesDistance: 25,
  deferredUpdatesInterval: 30_000,
  activityType: Location.ActivityType.OtherNavigation,
  pausesUpdatesAutomatically: false,
  showsBackgroundLocationIndicator: true,
  foregroundService: {
    notificationTitle: '이동 기록 중',
    notificationBody: '전동보장구 주행 데이터를 기기에 저장하고 있습니다.',
    notificationColor: '#1B6956',
    killServiceOnDestroy: false,
  },
};

async function assertBackgroundTaskCanStart(): Promise<void> {
  let available: boolean;
  try {
    available = await TaskManager.isAvailableAsync();
  } catch {
    throw new Error('BACKGROUND_TASK_UNAVAILABLE');
  }
  if (!available) {
    throw new Error('BACKGROUND_TASK_UNAVAILABLE');
  }
  if (!TaskManager.isTaskDefined(BACKGROUND_LOCATION_TASK_NAME)) {
    throw new Error('BACKGROUND_TASK_UNDEFINED');
  }
}

export async function isBackgroundLocationAvailable(): Promise<boolean> {
  return TaskManager.isAvailableAsync();
}

export async function isBackgroundLocationRunning(): Promise<boolean> {
  if (!(await isBackgroundLocationAvailable())) return false;
  return Location.hasStartedLocationUpdatesAsync(BACKGROUND_LOCATION_TASK_NAME);
}

export async function startBackgroundLocation(): Promise<void> {
  await assertBackgroundTaskCanStart();
  if (await isBackgroundLocationRunning()) return;
  await Location.startLocationUpdatesAsync(
    BACKGROUND_LOCATION_TASK_NAME,
    BACKGROUND_LOCATION_OPTIONS,
  );
}

export async function restartBackgroundLocation(): Promise<void> {
  await stopBackgroundLocation();
  await assertBackgroundTaskCanStart();
  await Location.startLocationUpdatesAsync(
    BACKGROUND_LOCATION_TASK_NAME,
    BACKGROUND_LOCATION_OPTIONS,
  );
}

export async function stopBackgroundLocation(): Promise<void> {
  if (!(await TaskManager.isAvailableAsync())) return;
  const running = await Location.hasStartedLocationUpdatesAsync(
    BACKGROUND_LOCATION_TASK_NAME,
  );
  if (!running) return;
  await Location.stopLocationUpdatesAsync(BACKGROUND_LOCATION_TASK_NAME);
}
