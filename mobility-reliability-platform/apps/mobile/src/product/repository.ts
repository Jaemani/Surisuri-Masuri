import {
  demoDevice,
  demoRepairJobs,
  demoRepairRequest,
  demoRepairerRoleSession,
  demoSubsidy,
  demoUserRoleSession,
} from './data';
import { isRepairerProductSnapshot } from './types';
import type {
  CreateRepairRequestInput,
  ProductRole,
  ProductSnapshot,
  RepairJob,
  RepairWorkOrder,
  RoleSession,
  DeviceSummary,
  SubsidySummary,
  DemoProductSnapshot,
  BeneficiaryProductSnapshot,
  RepairerJobCommand,
} from './types';

export type ProductRepositoryErrorCode =
  | 'NOT_CONFIGURED'
  | 'AUTH_REQUIRED'
  | 'APP_CHECK_REQUIRED'
  | 'NETWORK_ERROR'
  | 'HTTP_ERROR'
  | 'INVALID_RESPONSE'
  | 'PROJECTION_PENDING'
  | 'REVISION_CONFLICT'
  | 'IDEMPOTENCY_CONFLICT'
  | 'REPAIR_ASSIGNMENT_REQUIRED'
  | 'REPAIR_TRANSITION_FORBIDDEN'
  | 'ROLE_SWITCH_UNSUPPORTED';

export class ProductRepositoryError extends Error {
  readonly code: ProductRepositoryErrorCode;
  readonly status?: number;

  constructor(code: ProductRepositoryErrorCode, message: string, status?: number) {
    super(message);
    this.name = 'ProductRepositoryError';
    this.code = code;
    this.status = status;
  }
}

export interface ProductRepository {
  getSnapshot(): Promise<ProductSnapshot>;
  createRepairRequest(input: CreateRepairRequestInput): Promise<RepairWorkOrder>;
  transitionRepairJob(input: RepairerJobCommand): Promise<RepairJob>;
  setRole(role: ProductRole): Promise<RoleSession>;
}

function copySnapshot(snapshot: ProductSnapshot): ProductSnapshot {
  if (isRepairerProductSnapshot(snapshot)) {
    return {
      roleSession: { ...snapshot.roleSession },
      repairJobs: snapshot.repairJobs.map(copyRepairJob),
    };
  }
  return {
    ...snapshot,
    roleSession: { ...snapshot.roleSession },
    repairRequest: snapshot.repairRequest ? { ...snapshot.repairRequest } : null,
    device: { ...snapshot.device, timeline: snapshot.device.timeline.map((item) => ({ ...item })) },
    subsidy: { ...snapshot.subsidy },
    ...(snapshot.repairJobs ? { repairJobs: snapshot.repairJobs.map(copyRepairJob) } : {}),
  };
}

export const demoProductSnapshot: DemoProductSnapshot = {
  roleSession: demoUserRoleSession,
  repairRequest: demoRepairRequest,
  device: demoDevice,
  subsidy: demoSubsidy,
  repairJobs: demoRepairJobs,
};

/**
 * Deterministic local adapter used by the product prototype.
 *
 * It is intentionally in-memory: commands exercise the same async boundary
 * as a network repository without pretending that demo data is persisted.
 */
export class DemoProductRepository implements ProductRepository {
  private snapshot: ProductSnapshot;
  private readonly userSnapshot: BeneficiaryProductSnapshot | null;

  constructor(seed: ProductSnapshot = demoProductSnapshot) {
    this.snapshot = copySnapshot(seed);
    this.userSnapshot = !isRepairerProductSnapshot(this.snapshot) ? copySnapshot(this.snapshot) as BeneficiaryProductSnapshot : null;
  }

  async getSnapshot(): Promise<ProductSnapshot> {
    return copySnapshot(this.snapshot);
  }

  async createRepairRequest(input: CreateRepairRequestInput): Promise<RepairWorkOrder> {
    const request: RepairWorkOrder = {
      id: 'demo-request-new',
      title: input.title,
      createdAt: '2026년 8월 13일',
      status: 'received',
      repairer: '가까운 수리센터를 찾는 중',
      visitAt: '센터 배정 후 안내해 드려요',
    };
    if (isRepairerProductSnapshot(this.snapshot)) {
      throw new ProductRepositoryError('ROLE_SWITCH_UNSUPPORTED', 'Repair requests are only available in the beneficiary view.');
    }
    this.snapshot = { ...this.snapshot, repairRequest: request };
    return { ...request };
  }

