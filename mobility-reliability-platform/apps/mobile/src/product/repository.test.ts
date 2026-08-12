import { describe, expect, it } from 'vitest';

import { demoProductSnapshot, DemoProductRepository, FirebaseProductRepository, ProductRepositoryError } from './repository';
import type { FirebaseProductRepositoryOptions, ProductHttpResponse } from './repository';

describe('DemoProductRepository', () => {
  it('returns deterministic product data without sharing mutable arrays', async () => {
    const repository = new DemoProductRepository();
    const first = await repository.getSnapshot();
    first.device.timeline.pop();
    first.repairJobs[0].customer = '변경된 이름';

    const second = await repository.getSnapshot();
    expect(second.device.timeline).toHaveLength(3);
    expect(second.repairJobs[0].customer).toBe('김정자 님');
    expect(second.roleSession).toEqual({ role: 'user', displayName: '김정자 님', isDemo: true });
  });

  it('applies async repair request and role commands to the snapshot', async () => {
    const repository = new DemoProductRepository();

    const request = await repository.createRepairRequest({ title: '브레이크가 뻑뻑해요' });
    expect(request.status).toBe('received');
    expect((await repository.getSnapshot()).repairRequest).toMatchObject({
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
    expect(requests[0].url).toContain('tenantId=tenant-1');
    expect(requests[0].init.headers.Authorization).toBe('Bearer id-token');
    expect(requests[0].init.headers['X-Firebase-AppCheck']).toBe('app-check-token');
    expect(requests[0].init.method).toBe('GET');
    expect(requests[0].init.body).toBeUndefined();
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
