import { getApp, getApps, initializeApp } from 'firebase-admin/app';
import { getFirestore } from 'firebase-admin/firestore';
import { DomainCommandError, safeId } from './canonical.js';
import { resolveActorContext, type HttpRequestLike, type HttpResponseLike } from './http.js';
import { FirestoreProductProjectionStore } from './projection-store.js';
import type { ConsoleProjectionName, ProductProjectionStore } from './projection-types.js';
import type { ActorContext } from './types.js';

export interface ProjectionHttpRequestLike extends HttpRequestLike {
  query?: Record<string, unknown>;
}

interface ProjectionDependencies {
  store: ProductProjectionStore;
  resolveActor: (uidToken: string, appCheckToken: string, tenantCandidate: unknown) => Promise<ActorContext>;
}

const projectionNames = new Set<ConsoleProjectionName>(['dashboard', 'users', 'devices', 'repairs', 'ledger', 'inspections', 'partners', 'reports', 'services']);

export async function getMobileProductSnapshotHandler(request: ProjectionHttpRequestLike, response: HttpResponseLike, overrides?: Partial<ProjectionDependencies>) {
  return executeProjection(request, response, 'mobile', overrides);
}

export async function getConsoleOperationsSnapshotHandler(request: ProjectionHttpRequestLike, response: HttpResponseLike, overrides?: Partial<ProjectionDependencies>) {
  return executeProjection(request, response, 'console', overrides);
}

async function executeProjection(request: ProjectionHttpRequestLike, response: HttpResponseLike, surface: 'mobile' | 'console', overrides?: Partial<ProjectionDependencies>) {
  try {
    if (request.method !== 'GET') throw new DomainCommandError('METHOD_NOT_ALLOWED', 'Only GET is supported.', 405);
    const authHeader = singleHeader(request.headers, 'authorization');
    const appCheckHeader = singleHeader(request.headers, 'x-firebase-appcheck');
    if (!authHeader || !/^Bearer\s+\S+$/.test(authHeader)) throw new DomainCommandError('AUTH_REQUIRED', 'A Firebase ID token is required.', 401);
    if (!appCheckHeader) throw new DomainCommandError('APP_CHECK_REQUIRED', 'A Firebase App Check token is required.', 401);
    const tenantCandidate = exactTenantCandidate(request);
    const actor = await (overrides?.resolveActor ?? resolveActorContext)(authHeader.slice(7), appCheckHeader, tenantCandidate);
    const app = getApps().length ? getApp() : initializeApp();
    const store = overrides?.store ?? new FirestoreProductProjectionStore(getFirestore(app));
    setPrivateResponseHeaders(response);
    if (surface === 'mobile') return response.status(200).json(await store.getMobileSnapshot(actor));
    const projection = exactQueryString(request.query?.projection, 'projection');
    if (!projectionNames.has(projection as ConsoleProjectionName)) throw new DomainCommandError('INVALID_PROJECTION', 'The requested operations projection is not supported.');
    return response.status(200).json(await store.getConsoleProjection(actor, projection as ConsoleProjectionName));
  } catch (error) {
    setPrivateResponseHeaders(response);
    if (error instanceof DomainCommandError) return response.status(error.status).json({ error: { code: error.code, message: error.message } });
    return response.status(500).json({ error: { code: 'INTERNAL', message: 'The projection could not be generated.' } });
  }
}

function exactTenantCandidate(request: ProjectionHttpRequestLike): string {
  const header = singleHeader(request.headers, 'x-tenant-id');
  const query = request.query?.tenantId;
  if (header !== undefined && query !== undefined) throw new DomainCommandError('DUPLICATE_TENANT_SCOPE', 'Tenant scope must be supplied exactly once.');
  return safeId(header ?? exactQueryString(query, 'tenantId'), 'tenantId');
}

function exactQueryString(value: unknown, field: string): string {
  if (Array.isArray(value)) throw new DomainCommandError(`DUPLICATE_${field.toUpperCase()}`, `${field} must appear exactly once.`);
  if (typeof value !== 'string' || !value.trim()) throw new DomainCommandError(`INVALID_${field.toUpperCase()}`, `${field} is required.`);
  return value.trim();
}

function singleHeader(headers: HttpRequestLike['headers'], name: string): string | undefined {
  const value = headers[name] ?? headers[name.toLowerCase()];
  if (Array.isArray(value)) {
    if (value.length !== 1) throw new DomainCommandError('DUPLICATE_HEADER', `${name} must appear exactly once.`);
    return value[0];
  }
  if (typeof value === 'string' && value.includes(',')) throw new DomainCommandError('DUPLICATE_HEADER', `${name} must appear exactly once.`);
  return value;
}

function setPrivateResponseHeaders(response: HttpResponseLike) {
  response.set?.('Cache-Control', 'private, no-store, max-age=0');
  response.set?.('Pragma', 'no-cache');
  response.set?.('X-Content-Type-Options', 'nosniff');
}
