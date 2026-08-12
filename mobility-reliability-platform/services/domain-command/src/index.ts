import { onRequest } from 'firebase-functions/v2/https';
import { appendSubsidyTransactionHandler, createRepairRequestHandler, transitionRepairRequestHandler } from './http.js';
import { getConsoleOperationsSnapshotHandler, getMobileProductSnapshotHandler } from './projection-http.js';

export { DomainCommandKernel, commandBodyHash } from './kernel.js';
export { InMemoryCommandStore } from './store.js';
export { FirestoreDomainCommandStore } from './firebase-store.js';
export { resolveActorContext, createRepairRequestHandler, transitionRepairRequestHandler, appendSubsidyTransactionHandler } from './http.js';
export { FirestoreProductProjectionStore } from './projection-store.js';
export * from './device-timeline-projector.js';
export * from './device-state-projector.js';
export * from './device-state-projection-store.js';
export { getMobileProductSnapshotHandler, getConsoleOperationsSnapshotHandler } from './projection-http.js';
export * from './projection-types.js';
export * from './types.js';

export const createRepairRequest = onRequest({ cors: false }, createRepairRequestHandler as never);
export const transitionRepairRequest = onRequest({ cors: false }, transitionRepairRequestHandler as never);
export const appendSubsidyTransaction = onRequest({ cors: false }, appendSubsidyTransactionHandler as never);
export const getMobileProductSnapshot = onRequest({ cors: false }, getMobileProductSnapshotHandler as never);
export const getConsoleOperationsSnapshot = onRequest({ cors: false }, getConsoleOperationsSnapshotHandler as never);
