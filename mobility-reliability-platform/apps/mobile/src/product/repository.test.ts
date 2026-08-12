import { describe, expect, it } from 'vitest';

import { demoProductSnapshot, DemoProductRepository, FirebaseProductRepository, mobileProductEndpoints, ProductRepositoryError } from './repository';
import type { FirebaseProductRepositoryOptions, ProductHttpResponse } from './repository';
import { isBeneficiaryProductSnapshot, isRepairerProductSnapshot } from './types';

describe('DemoProductRepository', () => {
  it('returns deterministic product data without sharing mutable arrays', async () => {
    const repository = new DemoProductRepository();
    const first = await repository.getSnapshot();
    if (!isBeneficiaryProductSnapshot(first)) throw new Error('expected beneficiary snapshot');
    first.device.timeline.pop();
    first.repairJobs?.[0] && (first.repairJobs[0].customerLabel = '변경된 이름');

    const second = await repository.getSnapshot();
    if (!isBeneficiaryProductSnapshot(second)) throw new Error('expected beneficiary snapshot');
    expect(second.device.timeline).toHaveLength(3);
    expect(isBeneficiaryProductSnapshot(second) ? second.repairJobs?.[0]?.customerLabel : undefined).toBe('이용자 C-1042');
    expect(second.roleSession).toEqual({ role: 'user', displayName: '김정자 님', isDemo: true });
  });

  it('applies async repair request and role commands to the snapshot', async () => {
    const repository = new DemoProductRepository();

    const request = await repository.createRepairRequest({ title: '브레이크가 뻑뻑해요' });
    expect(request.status).toBe('received');
    const current = await repository.getSnapshot();
    if (!isBeneficiaryProductSnapshot(current)) throw new Error('expected beneficiary snapshot');
    expect(current.repairRequest).toMatchObject({
      id: 'demo-request-new',
      title: '브레이크가 뻑뻑해요',
    });

    const role = await repository.setRole('repairer');
    expect(role).toMatchObject({ role: 'repairer', isDemo: true });
    expect((await repository.getSnapshot()).roleSession.role).toBe('repairer');
  });
});