  async transitionRepairJob(input: RepairerJobCommand): Promise<RepairJob> {
    if (!isRepairerProductSnapshot(this.snapshot)) throw new ProductRepositoryError('ROLE_SWITCH_UNSUPPORTED', 'Repairer commands require the repairer view.');
    const job = this.snapshot.repairJobs.find((candidate) => candidate.id === input.repairRequestId);
    if (!job || job.revision !== input.expectedRevision || !job.allowedActions.includes(input.action)) throw new ProductRepositoryError('REVISION_CONFLICT', 'The repair job changed; reload before trying again.', 409);
    const nextStatus = input.action === 'schedule' ? 'scheduled' : input.action === 'submit' ? 'repairer_submitted' : 'in_progress';
    const next: RepairJob = {
      ...job,
      status: nextStatus,
      revision: job.revision + 1,
      ...(input.action === 'schedule' ? { scheduledAt: input.scheduledAt, scheduleLabel: demoScheduleLabel(input.scheduledAt) } : {}),
      ...(input.action === 'submit' ? { billedAmountKrw: input.billedAmountKrw, submittedAt: new Date().toISOString() } : {}),
      allowedActions: nextStatus === 'scheduled' ? ['start'] : nextStatus === 'in_progress' ? ['submit'] : [],
    };
    this.snapshot = { ...this.snapshot, repairJobs: this.snapshot.repairJobs.map((candidate) => candidate.id === next.id ? next : candidate) };
    return copyRepairJob(next);
  }

  async setRole(role: ProductRole): Promise<RoleSession> {
    if (role === 'repairer') {
      const jobs = isRepairerProductSnapshot(this.snapshot)
        ? this.snapshot.repairJobs
        : this.snapshot.repairJobs ?? this.userSnapshot?.repairJobs ?? [];
      this.snapshot = { roleSession: { ...demoRepairerRoleSession }, repairJobs: (jobs ?? []).map(copyRepairJob) };
    } else {
      if (!this.userSnapshot) throw new ProductRepositoryError('ROLE_SWITCH_UNSUPPORTED', 'A beneficiary snapshot is not available.');
      this.snapshot = { ...this.userSnapshot, roleSession: { ...demoUserRoleSession } };
    }
    const roleSession = this.snapshot.roleSession;
    return { ...roleSession };
  }
}

/** Minimal token provider shapes so the app does not import Firebase clients. */
export interface FirebaseAuthTokenProvider {
  getIdToken(forceRefresh?: boolean): Promise<string | null>;
}

export interface FirebaseAppCheckTokenProvider {
  getToken(forceRefresh?: boolean): Promise<string | { token?: string } | null>;
}

export interface ProductHttpResponse {
  status: number;
  ok: boolean;
  json(): Promise<unknown>;
}

export type ProductHttpFetch = (
  url: string,
  init: { method: 'GET' | 'POST'; headers: Record<string, string>; body?: string },
) => Promise<ProductHttpResponse>;

export type FirebaseProductRepositoryOptions = {
  /** Firebase callable/function URL or an API gateway origin. */
  baseUrl: string;
  auth: FirebaseAuthTokenProvider;
  appCheck: FirebaseAppCheckTokenProvider;
  /** Candidate tenant/person/device identifiers. The server remains authoritative. */
  tenantId: string;
  beneficiaryId: string;
  deviceId: string;
  /** Required because the Domain Command contract requires this boolean. */
  defaultPublicFundingInvolved?: boolean;
  defaultRequestedAmountKrw?: number;
  endpoints?: {
    snapshot?: string;
    createRepairRequest?: string;
    transitionRepairRequest?: string;
  };
  /** Required when the runtime does not expose Web Crypto randomUUID. */
  createIdempotencyKey?: () => string;
  fetch?: ProductHttpFetch;
};

/** Deployed Firebase Functions names used by the mobile product surface. */
export const mobileProductEndpoints = {
  snapshot: '/getMobileProductSnapshot',
  createRepairRequest: '/createRepairRequest',
  transitionRepairRequest: '/transitionRepairRequest',
} as const;

/**
 * Production adapter for the Firebase-first product.
 *
 * This class deliberately has no Firebase Firestore import. Firebase Auth and
 * App Check are injected as token providers, and every mutation goes through
 * the Domain Command HTTP boundary. Missing credentials, malformed responses,
 * and a not-yet-materialized projection are hard failures; demo data is never
 * used as an accidental production fallback.
 */
