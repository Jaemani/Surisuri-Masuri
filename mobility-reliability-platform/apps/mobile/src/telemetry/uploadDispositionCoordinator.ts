import {
  applyUploadDispositionCore,
  correlateUploadDispositionCore,
  type UploadDispositionCommand,
  type UploadDispositionCorrelation,
  type UploadDispositionReadDatabase,
  type UploadDispositionTransaction,
} from './uploadDisposition';

export type ManagedUploadDispositionConnection = UploadDispositionTransaction &
  UploadDispositionReadDatabase & {
    execAsync(source: string): Promise<void>;
    closeAsync(): Promise<void>;
  };

export type UploadDispositionPersistenceResult = {
  kind: 'committed';
  operationalWarnings?: Array<'writer_close_failed' | 'reader_close_failed'>;
};

export type UploadDispositionPersistenceErrorCode =
  | 'UPLOAD_DISPOSITION_NOT_COMMITTED'
  | 'UPLOAD_DISPOSITION_COMMIT_UNVERIFIABLE'
  | 'UPLOAD_DISPOSITION_ROLLBACK_FAILED';

export class UploadDispositionPersistenceError extends Error {
  readonly name = 'UploadDispositionPersistenceError';

  constructor(
    readonly code: UploadDispositionPersistenceErrorCode,
    readonly causeValue?: unknown,
  ) {
    super(code);
  }
}

export type UploadDispositionCoordinatorDependencies = {
  openWriter(): Promise<ManagedUploadDispositionConnection>;
  openReader(): Promise<ManagedUploadDispositionConnection>;
  applyCore?: typeof applyUploadDispositionCore;
  correlateCore?: typeof correlateUploadDispositionCore;
};

async function configureWriter(
  writer: ManagedUploadDispositionConnection,
): Promise<void> {
  await writer.execAsync('PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 5000;');
  const foreignKeys = await writer.getFirstAsync<{ foreign_keys: number }>(
    'PRAGMA foreign_keys',
  );
  if (foreignKeys?.foreign_keys !== 1) {
    throw new Error('UPLOAD_DATABASE_FOREIGN_KEYS_DISABLED');
  }
}

async function correlateOnFreshReadConnection(
  dependencies: UploadDispositionCoordinatorDependencies,
  command: UploadDispositionCommand,
): Promise<{
  correlation: UploadDispositionCorrelation;
  readerCloseFailed: boolean;
}> {
  const reader = await dependencies.openReader();
  let readerCloseFailed = false;
  let correlation: UploadDispositionCorrelation | undefined;
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
      throw new Error('UPLOAD_CORRELATION_DATABASE_NOT_READ_ONLY');
    }
    correlation = await (
      dependencies.correlateCore ?? correlateUploadDispositionCore
    )(reader, command);
  } finally {
    await reader.closeAsync().catch(() => {
      readerCloseFailed = true;
    });
  }
  if (!correlation) {
    throw new Error('UPLOAD_DISPOSITION_CORRELATION_MISSING');
  }
  return { correlation, readerCloseFailed };
}

export async function applyUploadDispositionWithRecovery(
  dependencies: UploadDispositionCoordinatorDependencies,
  command: UploadDispositionCommand,
): Promise<UploadDispositionPersistenceResult> {
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
    try {
      await (dependencies.applyCore ?? applyUploadDispositionCore)(
        {
          withExclusiveTransactionAsync: async (task) => task(writer),
        },
        command,
      );
    } catch (error) {
      try {
        await writer.execAsync('ROLLBACK;');
        transactionMayBeOpen = false;
      } catch (rollbackError) {
        transactionMayBeOpen = false;
        await closeWriter();
        throw new UploadDispositionPersistenceError(
          'UPLOAD_DISPOSITION_ROLLBACK_FAILED',
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
      // A rejected COMMIT promise does not prove whether native SQLite committed.
      // Never issue ROLLBACK after this point; close and correlate on a fresh snapshot.
      transactionMayBeOpen = false;
      const writerCloseSucceeded = await closeWriter();
      let freshRead: Awaited<ReturnType<typeof correlateOnFreshReadConnection>>;
      try {
        freshRead = await correlateOnFreshReadConnection(dependencies, command);
      } catch (correlationError) {
        throw new UploadDispositionPersistenceError(
          'UPLOAD_DISPOSITION_COMMIT_UNVERIFIABLE',
          correlationError,
        );
      }

      if (freshRead.correlation.kind === 'committed') {
        const operationalWarnings: NonNullable<
          UploadDispositionPersistenceResult['operationalWarnings']
        > = [];
        if (!writerCloseSucceeded) operationalWarnings.push('writer_close_failed');
        if (freshRead.readerCloseFailed) operationalWarnings.push('reader_close_failed');
        return {
          kind: 'committed',
          ...(operationalWarnings.length > 0 ? { operationalWarnings } : {}),
        };
      }
      if (freshRead.correlation.kind === 'not_committed' && writerCloseSucceeded) {
        throw new UploadDispositionPersistenceError(
          'UPLOAD_DISPOSITION_NOT_COMMITTED',
          commitError,
        );
      }
      throw new UploadDispositionPersistenceError(
        'UPLOAD_DISPOSITION_COMMIT_UNVERIFIABLE',
        commitError,
      );
    }

    const writerCloseSucceeded = await closeWriter();
    return {
      kind: 'committed',
      ...(!writerCloseSucceeded
        ? { operationalWarnings: ['writer_close_failed' as const] }
        : {}),
    };
  } catch (error) {
    if (transactionMayBeOpen) {
      // BEGIN IMMEDIATE can reject after SQLite has opened the transaction.
      // Keep the marker set until the compensating ROLLBACK succeeds so a
      // rejected BEGIN is handled exactly like any other uncertain write.
      try {
        await writer.execAsync('ROLLBACK;');
        transactionMayBeOpen = false;
      } catch (rollbackError) {
        // A failed rollback does not tell us whether SQLite kept the
        // transaction open. Closing the connection is the only remaining
        // lifecycle boundary. If BEGIN never resolved and close succeeds,
        // returning the original BEGIN error preserves the caller's retry
        // semantics; otherwise persistence is explicitly unverifiable.
        transactionMayBeOpen = false;
        const writerCloseSucceeded = await closeWriter();
        if (beginResolved || !writerCloseSucceeded) {
          throw new UploadDispositionPersistenceError(
            'UPLOAD_DISPOSITION_ROLLBACK_FAILED',
            rollbackError,
          );
        }
      }
    }
    await closeWriter();
    throw error;
  }
}