describe('FirebaseProductRepository', () => {
  it('fails closed with NOT_CONFIGURED until providers and endpoints are configured', async () => {
    const repository = new FirebaseProductRepository();

    await expect(repository.getSnapshot()).rejects.toMatchObject({ code: 'NOT_CONFIGURED' });
    await expect(repository.createRepairRequest({ title: '수리 요청' })).rejects.toMatchObject({ code: 'NOT_CONFIGURED' });
    await expect(repository.setRole('repairer')).rejects.toMatchObject({ code: 'ROLE_SWITCH_UNSUPPORTED' });
  });

  it('injects Firebase ID token and App Check token without importing Firestore', async () => {
    const requests: Array<{ url: string; init: { method: string; headers: Record<string, string>; body?: string } }> = [];
    const options = firebaseOptions(async (url, init) => {
      requests.push({ url, init });
      return jsonResponse(200, demoProductSnapshot);
    });
    const repository = new FirebaseProductRepository(options);

    const snapshot = await repository.getSnapshot();

    expect(snapshot.roleSession.isDemo).toBe(false);
    expect(requests).toHaveLength(1);
    expect(requests[0].url).toBe(`https://example.test${mobileProductEndpoints.snapshot}?tenantId=tenant-1`);
    expect(requests[0].init.headers.Authorization).toBe('Bearer id-token');
    expect(requests[0].init.headers['X-Firebase-AppCheck']).toBe('app-check-token');
    expect(requests[0].init.method).toBe('GET');
    expect(requests[0].init.body).toBeUndefined();
  });

  it('decodes the beneficiary projection without requiring repairer-only jobs', async () => {
    const { repairJobs: _repairJobs, ...beneficiaryProjection } = demoProductSnapshot;
    const repository = new FirebaseProductRepository(firebaseOptions(async () => jsonResponse(200, beneficiaryProjection)));

    const snapshot = await repository.getSnapshot();

    expect(snapshot.roleSession).toEqual({ role: 'user', displayName: '김정자 님', isDemo: false });
    expect('repairJobs' in snapshot).toBe(false);
  });

  it('decodes the repairer projection without requiring beneficiary-only fields', async () => {
    const repository = new FirebaseProductRepository(firebaseOptions(async () => jsonResponse(200, {
      roleSession: { role: 'repairer', displayName: '따뜻한바퀴 수리센터', isDemo: false },
      repairJobs: [{ id: 'job-1', revision: 3, status: 'assigned', customerLabel: '이용자 C-1042', device: { publicCode: 'MOB-1', model: '나래 모빌리티 M-22' }, issue: '브레이크 점검', scheduledAt: null, scheduleLabel: '일정 협의 필요', priority: 'today', billedAmountKrw: null, submittedAt: null, allowedActions: ['schedule'], subsidyAccountId: 'must-not-spread' }],
    })));

    const snapshot = await repository.getSnapshot();

    if (!isRepairerProductSnapshot(snapshot)) throw new Error('expected repairer snapshot');
    expect(snapshot.roleSession.role).toBe('repairer');
    expect(snapshot.repairJobs).toHaveLength(1);
    expect(snapshot.repairJobs[0]).not.toHaveProperty('subsidyAccountId');
    expect('device' in snapshot).toBe(false);
  });

  it('posts a narrow repairer schedule command and returns the authoritative refreshed job', async () => {
    const requests: Array<{ url: string; init: { method: string; headers: Record<string, string>; body?: string } }> = [];
    const projectedJob = { ...demoProductSnapshot.repairJobs[0], status: 'scheduled' as const, revision: 4, scheduledAt: '2026-08-20T05:00:00.000Z', scheduleLabel: '8월 20일 오후 2:00', allowedActions: ['start' as const] };
    const repository = new FirebaseProductRepository(firebaseOptions(async (url, init) => {
      requests.push({ url, init });
      return init.method === 'POST'
        ? jsonResponse(200, { commandType: 'transition_repair_request', resourceId: projectedJob.id, revision: 4, status: 'scheduled' })
        : jsonResponse(200, { roleSession: { role: 'repairer', displayName: '따뜻한바퀴 수리센터' }, repairJobs: [projectedJob] });
    }));

    const job = await repository.transitionRepairJob({ action: 'schedule', repairRequestId: projectedJob.id, expectedRevision: 3, scheduledAt: projectedJob.scheduledAt!, idempotencyKey: 'schedule-key-1' });
    expect(job).toMatchObject({ status: 'scheduled', revision: 4 });
    expect(JSON.parse(requests[0].init.body!)).toEqual({ tenantId: 'tenant-1', repairRequestId: projectedJob.id, expectedRevision: 3, toStatus: 'scheduled', scheduledAt: projectedJob.scheduledAt });
    expect(requests[0].init.headers['Idempotency-Key']).toBe('schedule-key-1');
    expect(requests[1].init.method).toBe('GET');
  });

  it('preserves revision conflicts and does not silently retry a mutation', async () => {
    let calls = 0;
    const repository = new FirebaseProductRepository(firebaseOptions(async () => {
      calls += 1;
      return jsonResponse(409, { error: { code: 'REVISION_CONFLICT', message: '최신 상태를 다시 확인해 주세요.' } });
    }));
    await expect(repository.transitionRepairJob({ action: 'start', repairRequestId: 'job-1', expectedRevision: 4, idempotencyKey: 'start-key-1' })).rejects.toMatchObject({ code: 'REVISION_CONFLICT', status: 409 });
    expect(calls).toBe(1);
  });

  it('posts a Domain Command and returns only the matching server projection', async () => {
    const requests: Array<{ url: string; init: { method: string; headers: Record<string, string>; body?: string } }> = [];
    const projected = {
      ...demoProductSnapshot,
      repairRequest: { ...demoProductSnapshot.repairRequest!, id: 'repair-created-1' },
    };
    const options = firebaseOptions(async (url, init) => {
      requests.push({ url, init });
      return requests.length === 1
        ? jsonResponse(201, { commandType: 'create_repair_request', resourceId: 'repair-created-1', eventId: 'event-1', tenantId: 'tenant-1' })
        : jsonResponse(200, { data: projected });
    });
    const repository = new FirebaseProductRepository(options);

    const request = await repository.createRepairRequest({
      title: '브레이크가 뻑뻑해요',
      requestedAmountKrw: 45000,
      idempotencyKey: 'repair-retry-key-1',
    });
    const commandBody = JSON.parse(requests[0].init.body!);

    expect(request.id).toBe('repair-created-1');
    expect(commandBody).toEqual({
      tenantId: 'tenant-1',
      beneficiaryId: 'person-1',
      deviceId: 'device-1',
      issueSummary: '브레이크가 뻑뻑해요',
      publicFundingInvolved: true,
      requestedAmountKrw: 45000,
    });
    expect(requests[0].init.headers['Idempotency-Key']).toBe('repair-retry-key-1');
    expect(requests[1].init.method).toBe('GET');
  });

  it('does not fall back to demo data when Auth or App Check is unavailable', async () => {
    const fetcher = async () => {
      throw new Error('must not be called');
    };
    const authFailure = new FirebaseProductRepository(firebaseOptions(fetcher, {
      getIdToken: async () => null,
    }));
    await expect(authFailure.getSnapshot()).rejects.toMatchObject({ code: 'AUTH_REQUIRED' });

    const appCheckFailure = new FirebaseProductRepository(firebaseOptions(fetcher, {
      getToken: async () => null,
    }));
    await expect(appCheckFailure.getSnapshot()).rejects.toMatchObject({ code: 'APP_CHECK_REQUIRED' });
  });

  it('fails closed for a missing projection after a successful command', async () => {
    const repository = new FirebaseProductRepository(firebaseOptions(async (_url, init) => init.method === 'POST'
      ? jsonResponse(201, { commandType: 'create_repair_request', resourceId: 'repair-created-2', eventId: 'event-2', tenantId: 'tenant-1' })
      : jsonResponse(200, { ...demoProductSnapshot, repairRequest: null })));

    await expect(repository.createRepairRequest({ title: '수리 요청' })).rejects.toMatchObject({
      code: 'PROJECTION_PENDING',
    });
  });

  it('maps rejected API responses to a typed error without exposing tokens', async () => {
    const repository = new FirebaseProductRepository(firebaseOptions(async () => jsonResponse(403, {
      error: { code: 'RESOURCE_FORBIDDEN', message: '권한이 없습니다.' },
    })));

    await expect(repository.getSnapshot()).rejects.toMatchObject({
      code: 'HTTP_ERROR',
      status: 403,
      message: '권한이 없습니다.',
    });
    await expect(repository.getSnapshot()).rejects.not.toThrow('id-token');
  });
});

function jsonResponse(status: number, body: unknown): ProductHttpResponse {
  return { status, ok: status >= 200 && status < 300, json: async () => body };
}

function firebaseOptions(
  fetch: FirebaseProductRepositoryOptions['fetch'],
  overrides: Partial<FirebaseProductRepositoryOptions['auth'] & FirebaseProductRepositoryOptions['appCheck']> = {},
): FirebaseProductRepositoryOptions {
  return {
    baseUrl: 'https://example.test',
    tenantId: 'tenant-1',
    beneficiaryId: 'person-1',
    deviceId: 'device-1',
    defaultPublicFundingInvolved: true,
    createIdempotencyKey: () => 'generated-repair-key-1',
    auth: { getIdToken: async () => 'id-token', ...(overrides as Partial<FirebaseProductRepositoryOptions['auth']>) },
    appCheck: { getToken: async () => 'app-check-token', ...(overrides as Partial<FirebaseProductRepositoryOptions['appCheck']>) },
    fetch,
  };
}
