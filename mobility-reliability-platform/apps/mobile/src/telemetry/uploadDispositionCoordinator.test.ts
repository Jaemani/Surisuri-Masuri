import { describe, expect, it } from 'vitest';

import type { UploadDispositionCommand } from './uploadDisposition';
import {
  applyUploadDispositionWithRecovery,
  UploadDispositionPersistenceError,
  type ManagedUploadDispositionConnection,
} from './uploadDispositionCoordinator';

const command = {
  authority: {
    clientBatchId: '80000000-0000-4000-8000-000000000001',
    sessionId: '80000000-0000-4000-8000-000000000002',
    sampleCount: 1,
    attemptCount: 1,
    leaseOwnerId: '80000000-0000-4000-8000-000000000003',
    leaseExpiresAt: '2026-07-23T08:02:00.000Z',
    bodySha256: 'a'.repeat(64),
  },
  disposition: { kind: 'retry', code: 'network_failure' },
  observedAt: '2026-07-23T08:01:00.000Z',
  retryAt: '2026-07-23T08:01:05.000Z',
} satisfies UploadDispositionCommand;

type Script = {
  begin?: 'resolve' | 'reject_after_begin' | 'reject_before_begin';
  commit?: 'resolve' | 'reject_after_commit' | 'reject_before_commit';
  close?: 'resolve' | 'reject';
  readerClose?: 'resolve' | 'reject';
  correlation?: 'committed' | 'not_committed' | 'unverifiable' | 'throw';
  apply?: 'resolve' | 'reject';
  rollback?: 'resolve' | 'reject';
  foreignKeys?: 0 | 1;
  queryOnly?: 0 | 1;
};

function harness(script: Script = {}) {
  const log: string[] = [];
  let committed = false;

  const connection = (
    role: 'writer' | 'reader',
  ): ManagedUploadDispositionConnection => ({
    execAsync: async (source) => {
      if (source.startsWith('PRAGMA')) log.push(`${role}:configure`);
      else log.push(`${role}:${source.replace(';', '').toLowerCase()}`);
      if (source === 'BEGIN IMMEDIATE;') {
        if (script.begin === 'reject_before_begin') {
          throw new Error('begin rejected before open');
        }
        if (script.begin === 'reject_after_begin') {
          throw new Error('begin response lost');
        }
      }
      if (source === 'COMMIT;') {
        if (script.commit === 'reject_before_commit') {
          throw new Error('commit rejected before apply');
        }
        committed = true;
        if (script.commit === 'reject_after_commit') {
          throw new Error('commit response lost');
        }
      }
      if (source === 'ROLLBACK;' && script.rollback === 'reject') {
        throw new Error('rollback failed');
      }
    },
    closeAsync: async () => {
      log.push(`${role}:close`);
      const behavior = role === 'writer' ? script.close : script.readerClose;
      if (behavior === 'reject') throw new Error(`${role} close failed`);
    },
    getFirstAsync: async <T,>(source: string) => {
      log.push(`${role}:read:${source.replace('PRAGMA ', '').toLowerCase()}`);
      if (source === 'PRAGMA foreign_keys') {
        return { foreign_keys: script.foreignKeys ?? 1 } as T;
      }
      if (source === 'PRAGMA query_only') {
        return { query_only: script.queryOnly ?? 1 } as T;
      }
      return null;
    },
    runAsync: async () => ({ changes: 1 }),
  });

  return {
    log,
    dependencies: {
      openWriter: async () => {
        log.push('writer:open');
        return connection('writer');
      },
      openReader: async () => {
        log.push('reader:open');
        return connection('reader');
      },
      applyCore: async () => {
        log.push('core:apply');
        if (script.apply === 'reject') throw new Error('core rejected');
      },
      correlateCore: async () => {
        log.push('core:correlate');
        if (script.correlation === 'throw') throw new Error('read failed');
        return { kind: script.correlation ?? (committed ? 'committed' : 'not_committed') } as const;
      },
    },
  };
}

