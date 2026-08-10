import {
  correlateUploadLeaseResultCore,
  leaseNextUploadBatchCore,
  type UploadLeaseDependencies,
  type UploadLeaseReadDatabase,
  type UploadLeaseResult,
  type UploadLeaseTransaction,
} from './uploadLease';

export type ManagedUploadLeaseConnection = UploadLeaseTransaction &
  UploadLeaseReadDatabase & {
    execAsync(source: string): Promise<void>;
    closeAsync(): Promise<void>;
  };

export type UploadLeasePersistenceResult = {
  result: UploadLeaseResult;
  operationalWarnings?: Array<'writer_close_failed' | 'reader_close_failed'>;
};

export type UploadLeasePersistenceErrorCode =
  | 'UPLOAD_LEASE_COMMIT_UNVERIFIABLE'
  | 'UPLOAD_LEASE_ROLLBACK_FAILED';

export class UploadLeasePersistenceError extends Error {
  readonly name = 'UploadLeasePersistenceError';

  constructor(
    readonly code: UploadLeasePersistenceErrorCode,
    readonly causeValue?: unknown,
  ) {
    super(code);
  }
}

export type UploadLeaseCoordinatorDependencies = {
  openWriter(): Promise<ManagedUploadLeaseConnection>;
  openReader(): Promise<ManagedUploadLeaseConnection>;
  leaseDependencies: UploadLeaseDependencies;
  leaseCore?: typeof leaseNextUploadBatchCore;
  correlateCore?: typeof correlateUploadLeaseResultCore;
};

async function configureWriter(writer: ManagedUploadLeaseConnection): Promise<void> {
  await writer.execAsync('PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 5000;');
  const foreignKeys = await writer.getFirstAsync<{ foreign_keys: number }>(
    'PRAGMA foreign_keys',
  );
  if (foreignKeys?.foreign_keys !== 1) {
    throw new Error('UPLOAD_DATABASE_FOREIGN_KEYS_DISABLED');
  }
}

async function correlateOnFreshReadConnection(
  dependencies: UploadLeaseCoordinatorDependencies,
  result: Exclude<UploadLeaseResult, { kind: 'none' }>,
): Promise<{ committed: boolean; readerCloseFailed: boolean }> {
  const reader = await dependencies.openReader();
  let readerCloseFailed = false;
  let committed: boolean | undefined;
  try {
    await reader.execAsync(
      'PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 5000; PRAGMA query_only = ON;',
    );
    const foreignKeys = await reader.getFirstAsync<{ foreign_keys: number }>(
      'PRAGMA foreign_keys',
    );
    const queryOnly = await reader.getFirstAsync<{ query_only: number }>(
      'PRAGMA query_only',
    );
    if (foreignKeys?.foreign_keys !== 1 || queryOnly?.query_only !== 1) {
      throw new Error('UPLOAD_LEASE_CORRELATION_DATABASE_NOT_READ_ONLY');
    }
    const correlation = await (
      dependencies.correlateCore ?? correlateUploadLeaseResultCore
    )(reader, result);
    committed = correlation.kind === 'committed';
  } finally {
    await reader.closeAsync().catch(() => {
      readerCloseFailed = true;
    });
  }
  if (committed === undefined) {
    throw new Error('UPLOAD_LEASE_CORRELATION_MISSING');
  }
  return { committed, readerCloseFailed };
}

export async function leaseNextUploadBatchWithRecovery(
  dependencies: UploadLeaseCoordinatorDependencies,
): Promise<UploadLeasePersistenceResult> {
  const writer = await dependencies.openWriter();
  let transactionMayBeOpen = false;
  let beginResolved = false;
  let writerCloseResult: boolean | undefined;

  const closeWriter = async (): Promise<boolean> => {
    if (writerCloseResult !== undefined) return writerCloseResult;
    try {
      await writer.closeAsync();
      writerCloseResult = true;
    } catch {
      writerCloseResult = false;
    }
    return writerCloseResult;
  };

  try {
    await configureWriter(writer);
    transactionMayBeOpen = true;
    await writer.execAsync('BEGIN IMMEDIATE;');
    beginResolved = true;

    let result: UploadLeaseResult;
    try {
      result = await (dependencies.leaseCore ?? leaseNextUploadBatchCore)(
        { withExclusiveTransactionAsync: async (task) => task(writer) },
        dependencies.leaseDependencies,
      );
    } catch (error) {
      try {
        await writer.execAsync('ROLLBACK;');
        transactionMayBeOpen = false;
      } catch (rollbackError) {
        transactionMayBeOpen = false;
        await closeWriter();
        throw new UploadLeasePersistenceError(
          'UPLOAD_LEASE_ROLLBACK_FAILED',
          rollbackError,
        );
      }
      await closeWriter();
      throw error;
    }

    try {
      await writer.execAsync('COMMIT;');
      transactionMayBeOpen = false;
    } catch (commitError) {
      // A rejected COMMIT does not prove whether native SQLite committed.
      // Never roll back after this point; close and inspect a fresh snapshot.
      transactionMayBeOpen = false;
      const writerCloseSucceeded = await closeWriter();
      const operationalWarnings: NonNullable<
        UploadLeasePersistenceResult['operationalWarnings']
      > = [];
      if (!writerCloseSucceeded) operationalWarnings.push('writer_close_failed');

      // `none` is a write-free result by contract, so no mutation needs correlation.
      if (result.kind === 'none') {
        return {
          result,
          ...(operationalWarnings.length > 0 ? { operationalWarnings } : {}),
        };
      }

      let freshRead: Awaited<ReturnType<typeof correlateOnFreshReadConnection>>;
      try {
        freshRead = await correlateOnFreshReadConnection(dependencies, result);
      } catch (correlationError) {
        throw new UploadLeasePersistenceError(
          'UPLOAD_LEASE_COMMIT_UNVERIFIABLE',
          correlationError,
        );
      }
      if (!freshRead.committed) {
        throw new UploadLeasePersistenceError(
          'UPLOAD_LEASE_COMMIT_UNVERIFIABLE',
          commitError,
        );
      }
      if (freshRead.readerCloseFailed) operationalWarnings.push('reader_close_failed');
      return {
        result,
        ...(operationalWarnings.length > 0 ? { operationalWarnings } : {}),
      };
    }

    const writerCloseSucceeded = await closeWriter();
    return {
      result,
      ...(!writerCloseSucceeded
        ? { operationalWarnings: ['writer_close_failed' as const] }
        : {}),
    };
  } catch (error) {
    if (transactionMayBeOpen) {
      try {
        await writer.execAsync('ROLLBACK;');
        transactionMayBeOpen = false;
      } catch (rollbackError) {
        transactionMayBeOpen = false;
        const writerCloseSucceeded = await closeWriter();
        if (beginResolved || !writerCloseSucceeded) {
          throw new UploadLeasePersistenceError(
            'UPLOAD_LEASE_ROLLBACK_FAILED',
            rollbackError,
          );
        }
      }
    }
    await closeWriter();
    throw error;
  }
}
