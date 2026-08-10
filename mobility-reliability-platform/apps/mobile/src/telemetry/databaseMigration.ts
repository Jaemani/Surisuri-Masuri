import {
  CREATE_TELEMETRY_SCHEMA_V4_SQL,
  CURRENT_TELEMETRY_SCHEMA_VERSION,
  FIND_INVALID_TERMINAL_OUTBOX_DELIVERY_SQL,
  FIND_INVALID_TELEMETRY_UPLOAD_BINDING_SQL,
  FIND_INVALID_TELEMETRY_UPLOAD_BATCH_SQL,
  MIGRATE_TELEMETRY_V1_TO_V2_SQL,
  MIGRATE_TELEMETRY_V2_TO_V3_SQL,
  MIGRATE_TELEMETRY_V3_TO_V4_SQL,
} from './databaseSchema';

type SqlValue = string | number | null;

export type TelemetryMigrationDatabase = {
  execAsync(source: string): Promise<void>;
  getFirstAsync<T>(source: string, ...params: SqlValue[]): Promise<T | null>;
  getAllAsync<T>(source: string, ...params: SqlValue[]): Promise<T[]>;
};

async function requireNoInvalidUploadState(
  database: TelemetryMigrationDatabase,
): Promise<void> {
  const invalidBatch = await database.getFirstAsync<{ invalid: number }>(
    FIND_INVALID_TELEMETRY_UPLOAD_BATCH_SQL,
  );
  if (invalidBatch) {
    throw new Error('DATABASE_UPLOAD_BATCH_BODY_INVALID');
  }
  const invalidBinding = await database.getFirstAsync<{ invalid: number }>(
    FIND_INVALID_TELEMETRY_UPLOAD_BINDING_SQL,
  );
  if (invalidBinding) {
    throw new Error('DATABASE_UPLOAD_BATCH_BINDING_INVALID');
  }
  const invalidTerminalDelivery = await database.getFirstAsync<{ invalid: number }>(
    FIND_INVALID_TERMINAL_OUTBOX_DELIVERY_SQL,
  );
  if (invalidTerminalDelivery) {
    throw new Error('DATABASE_TERMINAL_OUTBOX_DELIVERY_INVALID');
  }
}

async function applyMigrationFromVersion(
  database: TelemetryMigrationDatabase,
  schemaVersion: number,
): Promise<void> {
  if (schemaVersion > CURRENT_TELEMETRY_SCHEMA_VERSION) {
    throw new Error('DATABASE_SCHEMA_NEWER_THAN_APP');
  }
  if (schemaVersion === CURRENT_TELEMETRY_SCHEMA_VERSION) return;

  if (schemaVersion === 0) {
    const existingV0Table = await database.getFirstAsync<{ name: string }>(
      `SELECT name FROM sqlite_master
       WHERE type = 'table' AND name = 'trip_session_projection'`,
    );
    if (existingV0Table) {
      throw new Error('UNVERSIONED_DEVELOPMENT_DATABASE_REQUIRES_CLEAR');
    }
    await database.execAsync(CREATE_TELEMETRY_SCHEMA_V4_SQL);
    return;
  }

  if (schemaVersion !== 1 && schemaVersion !== 2 && schemaVersion !== 3) {
    throw new Error('DATABASE_SCHEMA_MIGRATION_UNAVAILABLE');
  }
  if (schemaVersion === 2 || schemaVersion === 3) {
    await requireNoInvalidUploadState(database);
  }
  if (schemaVersion === 1) {
    await database.execAsync(MIGRATE_TELEMETRY_V1_TO_V2_SQL);
  }
  if (schemaVersion === 1 || schemaVersion === 2) {
    await database.execAsync(MIGRATE_TELEMETRY_V2_TO_V3_SQL);
  }
  await database.execAsync(MIGRATE_TELEMETRY_V3_TO_V4_SQL);
}

export async function migrateTelemetryDatabaseCore(
  database: TelemetryMigrationDatabase,
): Promise<void> {
  await database.execAsync('PRAGMA busy_timeout = 5000; PRAGMA journal_mode = WAL;');
  await database.execAsync('PRAGMA foreign_keys = OFF;');
  let transactionMayBeOpen = false;
  let primaryError: unknown;
  let rollbackError: unknown;
  let foreignKeyRestoreError: unknown;

  try {
    const disabledForeignKeys = await database.getFirstAsync<{ foreign_keys: number }>(
      'PRAGMA foreign_keys',
    );
    if (disabledForeignKeys?.foreign_keys !== 0) {
      throw new Error('DATABASE_FOREIGN_KEYS_DISABLE_FAILED');
    }

    transactionMayBeOpen = true;
    await database.execAsync('BEGIN IMMEDIATE;');
    const versionRow = await database.getFirstAsync<{ user_version: number }>(
      'PRAGMA user_version',
    );
    const schemaVersion = versionRow?.user_version ?? 0;
    await applyMigrationFromVersion(database, schemaVersion);
    await requireNoInvalidUploadState(database);

    const foreignKeyViolations = await database.getAllAsync('PRAGMA foreign_key_check');
    if (foreignKeyViolations.length > 0) {
      throw new Error('DATABASE_FOREIGN_KEY_CHECK_FAILED');
    }
    await database.execAsync('COMMIT;');
    transactionMayBeOpen = false;
  } catch (error) {
    primaryError = error;
    if (transactionMayBeOpen) {
      try {
        await database.execAsync('ROLLBACK;');
        transactionMayBeOpen = false;
      } catch (errorDuringRollback) {
        rollbackError = errorDuringRollback;
      }
    }
  }

  try {
    await database.execAsync('PRAGMA foreign_keys = ON;');
    const enabledForeignKeys = await database.getFirstAsync<{ foreign_keys: number }>(
      'PRAGMA foreign_keys',
    );
    if (enabledForeignKeys?.foreign_keys !== 1) {
      throw new Error('DATABASE_FOREIGN_KEYS_ENABLE_FAILED');
    }
  } catch (error) {
    foreignKeyRestoreError = error;
  }

  if (rollbackError) {
    throw new Error('DATABASE_MIGRATION_ROLLBACK_FAILED', { cause: rollbackError });
  }
  if (primaryError) {
    throw primaryError;
  }
  if (foreignKeyRestoreError) {
    throw foreignKeyRestoreError;
  }
}