export class FirebaseProductRepository implements ProductRepository {
  private readonly options?: FirebaseProductRepositoryOptions;

  constructor(options?: FirebaseProductRepositoryOptions) {
    this.options = options;
  }

  async getSnapshot(): Promise<ProductSnapshot> {
    const response = await this.request('GET', this.endpoint('snapshot', true));
    return decodeProductSnapshot(await readJson(response));
  }

  async createRepairRequest(input: CreateRepairRequestInput): Promise<RepairWorkOrder> {
    const options = this.requireOptions();
    const title = input.title.trim();
    if (title.length === 0) {
      throw new ProductRepositoryError('INVALID_RESPONSE', 'A repair request needs a non-empty issue summary.');
    }
    if (!options.beneficiaryId || !options.deviceId) {
      throw new ProductRepositoryError('NOT_CONFIGURED', 'Beneficiary and device scope are required for a repair command.');
    }

    const publicFundingInvolved = input.publicFundingInvolved ?? options.defaultPublicFundingInvolved;
    if (typeof publicFundingInvolved !== 'boolean') {
      throw new ProductRepositoryError('NOT_CONFIGURED', 'The repair funding scope is not configured.');
    }
    const requestedAmountKrw = input.requestedAmountKrw ?? options.defaultRequestedAmountKrw;
    if (requestedAmountKrw !== undefined && (!Number.isSafeInteger(requestedAmountKrw) || requestedAmountKrw <= 0)) {
      throw new ProductRepositoryError('INVALID_RESPONSE', 'The requested repair amount must be a positive integer.');
    }

    const body: Record<string, unknown> = {
      tenantId: options.tenantId,
      beneficiaryId: options.beneficiaryId,
      deviceId: options.deviceId,
      issueSummary: title,
      publicFundingInvolved,
    };
    if (requestedAmountKrw !== undefined) body.requestedAmountKrw = requestedAmountKrw;

    const response = await this.request('POST', this.endpoint('createRepairRequest'), body, input.idempotencyKey);
    const result = decodeCreateCommandResult(await readJson(response));

    // The command service returns an auditable CommandResult, not a client-
    // authoritative work order. Read the server projection before updating UI.
    const snapshot = await this.getSnapshot();
    if (isRepairerProductSnapshot(snapshot)) {
      throw new ProductRepositoryError('PROJECTION_PENDING', 'The repair command projection is not available for the repairer role.');
    }
    const request = snapshot.repairRequest;
    if (!request || request.id !== result.resourceId) {
      throw new ProductRepositoryError(
        'PROJECTION_PENDING',
        'The repair command succeeded, but its read projection is not available yet.',
      );
    }
    return { ...request };
  }

  async transitionRepairJob(input: RepairerJobCommand): Promise<RepairJob> {
    const options = this.requireOptions();
    const body: Record<string, unknown> = {
      tenantId: options.tenantId,
      repairRequestId: input.repairRequestId,
      expectedRevision: input.expectedRevision,
      toStatus: input.action === 'schedule' ? 'scheduled' : input.action === 'submit' ? 'repairer_submitted' : 'in_progress',
    };
    if (input.action === 'schedule') body.scheduledAt = input.scheduledAt;
    if (input.action === 'submit') {
      if (!Number.isSafeInteger(input.billedAmountKrw) || input.billedAmountKrw <= 0) throw new ProductRepositoryError('INVALID_RESPONSE', 'The billed repair amount must be a positive integer.');
      body.billedAmountKrw = input.billedAmountKrw;
    }
    const response = await this.request('POST', this.endpoint('transitionRepairRequest'), body, input.idempotencyKey);
    const resourceId = decodeTransitionCommandResult(await readJson(response));
    const snapshot = await this.getSnapshot();
    if (!isRepairerProductSnapshot(snapshot)) throw new ProductRepositoryError('PROJECTION_PENDING', 'The repairer projection is not available.');
    const job = snapshot.repairJobs.find((candidate) => candidate.id === resourceId && candidate.revision > input.expectedRevision);
    if (!job) throw new ProductRepositoryError('PROJECTION_PENDING', 'The repair command succeeded, but its read projection is not available yet.');
    return copyRepairJob(job);
  }

  setRole(_role: ProductRole): Promise<RoleSession> {
    return Promise.reject(new ProductRepositoryError(
      'ROLE_SWITCH_UNSUPPORTED',
      'Production role changes must come from an authorized session or server projection.',
    ));
  }

