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

export async function isBackgroundLocationAvailable(): Promise<boolean> {
  try {
    return await TaskManager.isAvailableAsync();
  } catch {
    return false;
  }
}

export async function isBackgroundLocationRunning(): Promise<boolean> {
  if (!(await isBackgroundLocationAvailable())) return false;
  try {
    return await Location.hasStartedLocationUpdatesAsync(
      BACKGROUND_LOCATION_TASK_NAME,
    );
  } catch {
    return false;
  }
}

export async function startBackgroundLocation(): Promise<void> {
  if (!(await isBackgroundLocationAvailable())) {
    throw new Error('BACKGROUND_TASK_UNAVAILABLE');
  }
  if (!TaskManager.isTaskDefined(BACKGROUND_LOCATION_TASK_NAME)) {
    throw new Error('BACKGROUND_TASK_UNDEFINED');
  }
  if (await isBackgroundLocationRunning()) return;
  await Location.startLocationUpdatesAsync(
    BACKGROUND_LOCATION_TASK_NAME,
    BACKGROUND_LOCATION_OPTIONS,
  );
}

export async function stopBackgroundLocation(): Promise<void> {
  if (!(await isBackgroundLocationRunning())) return;
  await Location.stopLocationUpdatesAsync(BACKGROUND_LOCATION_TASK_NAME);
}
