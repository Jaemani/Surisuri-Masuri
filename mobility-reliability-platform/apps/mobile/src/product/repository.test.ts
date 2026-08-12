import { describe, expect, it } from 'vitest';

import { DemoProductRepository, FirebaseProductRepository } from './repository';

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
  it('fails closed with NOT_CONFIGURED until domain-command endpoints exist', async () => {
    const repository = new FirebaseProductRepository();

    await expect(repository.getSnapshot()).rejects.toMatchObject({ code: 'NOT_CONFIGURED' });
    await expect(repository.createRepairRequest({ title: '수리 요청' })).rejects.toMatchObject({ code: 'NOT_CONFIGURED' });
    await expect(repository.setRole('repairer')).rejects.toMatchObject({ code: 'NOT_CONFIGURED' });
  });
});
