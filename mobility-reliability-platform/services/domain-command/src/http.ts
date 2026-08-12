import { getAppCheck } from 'firebase-admin/app-check';
import { getApps, getApp, initializeApp } from 'firebase-admin/app';
import { getAuth } from 'firebase-admin/auth';
import { getFirestore } from 'firebase-admin/firestore';
import { DomainCommandError, assertIdempotencyKey, bodyHash, safeId } from './canonical.js';
import { FirestoreDomainCommandStore } from './firebase-store.js';
import { normalizeCreateCommand, normalizeSubsidyCommand, normalizeTransitionCommand } from './workflow.js';
import type { ActorContext, Role } from './types.js';
import type { CommandStore } from './store.js';

export interface HttpRequestLike {
  method?: string;
  headers: Record<string, string | string[] | undefined>;
  body?: unknown;
}

export interface HttpResponseLike {
  status(code: number): HttpResponseLike;
  json(value: unknown): unknown;
}

interface CommandDependencies {
  store: CommandStore;
  resolveActor: (uidToken: string, appCheckToken: string, tenantCandidate: unknown) => Promise<ActorContext>;
}

function adminApp() {
  return getApps().length ? getApp() : initializeApp();
}

export async function resolveActorContext(uidToken: string, appCheckToken: string, tenantCandidate: unknown): Promise<ActorContext> {
  const app = adminApp();
  const [decodedIdToken, decodedAppCheck] = await Promise.all([
    getAuth(app).verifyIdToken(uidToken),
    getAppCheck(app).verifyToken(appCheckToken),
  ]);
  const tenantId = safeId(tenantCandidate, 'tenantId');
  const db = getFirestore(app);
  const [tenantSnapshot, membershipSnapshot] = await Promise.all([
    db.doc(`tenants/${tenantId}`).get(),
    db.doc(`tenants/${tenantId}/memberships/${decodedIdToken.uid}`).get(),
  ]);
  const tenant = tenantSnapshot.data() as { tenant_id?: string; status?: string; allowed_app_ids?: unknown } | undefined;
  if (!tenantSnapshot.exists || tenant?.tenant_id !== tenantId || tenant.status !== 'active') throw new DomainCommandError('TENANT_INACTIVE', 'The institution is not active.', 403);
  if (!membershipSnapshot.exists) throw new DomainCommandError('MEMBERSHIP_REQUIRED', 'The authenticated user is not an active member of this institution.', 403);
  const membership = membershipSnapshot.data() as { tenant_id?: string; firebase_uid?: string; status?: string; roles?: unknown; person_id?: string; valid_from?: unknown; valid_to?: unknown } | undefined;
  if (membership?.tenant_id !== tenantId || membership.firebase_uid !== decodedIdToken.uid || membership.status !== 'active' || !Array.isArray(membership.roles)) throw new DomainCommandError('MEMBERSHIP_REQUIRED', 'The authenticated user is not an active member of this institution.', 403);
  const now = Date.now();
  const validFrom = firestoreMillis(membership.valid_from);
  const validTo = membership.valid_to === undefined || membership.valid_to === null ? undefined : firestoreMillis(membership.valid_to);
  if (validFrom === undefined || validFrom > now || (validTo !== undefined && now >= validTo)) throw new DomainCommandError('MEMBERSHIP_EXPIRED', 'The institution membership is outside its validity period.', 403);
  const roles = membership.roles.filter((role): role is Role => ['beneficiary', 'guardian', 'case_worker', 'repairer', 'tenant_admin', 'auditor'].includes(String(role)));
  if (roles.length === 0 || roles.length !== membership.roles.length) throw new DomainCommandError('MEMBERSHIP_INVALID', 'The institution membership has no valid role.', 403);
  if (Array.isArray(tenant.allowed_app_ids) && !tenant.allowed_app_ids.includes(decodedAppCheck.appId)) throw new DomainCommandError('APP_NOT_ALLOWED', 'This application is not allowed for the institution.', 403);
  return { uid: decodedIdToken.uid, tenantId, roles, ...(membership.person_id === undefined ? {} : { personId: membership.person_id }), appId: decodedAppCheck.appId };
}

function firestoreMillis(value: unknown): number | undefined {
  if (value && typeof value === 'object' && 'toMillis' in value && typeof (value as { toMillis?: unknown }).toMillis === 'function') return (value as { toMillis(): number }).toMillis();
  if (typeof value === 'string') {
    const parsed = Date.parse(value);
    return Number.isNaN(parsed) ? undefined : parsed;
  }
  return undefined;
}

export async function createRepairRequestHandler(request: HttpRequestLike, response: HttpResponseLike, overrides?: Partial<CommandDependencies>) {
  return execute(request, response, 'create', overrides);
}

export async function transitionRepairRequestHandler(request: HttpRequestLike, response: HttpResponseLike, overrides?: Partial<CommandDependencies>) {
  return execute(request, response, 'transition', overrides);
}

export async function appendSubsidyTransactionHandler(request: HttpRequestLike, response: HttpResponseLike, overrides?: Partial<CommandDependencies>) {
  return execute(request, response, 'subsidy', overrides);
}

async function execute(request: HttpRequestLike, response: HttpResponseLike, operation: 'create' | 'transition' | 'subsidy', overrides?: Partial<CommandDependencies>) {
  try {
    if (request.method !== 'POST') throw new DomainCommandError('METHOD_NOT_ALLOWED', 'Only POST is supported.', 405);
    const idempotencyKey = singleHeader(request.headers, 'idempotency-key');
    assertIdempotencyKey(idempotencyKey);
    const body = asObject(request.body);
    const { tenantId, ...payload } = body;
    const authHeader = singleHeader(request.headers, 'authorization');
    const appCheckHeader = singleHeader(request.headers, 'x-firebase-appcheck');
    if (!authHeader || !/^Bearer\s+\S+$/.test(authHeader)) throw new DomainCommandError('AUTH_REQUIRED', 'A Firebase ID token is required.', 401);
    if (!appCheckHeader) throw new DomainCommandError('APP_CHECK_REQUIRED', 'A Firebase App Check token is required.', 401);
    const actor = await (overrides?.resolveActor ?? resolveActorContext)(authHeader.slice(7), appCheckHeader, tenantId);
    const store = overrides?.store ?? new FirestoreDomainCommandStore(getFirestore(adminApp()));
    const hash = bodyHash(body);
    if (operation === 'create') {
      const command = normalizeCreateCommand(payload);
      return response.status(201).json(await store.createRepair({ actor, command, idempotencyKey, bodyHash: hash }));
    }
    if (operation === 'transition') {
      const command = normalizeTransitionCommand(payload);
      return response.status(200).json(await store.transitionRepair({ actor, command, idempotencyKey, bodyHash: hash }));
    }
    const command = normalizeSubsidyCommand(payload);
    return response.status(200).json(await store.appendSubsidy({ actor, command, idempotencyKey, bodyHash: hash }));
  } catch (error) {
    if (error instanceof DomainCommandError) return response.status(error.status).json({ error: { code: error.code, message: error.message } });
    return response.status(500).json({ error: { code: 'INTERNAL', message: 'The command could not be completed.' } });
  }
}

function asObject(body: unknown): Record<string, unknown> {
  if (!body || typeof body !== 'object' || Array.isArray(body)) throw new DomainCommandError('INVALID_COMMAND', 'Request body must be an object.');
  return body as Record<string, unknown>;
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
