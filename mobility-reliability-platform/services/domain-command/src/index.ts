import { onRequest } from 'firebase-functions/v2/https';
import { appendSubsidyTransactionHandler, createRepairRequestHandler, transitionRepairRequestHandler } from './http.js';

export { DomainCommandKernel, commandBodyHash } from './kernel.js';
export { InMemoryCommandStore } from './store.js';
export { FirestoreDomainCommandStore } from './firebase-store.js';
export { resolveActorContext, createRepairRequestHandler, transitionRepairRequestHandler, appendSubsidyTransactionHandler } from './http.js';
export * from './types.js';

export const createRepairRequest = onRequest({ cors: false }, createRepairRequestHandler as never);
export const transitionRepairRequest = onRequest({ cors: false }, transitionRepairRequestHandler as never);
export const appendSubsidyTransaction = onRequest({ cors: false }, appendSubsidyTransactionHandler as never);