describe('upload disposition connection coordinator', () => {
  it('commits once and never opens a correlation reader on the normal path', async () => {
    const state = harness();
    await expect(
      applyUploadDispositionWithRecovery(state.dependencies, command),
    ).resolves.toEqual({ kind: 'committed' });
    expect(state.log).toEqual([
      'writer:open',
      'writer:configure',
      'writer:read:foreign_keys',
      'writer:begin immediate',
      'core:apply',
      'writer:commit',
      'writer:close',
    ]);
  });

  it('recognizes commit-response loss on a fresh read-only connection', async () => {
    const state = harness({ commit: 'reject_after_commit' });
    await expect(
      applyUploadDispositionWithRecovery(state.dependencies, command),
    ).resolves.toEqual({ kind: 'committed' });
    expect(state.log).toEqual([
      'writer:open',
      'writer:configure',
      'writer:read:foreign_keys',
      'writer:begin immediate',
      'core:apply',
      'writer:commit',
      'writer:close',
      'reader:open',
      'reader:configure',
      'reader:read:foreign_keys',
      'reader:read:query_only',
      'core:correlate',
      'reader:close',
    ]);
    expect(state.log).not.toContain('writer:rollback');
  });

  it('classifies a proven pre-commit failure for local-only reapplication', async () => {
    const state = harness({ commit: 'reject_before_commit' });
    await expect(
      applyUploadDispositionWithRecovery(state.dependencies, command),
    ).rejects.toMatchObject({ code: 'UPLOAD_DISPOSITION_NOT_COMMITTED' });
    expect(state.log).not.toContain('writer:rollback');
  });

  it.each(['unverifiable', 'throw'] as const)(
    'fails closed when commit correlation is %s',
    async (correlation) => {
      const state = harness({
        commit: 'reject_after_commit',
        correlation,
      });
      await expect(
        applyUploadDispositionWithRecovery(state.dependencies, command),
      ).rejects.toMatchObject({ code: 'UPLOAD_DISPOSITION_COMMIT_UNVERIFIABLE' });
    },
  );

  it('does not trust not-committed correlation when writer close failed', async () => {
    const state = harness({
      commit: 'reject_before_commit',
      close: 'reject',
      correlation: 'not_committed',
    });
    await expect(
      applyUploadDispositionWithRecovery(state.dependencies, command),
    ).rejects.toMatchObject({ code: 'UPLOAD_DISPOSITION_COMMIT_UNVERIFIABLE' });
  });

  it('rolls back a core failure without opening a reader', async () => {
    const state = harness({ apply: 'reject' });
    await expect(
      applyUploadDispositionWithRecovery(state.dependencies, command),
    ).rejects.toThrow('core rejected');
    expect(state.log).toContain('writer:rollback');
    expect(state.log).not.toContain('reader:open');
  });

  it('attempts rollback when the BEGIN response is lost', async () => {
    const state = harness({ begin: 'reject_after_begin' });

    await expect(
      applyUploadDispositionWithRecovery(state.dependencies, command),
    ).rejects.toThrow('begin response lost');
    expect(state.log).toEqual([
      'writer:open',
      'writer:configure',
      'writer:read:foreign_keys',
      'writer:begin immediate',
      'writer:rollback',
      'writer:close',
    ]);
    expect(state.log).not.toContain('core:apply');
  });

  it('returns the original BEGIN error when rollback fails but close succeeds', async () => {
    const state = harness({
      begin: 'reject_after_begin',
      rollback: 'reject',
    });

    await expect(
      applyUploadDispositionWithRecovery(state.dependencies, command),
    ).rejects.toThrow('begin response lost');
    expect(state.log).toEqual([
      'writer:open',
      'writer:configure',
      'writer:read:foreign_keys',
      'writer:begin immediate',
      'writer:rollback',
      'writer:close',
    ]);
    expect(state.log).not.toContain('core:apply');
  });

  it('fails closed when BEGIN rollback and writer close both fail', async () => {
    const state = harness({
      begin: 'reject_after_begin',
      rollback: 'reject',
      close: 'reject',
    });

    await expect(
      applyUploadDispositionWithRecovery(state.dependencies, command),
    ).rejects.toMatchObject({ code: 'UPLOAD_DISPOSITION_ROLLBACK_FAILED' });
    expect(state.log).toEqual([
      'writer:open',
      'writer:configure',
      'writer:read:foreign_keys',
      'writer:begin immediate',
      'writer:rollback',
      'writer:close',
    ]);
    expect(state.log).not.toContain('core:apply');
  });

  it('rejects a writer that cannot prove foreign-key enforcement', async () => {
    const state = harness({ foreignKeys: 0 });
    await expect(
      applyUploadDispositionWithRecovery(state.dependencies, command),
    ).rejects.toThrow('UPLOAD_DATABASE_FOREIGN_KEYS_DISABLED');
    expect(state.log).not.toContain('writer:begin immediate');
  });

  it('fails closed when the fresh correlation connection is not read-only', async () => {
    const state = harness({
      commit: 'reject_after_commit',
      queryOnly: 0,
    });
    await expect(
      applyUploadDispositionWithRecovery(state.dependencies, command),
    ).rejects.toMatchObject({ code: 'UPLOAD_DISPOSITION_COMMIT_UNVERIFIABLE' });
    expect(state.log).not.toContain('core:correlate');
  });

  it('surfaces rollback failure as a fail-closed persistence error', async () => {
    const state = harness({ apply: 'reject', rollback: 'reject' });
    await expect(
      applyUploadDispositionWithRecovery(state.dependencies, command),
    ).rejects.toBeInstanceOf(UploadDispositionPersistenceError);
    const repeatedState = harness({ apply: 'reject', rollback: 'reject' });
    await expect(
      applyUploadDispositionWithRecovery(repeatedState.dependencies, command),
    ).rejects.toMatchObject({ code: 'UPLOAD_DISPOSITION_ROLLBACK_FAILED' });
  });

  it('keeps a successful commit successful when only writer close fails', async () => {
    const state = harness({ close: 'reject' });
    await expect(
      applyUploadDispositionWithRecovery(state.dependencies, command),
    ).resolves.toEqual({
      kind: 'committed',
      operationalWarnings: ['writer_close_failed'],
    });
  });

  it('reports a reader-close warning after exact durable correlation', async () => {
    const state = harness({
      commit: 'reject_after_commit',
      readerClose: 'reject',
    });
    await expect(
      applyUploadDispositionWithRecovery(state.dependencies, command),
    ).resolves.toEqual({
      kind: 'committed',
      operationalWarnings: ['reader_close_failed'],
    });
  });

  it('preserves both close warnings after exact durable correlation', async () => {
    const state = harness({
      commit: 'reject_after_commit',
      close: 'reject',
      readerClose: 'reject',
    });
    await expect(
      applyUploadDispositionWithRecovery(state.dependencies, command),
    ).resolves.toEqual({
      kind: 'committed',
      operationalWarnings: ['writer_close_failed', 'reader_close_failed'],
    });
  });
});