  private requireOptions(): FirebaseProductRepositoryOptions {
    if (!this.options) {
      throw new ProductRepositoryError(
        'NOT_CONFIGURED',
        'Firebase product repository requires injected API, Auth, and App Check providers.',
      );
    }
    if (!this.options.baseUrl.trim() || !this.options.tenantId.trim()) {
      throw new ProductRepositoryError('NOT_CONFIGURED', 'A Firebase API origin and tenant scope are required.');
    }
    return this.options;
  }

  private endpoint(kind: 'snapshot' | 'createRepairRequest' | 'transitionRepairRequest', withTenant = false): string {
    const options = this.requireOptions();
    const path = options.endpoints?.[kind] ?? mobileProductEndpoints[kind];
    if (!path.trim()) throw new ProductRepositoryError('NOT_CONFIGURED', `${kind} endpoint is empty.`);
    const base = options.baseUrl.replace(/\/+$/, '');
    const url = /^https?:\/\//.test(path) ? path : `${base}/${path.replace(/^\/+/, '')}`;
    if (!withTenant) return url;
    const joiner = url.includes('?') ? '&' : '?';
    return `${url}${joiner}tenantId=${encodeURIComponent(options.tenantId)}`;
  }

  private async request(
    method: 'GET' | 'POST',
    url: string,
    body?: Record<string, unknown>,
    suppliedIdempotencyKey?: string,
  ): Promise<ProductHttpResponse> {
    const options = this.requireOptions();
    const idToken = await this.readIdToken(options.auth);
    const appCheckToken = await this.readAppCheckToken(options.appCheck);
    const fetcher = options.fetch ?? defaultProductFetch;
    const headers: Record<string, string> = {
      Accept: 'application/json',
      Authorization: `Bearer ${idToken}`,
      'X-Firebase-AppCheck': appCheckToken,
    };
    if (body !== undefined) {
      headers['Content-Type'] = 'application/json';
      headers['Idempotency-Key'] = suppliedIdempotencyKey ?? newIdempotencyKey(options);
    }

    let response: ProductHttpResponse;
    try {
      response = await fetcher(url, {
        method,
        headers,
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
    } catch (_error) {
      throw new ProductRepositoryError('NETWORK_ERROR', 'The product service could not be reached.');
    }

    if (!response.ok) {
      const error = await readError(response);
      throw new ProductRepositoryError(error.code, error.message, response.status);
    }
    return response;
  }

  private async readIdToken(provider: FirebaseAuthTokenProvider): Promise<string> {
    try {
      const token = await provider.getIdToken();
      if (!token?.trim()) throw new Error('missing token');
      return token;
    } catch (_error) {
      throw new ProductRepositoryError('AUTH_REQUIRED', 'A Firebase ID token is required.');
    }
  }

  private async readAppCheckToken(provider: FirebaseAppCheckTokenProvider): Promise<string> {
    try {
      const result = await provider.getToken();
      const token = typeof result === 'string' ? result : result?.token;
      if (!token?.trim()) throw new Error('missing token');
      return token;
    } catch (_error) {
      throw new ProductRepositoryError('APP_CHECK_REQUIRED', 'A Firebase App Check token is required.');
    }
  }
}

const defaultProductFetch: ProductHttpFetch = (url, init) => globalThis.fetch(url, init).then((response) => ({
  status: response.status,
  ok: response.ok,
  json: () => response.json(),
}));

function newIdempotencyKey(options: FirebaseProductRepositoryOptions): string {
  const key = options.createIdempotencyKey?.() ?? globalThis.crypto?.randomUUID?.();
  if (!key || typeof key !== 'string') {
    throw new ProductRepositoryError('NOT_CONFIGURED', 'A cryptographically unique idempotency key provider is required for repair commands.');
  }
  return key;
}

async function readJson(response: ProductHttpResponse): Promise<unknown> {
  try {
    return await response.json();
  } catch (_error) {
    throw new ProductRepositoryError('INVALID_RESPONSE', 'The product service returned invalid JSON.', response.status);
  }
}

async function readError(response: ProductHttpResponse): Promise<{ code: ProductRepositoryErrorCode; message: string }> {
  try {
    const body = await response.json();
    const error = body && typeof body === 'object' && 'error' in body ? (body as { error?: unknown }).error : undefined;
    const code = error && typeof error === 'object' && 'code' in error ? (error as { code?: unknown }).code : undefined;
    const message = error && typeof error === 'object' && 'message' in error ? (error as { message?: unknown }).message : undefined;
    const preserved = ['REVISION_CONFLICT', 'IDEMPOTENCY_CONFLICT', 'REPAIR_ASSIGNMENT_REQUIRED', 'REPAIR_TRANSITION_FORBIDDEN'] as const;
    return {
      code: response.status === 401 ? 'AUTH_REQUIRED' : preserved.includes(code as typeof preserved[number]) ? code as typeof preserved[number] : 'HTTP_ERROR',
      message: typeof message === 'string' && message.length > 0 ? message : `The product service rejected the request (${String(code ?? response.status)}).`,
    };
  } catch (_error) {
    return { code: response.status === 401 ? 'AUTH_REQUIRED' : 'HTTP_ERROR', message: `The product service rejected the request (${response.status}).` };
  }
}

function decodeCreateCommandResult(payload: unknown): { resourceId: string } {
  const candidate = unwrapData(payload);
  if (!candidate || typeof candidate !== 'object') throw new ProductRepositoryError('INVALID_RESPONSE', 'The repair command response is invalid.');
  const result = candidate as { commandType?: unknown; resourceId?: unknown };
  if (result.commandType !== 'create_repair_request' || typeof result.resourceId !== 'string' || result.resourceId.length === 0) {
    throw new ProductRepositoryError('INVALID_RESPONSE', 'The repair command response is missing its resource identity.');
  }
  return { resourceId: result.resourceId };
}

function decodeTransitionCommandResult(payload: unknown): string {
  const candidate = unwrapData(payload);
  if (!candidate || typeof candidate !== 'object') throw new ProductRepositoryError('INVALID_RESPONSE', 'The repair transition response is invalid.');
  const result = candidate as { commandType?: unknown; resourceId?: unknown };
  if (result.commandType !== 'transition_repair_request' || typeof result.resourceId !== 'string' || !result.resourceId) throw new ProductRepositoryError('INVALID_RESPONSE', 'The repair transition response is missing its resource identity.');
  return result.resourceId;
}

function decodeProductSnapshot(payload: unknown): ProductSnapshot {
  const candidate = unwrapData(payload);
  if (!candidate || typeof candidate !== 'object') throw new ProductRepositoryError('INVALID_RESPONSE', 'The product snapshot response is invalid.');
  const snapshot = candidate as {
    roleSession?: { role?: unknown; displayName?: unknown };
    repairRequest?: unknown;
    device?: unknown;
    subsidy?: unknown;
    repairJobs?: unknown;
  };
  const roleSession = snapshot.roleSession;
  if (!roleSession || (roleSession.role !== 'user' && roleSession.role !== 'repairer') || typeof roleSession.displayName !== 'string') {
    throw new ProductRepositoryError('INVALID_RESPONSE', 'The product snapshot has an invalid role session.');
  }
  if (roleSession.role === 'repairer') {
    if (!Array.isArray(snapshot.repairJobs) || !snapshot.repairJobs.every(validRepairJob)) {
      throw new ProductRepositoryError('INVALID_RESPONSE', 'The repairer snapshot is missing a valid repair jobs projection.');
    }
    return { roleSession: { role: 'repairer', displayName: roleSession.displayName, isDemo: false }, repairJobs: snapshot.repairJobs.map((job) => decodeRepairJob(job)) };
  }
  if (!snapshot.device || !validDevice(snapshot.device) || !snapshot.subsidy || !validSubsidy(snapshot.subsidy)) {
    throw new ProductRepositoryError('INVALID_RESPONSE', 'The beneficiary snapshot is missing a valid device or subsidy projection.');
  }
  if (snapshot.repairRequest !== null && snapshot.repairRequest !== undefined && !validWorkOrder(snapshot.repairRequest)) {
    throw new ProductRepositoryError('INVALID_RESPONSE', 'The beneficiary snapshot has an invalid repair request.');
  }
  if (snapshot.repairJobs !== undefined && (!Array.isArray(snapshot.repairJobs) || !snapshot.repairJobs.every(validRepairJob))) {
    throw new ProductRepositoryError('INVALID_RESPONSE', 'The beneficiary snapshot has an invalid repair jobs projection.');
  }
  return {
    roleSession: { role: 'user', displayName: roleSession.displayName, isDemo: false },
    repairRequest: snapshot.repairRequest ? { ...snapshot.repairRequest } : null,
    device: { ...snapshot.device, timeline: snapshot.device.timeline.map((item) => ({ ...item })) },
    subsidy: { ...snapshot.subsidy },
    ...(snapshot.repairJobs ? { repairJobs: snapshot.repairJobs.map((job) => ({ ...job })) } : {}),
  };
}

function unwrapData(payload: unknown): unknown {
  if (!payload || typeof payload !== 'object') return payload;
  const record = payload as Record<string, unknown>;
  if ('data' in record && record.data && typeof record.data === 'object') return record.data;
  if ('snapshot' in record) return record.snapshot;
  return payload;
}

function validWorkOrder(value: unknown): value is RepairWorkOrder {
  if (!value || typeof value !== 'object') return false;
  const item = value as Partial<RepairWorkOrder>;
  return typeof item.id === 'string' && typeof item.title === 'string' && typeof item.createdAt === 'string'
    && typeof item.status === 'string' && typeof item.repairer === 'string' && typeof item.visitAt === 'string';
}

function validDevice(value: unknown): value is DeviceSummary {
  if (!value || typeof value !== 'object') return false;
  const item = value as Partial<DeviceSummary>;
  return typeof item.id === 'string' && typeof item.name === 'string' && typeof item.registrationNumber === 'string'
    && typeof item.registeredAt === 'string' && (item.status === 'healthy' || item.status === 'attention')
    && Array.isArray(item.timeline) && item.timeline.every((timeline) => timeline && typeof timeline.id === 'string' && typeof timeline.date === 'string' && typeof timeline.title === 'string' && typeof timeline.detail === 'string');
}

function validSubsidy(value: unknown): value is SubsidySummary {
  if (!value || typeof value !== 'object') return false;
  const item = value as Partial<SubsidySummary>;
  return typeof item.program === 'string' && typeof item.cycle === 'string' && Number.isSafeInteger(item.used)
    && Number.isSafeInteger(item.total) && typeof item.nextReview === 'string' && typeof item.note === 'string';
}

function validRepairJob(value: unknown): value is RepairJob {
  if (!value || typeof value !== 'object') return false;
  const item = value as Partial<RepairJob>;
  return typeof item.id === 'string' && Number.isSafeInteger(item.revision) && (item.revision ?? 0) > 0
    && ['assigned', 'scheduled', 'in_progress', 'repairer_submitted', 'needs_correction', 'center_verified'].includes(String(item.status))
    && typeof item.customerLabel === 'string' && !!item.device && typeof item.device.publicCode === 'string' && typeof item.device.model === 'string'
    && typeof item.issue === 'string' && (item.scheduledAt === null || typeof item.scheduledAt === 'string') && typeof item.scheduleLabel === 'string'
    && (item.priority === 'today' || item.priority === 'scheduled') && (item.billedAmountKrw === null || Number.isSafeInteger(item.billedAmountKrw))
    && (item.submittedAt === null || typeof item.submittedAt === 'string') && Array.isArray(item.allowedActions)
    && item.allowedActions.every((action) => ['schedule', 'start', 'submit', 'resume'].includes(action));
}

function decodeRepairJob(value: unknown): RepairJob {
  if (!validRepairJob(value)) throw new ProductRepositoryError('INVALID_RESPONSE', 'The repair job projection is invalid.');
  return copyRepairJob(value);
}

function copyRepairJob(job: RepairJob): RepairJob {
  return {
    id: job.id,
    revision: job.revision,
    status: job.status,
    customerLabel: job.customerLabel,
    device: { publicCode: job.device.publicCode, model: job.device.model },
    issue: job.issue,
    scheduledAt: job.scheduledAt,
    scheduleLabel: job.scheduleLabel,
    priority: job.priority,
    billedAmountKrw: job.billedAmountKrw,
    submittedAt: job.submittedAt,
    allowedActions: [...job.allowedActions],
  };
}
function demoScheduleLabel(value: string) { return new Intl.DateTimeFormat('ko-KR', { timeZone: 'Asia/Seoul', month: 'long', day: 'numeric', weekday: 'short', hour: 'numeric', minute: '2-digit' }).format(new Date(value)); }

export type ProductRepositorySource = 'demo' | 'firebase';

export function createProductRepository(
  source: ProductRepositorySource = 'demo',
  options?: FirebaseProductRepositoryOptions,
): ProductRepository {
  return source === 'firebase' ? new FirebaseProductRepository(options) : new DemoProductRepository();
}
