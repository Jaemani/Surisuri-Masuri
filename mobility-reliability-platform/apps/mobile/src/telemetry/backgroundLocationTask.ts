import * as Location from 'expo-location';
import * as TaskManager from 'expo-task-manager';

import {
  appendLocationSample,
  clearBackgroundTaskFailure,
  getActiveTripSession,
  hasBackgroundTaskFailure,
  recordBackgroundTaskFailure,
  recordRejectedSample,
} from './database';
import { processBackgroundLocationBatch } from './backgroundLocationProcessor';
import { BACKGROUND_LOCATION_TASK_NAME } from './backgroundLocationRuntime';
import { createBackgroundLocationTaskHandler } from './backgroundLocationTaskHandler';

type BackgroundLocationTaskData = {
  locations?: Location.LocationObject[];
};

const handleBackgroundLocation = createBackgroundLocationTaskHandler({
  processBatch: (locations) =>
    processBackgroundLocationBatch(locations, {
      getActiveTripSession,
      appendLocationSample,
      recordRejectedSample,
      now: Date.now,
    }),
  recordFailure: recordBackgroundTaskFailure,
  hasFailure: hasBackgroundTaskFailure,
  clearFailure: clearBackgroundTaskFailure,
});

if (!TaskManager.isTaskDefined(BACKGROUND_LOCATION_TASK_NAME)) {
  TaskManager.defineTask<BackgroundLocationTaskData>(
    BACKGROUND_LOCATION_TASK_NAME,
    handleBackgroundLocation,
  );
}
