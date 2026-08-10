import { describe, expect, it } from 'vitest';

import type { UploadLeaseResult } from './uploadLease';
import {
  leaseNextUploadBatchWithRecovery,
  type ManagedUploadLeaseConnection,
} from './uploadLeaseCoordinator';

const leasedResult = {
  kind: 'leased',
  lease: {
    clientBatchId: '81000000-0000-4000-8000-000000000001',
    sessionId: '81000000-0000-4000-8000-000000000002',
    sampleCount: 1,
    attemptCount: 1,
    leaseOwnerId: '81000000-0000-4000-8000-000000000003',
    leaseExpiresAt: '2026-08-11T00:02:00.000Z',
    body: '{}',
    bodySha256: 'a'.repeat(64),
  },
} satisfies UploadLeaseResult;

type Script = {
  result?: UploadLeaseResult;
  begin?: 'resolve' | 'reject';
  commit?: 'resolve' | 'reject';
  rollback?: 'resolve' | 'reject';
  writerClose?: 'resolve' | 'reject';
  readerClose?: 'resolve' | 'reject';
  correlation?: 'committed' | 'unverifiable' | 'throw';
  queryOnly?: 0 | 1;
};

function harness(script: Script = {}) {
  const log: string[] = [];
  const connection = (role: 'writer' | 'reader'): ManagedUploadLeaseConnection => ({
    execAsync: async (source) => {
      if (source.startsWith('PRAGMA')) log.push(`${role}:configure`);
      else log.push(`${role}:${source.replace(';', '').toLowerCase()}`);
      if (source === 'BEGIN IMMEDIATE;' && script.begin === 'reject') {
        throw new Error('BEGIN_RESPONSE_LOST');
      }
      if (source === 'COMMIT;' && script.commit === 'reject') {
        throw new Error('COMMIT_RESPONSE_LOST');
      }
      if (source === 'ROLLBACK;' && script.rollback === 'reject') {
        throw new Error('ROLLBACK_RESPONSE_LOST');
      }
    },
    closeAsync: async () => {
      log.push(`${role}:close`);
      if (
        (role === 'writer' && script.writerClose === 'reject') ||
        (role === 'reader' && script.readerClose === 'reject')
      ) {
        throw new Error(`${role.toUpperCase()}_CLOSE_FAILED`);
      }
    },
    getFirstAsync: async <T,>(source: string) => {
      if (source === 'PRAGMA foreign_keys') return { foreign_keys: 1 } as T;
      if (source === 'PRAGMA query_only') {
        return { query_only: script.queryOnly ?? 1 } as T;
      }
      return null;
    },
    getAllAsync: async <T,>() => [] as T[],
    runAsync: async () => ({ changes: 1 }),
  });

  return {
    log,
    dependencies: {
      openWriter: async () => connection('writer'),
      openReader: async () => connection('reader'),
      leaseDependencies: {
        createLeaseOwnerId: () => leasedResult.lease.leaseOwnerId,
        leaseExpiresAt: () => leasedResult.lease.leaseExpiresAt,
        now: () => '2026-08-11T00:00:00.000Z',
        sha256: async () => leasedResult.lease.bodySha256,
      },
      leaseCore: async () => script.result ?? leasedResult,
      correlateCore: async () => {
        if (script.correlation === 'throw') throw new Error('CORRELATION_FAILED');
        return { kind: script.correlation ?? 'committed' } as const;
      },
    },
  };
}

describe('upload lease connection coordinator', () => {
  it('commits a lease once on the normal path', async () => {
    const state = harness();
    await expect(leaseNextUploadBatchWithRecovery(state.dependencies)).resolves.toEqual({
      result: leasedResult,
    });
    expect(state.log).not.toContain('reader:configure');
  });

  it('correlates a committed lease on a fresh read-only connection', async () => {
    const state = harness({ commit: 'reject' });
    await expect(leaseNextUploadBatchWithRecovery(state.dependencies)).resolves.toEqual({
      result: leasedResult,
    });
    expect(state.log).not.toContain('writer:rollback');
    expect(state.log).toContain('reader:configure');
  });

  it('fails closed when a commit cannot be correlated', async () => {
    const state = harness({ commit: 'reject', correlation: 'unverifiable' });
    await expect(
      leaseNextUploadBatchWithRecovery(state.dependencies),
    ).rejects.toMatchObject({ code: 'UPLOAD_LEASE_COMMIT_UNVERIFIABLE' });
    expect(state.log).not.toContain('writer:rollback');
  });

  it('returns a write-free none result without opening a reader after commit loss', async () => {
    const none = { kind: 'none' } as const;
    const state = harness({ commit: 'reject', result: none });
    await expect(leaseNextUploadBatchWithRecovery(state.dependencies)).resolves.toEqual({
      result: none,
    });
    expect(state.log).not.toContain('reader:configure');
  });

  it('rolls back when the BEGIN response is lost', async () => {
    const state = harness({ begin: 'reject' });
    await expect(leaseNextUploadBatchWithRecovery(state.dependencies)).rejects.toThrow(
      'BEGIN_RESPONSE_LOST',
    );
    expect(state.log).toContain('writer:rollback');
  });

  it('fails closed when BEGIN rollback and close are both unverifiable', async () => {
    const state = harness({
      begin: 'reject',
      rollback: 'reject',
      writerClose: 'reject',
    });
    await expect(
      leaseNextUploadBatchWithRecovery(state.dependencies),
    ).rejects.toMatchObject({ code: 'UPLOAD_LEASE_ROLLBACK_FAILED' });
  });

  it('preserves writer and reader close warnings after durable correlation', async () => {
    const state = harness({
      commit: 'reject',
      writerClose: 'reject',
      readerClose: 'reject',
    });
    await expect(leaseNextUploadBatchWithRecovery(state.dependencies)).resolves.toEqual({
      result: leasedResult,
      operationalWarnings: ['writer_close_failed', 'reader_close_failed'],
    });
  });

  it('rejects a correlation connection that is not read-only', async () => {
    const state = harness({ commit: 'reject', queryOnly: 0 });
    await expect(
      leaseNextUploadBatchWithRecovery(state.dependencies),
    ).rejects.toMatchObject({ code: 'UPLOAD_LEASE_COMMIT_UNVERIFIABLE' });
  });
});
