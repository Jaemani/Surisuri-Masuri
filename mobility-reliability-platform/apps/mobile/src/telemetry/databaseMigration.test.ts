// @ts-expect-error Node 22 provides this test-only module; the Expo app intentionally excludes Node types.
import { DatabaseSync } from 'node:sqlite';

import { describe, expect, it } from 'vitest';

import {
  migrateTelemetryDatabaseCore,
  type TelemetryMigrationDatabase,
} from './databaseMigration';
import { CURRENT_TELEMETRY_SCHEMA_VERSION } from './databaseSchema';
import { buildImmutableTelemetryBatch } from './syncProtocol';

type NodeDatabase = InstanceType<typeof DatabaseSync>;
type SqlValue = string | number | null;

function asAsyncDatabase(database: NodeDatabase): TelemetryMigrationDatabase {
  return {
    execAsync: async (source) => database.exec(source),
    getFirstAsync: async <T,>(source: string, ...params: SqlValue[]) =>
      (database.prepare(source).get(...params) as T | undefined) ?? null,
    getAllAsync: async <T,>(source: string, ...params: SqlValue[]) =>
      database.prepare(source).all(...params) as T[],
  };
}

function seedHeldV3Batch(
  database: NodeDatabase,
  positions: readonly number[],
  finalState: 'leased' | 'held' = 'held',
): void {
  const ids = {
    session: '90000000-0000-4000-8000-000000000011',
    installation: '90000000-0000-4000-8000-000000000012',
    tenant: '90000000-0000-4000-8000-000000000013',
    device: '90000000-0000-4000-8000-000000000014',
    trip: '90000000-0000-4000-8000-000000000015',
    consent: '90000000-0000-4000-8000-000000000016',
    events: positions.map(
      (_, index) =>
        `90000000-0000-4000-8000-${String(17 + index).padStart(12, '0')}`,
    ),
    batch: '90000000-0000-4000-8000-000000000100',
    owner: '90000000-0000-4000-8000-000000000101',
  };
  const timestamps = positions.map((_, index) =>
    new Date(Date.parse('2026-07-23T11:00:00.000Z') + index * 1_000).toISOString(),
  );
  const finalTimestamp = timestamps.at(-1) ?? timestamps[0];

  database.exec(`
    DROP TRIGGER validate_terminal_outbox_delivery;
    DROP TRIGGER immutable_terminal_outbox_delivery;
    DROP TRIGGER enforce_upload_batch_cardinality;
    DROP TRIGGER validate_upload_batch_item_position;
    DROP TRIGGER require_uploadable_batch_item;
    DROP INDEX active_upload_batch_fifo;
    DROP INDEX expired_upload_batch;
    PRAGMA user_version = 3;
    PRAGMA ignore_check_constraints = ON;
  `);

  database
    .prepare(
      `INSERT INTO trip_session_projection (
         session_id, installation_id, tenant_id, mobility_device_id,
         server_trip_id, consent_revision_id, upload_eligibility,
         started_at, ended_at, state, updated_at
       ) VALUES (?, ?, ?, ?, ?, ?, 'server_bound', ?, ?, 'stopped', ?)`,
    )
    .run(
      ids.session,
      ids.installation,
      ids.tenant,
      ids.device,
      ids.trip,
      ids.consent,
      timestamps[0],
      finalTimestamp,
      finalTimestamp,
    );

  for (const [index, eventId] of ids.events.entries()) {
    database
      .prepare(
        `INSERT INTO trip_event_log (
           event_id, session_id, event_sequence, sample_sequence, event_type,
           occurred_at, latitude, longitude, horizontal_accuracy_m,
           altitude_m, speed_mps, heading_degrees, is_mock_location,
           payload_json, created_at
         ) VALUES (?, ?, ?, ?, 'location_sample', ?, ?, ?, 5,
                   NULL, NULL, NULL, 0, '{}', ?)`,
      )
      .run(
        eventId,
        ids.session,
        index,
        index,
        timestamps[index],
        37.5 + index / 100,
        127 + index / 100,
        timestamps[index],
      );
    database
      .prepare(
        `INSERT INTO outbox_delivery (event_id, delivery_scope, state)
         VALUES (?, 'telemetry_upload', 'pending')`,
      )
      .run(eventId);
  }

  const body = buildImmutableTelemetryBatch({
    clientBatchId: ids.batch,
    sentAt: timestamps[1],
    scope: {
      tenantId: ids.tenant,
      deviceId: ids.device,
      tripId: ids.trip,
      clientSessionId: ids.session,
      installationId: ids.installation,
      consentRevisionId: ids.consent,
    },
    samples: ids.events.map((eventId, index) => ({
      clientSampleId: eventId,
      sequence: index,
      capturedAt: timestamps[index],
      latitude: 37.5 + index / 100,
      longitude: 127 + index / 100,
      horizontalAccuracyM: 5,
      isMockLocation: false,
    })),
  }).body;

  database
    .prepare(
      `INSERT INTO telemetry_upload_batch (
         client_batch_id, session_id, installation_id, tenant_id, device_id,
         server_trip_id, consent_revision_id, body_json, body_sha256,
         sample_count, state, created_at, updated_at
       ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?)`,
    )
    .run(
      ids.batch,
      ids.session,
      ids.installation,
      ids.tenant,
      ids.device,
      ids.trip,
      ids.consent,
      body,
      'a'.repeat(64),
      positions.length,
      timestamps[0],
      finalTimestamp,
    );

  for (const [index, position] of positions.entries()) {
    database
      .prepare(
        `INSERT INTO telemetry_upload_batch_item (
           client_batch_id, session_id, position, event_id
         ) VALUES (?, ?, ?, ?)`,
      )
      .run(ids.batch, ids.session, position, ids.events[index]);
  }

  database.exec('PRAGMA ignore_check_constraints = OFF;');

  database
    .prepare(
      `UPDATE telemetry_upload_batch
       SET state = 'leased', attempt_count = 1, lease_owner_id = ?,
           lease_expires_at = '2026-07-23T11:02:00.000Z', updated_at = ?`,
    )
    .run(ids.owner, finalTimestamp);
  if (finalState === 'leased') return;
  database
    .prepare(
      `UPDATE telemetry_upload_batch
       SET state = 'held', attempt_count = 0, lease_owner_id = NULL,
           lease_expires_at = NULL, last_error_code = 'payload_rejected',
           updated_at = ?`,
    )
    .run(finalTimestamp);
  database
    .prepare(
      `UPDATE outbox_delivery
       SET state = 'held', attempt_count = 0, next_attempt_at = NULL,
           acknowledged_at = NULL, last_error_code = 'payload_rejected'
       WHERE event_id IN (${ids.events.map(() => '?').join(', ')})`,
    )
    .run(...ids.events);
}

describe('telemetry database migration coordinator', () => {
  it('creates and revalidates v4 under a writer transaction', async () => {
    const database = new DatabaseSync(':memory:');

    await migrateTelemetryDatabaseCore(asAsyncDatabase(database));
    await migrateTelemetryDatabaseCore(asAsyncDatabase(database));

    expect(database.prepare('PRAGMA user_version').get()).toEqual({
      user_version: CURRENT_TELEMETRY_SCHEMA_VERSION,
    });
    expect(database.prepare('PRAGMA foreign_keys').get()).toEqual({ foreign_keys: 1 });
    expect(
      database
        .prepare(`SELECT 1 AS present FROM sqlite_master WHERE type = 'index' AND name = ?`)
        .get('active_upload_batch_fifo'),
    ).toEqual({ present: 1 });
    expect(
      database
        .prepare(`SELECT 1 AS present FROM sqlite_master WHERE type = 'trigger' AND name = ?`)
        .get('validate_upload_batch_item_position'),
    ).toEqual({ present: 1 });
    database.close();
  });

  it('serializes stale v3 initializers and applies v3-to-v4 exactly once', async () => {
    const shared = {
      version: 3,
      migrationApplications: 0,
      activeWriters: 0,
      maximumActiveWriters: 0,
      lockTail: Promise.resolve(),
    };

    function connection(): TelemetryMigrationDatabase {
      let foreignKeys = 1;
      let pendingVersion: number | null = null;
      let releaseLock: (() => void) | null = null;
      return {
        execAsync: async (source) => {
          if (source === 'PRAGMA foreign_keys = OFF;') {
            foreignKeys = 0;
            return;
          }
          if (source === 'PRAGMA foreign_keys = ON;') {
            foreignKeys = 1;
            return;
          }
          if (source === 'BEGIN IMMEDIATE;') {
            const previous = shared.lockTail;
            let release!: () => void;
            shared.lockTail = new Promise<void>((resolve) => {
              release = resolve;
            });
            await previous;
            releaseLock = release;
            shared.activeWriters += 1;
            shared.maximumActiveWriters = Math.max(
              shared.maximumActiveWriters,
              shared.activeWriters,
            );
            return;
          }
          if (source.includes('CREATE INDEX active_upload_batch_fifo')) {
            shared.migrationApplications += 1;
            pendingVersion = 4;
            return;
          }
          if (source === 'COMMIT;') {
            if (pendingVersion !== null) shared.version = pendingVersion;
            shared.activeWriters -= 1;
            releaseLock?.();
            releaseLock = null;
            return;
          }
          if (source === 'ROLLBACK;') {
            shared.activeWriters -= 1;
            releaseLock?.();
            releaseLock = null;
          }
        },
        getFirstAsync: async <T,>(source: string) => {
          if (source === 'PRAGMA foreign_keys') return { foreign_keys: foreignKeys } as T;
          if (source === 'PRAGMA user_version') return { user_version: shared.version } as T;
          return null;
        },
        getAllAsync: async <T,>() => [] as T[],
      };
    }

    await Promise.all([
      migrateTelemetryDatabaseCore(connection()),
      migrateTelemetryDatabaseCore(connection()),
    ]);

    expect(shared.version).toBe(4);
    expect(shared.migrationApplications).toBe(1);
    expect(shared.maximumActiveWriters).toBe(1);
  });

  it('rolls back when the BEGIN response is lost after native open', async () => {
    let foreignKeys = 1;
    const operations: string[] = [];
    const database: TelemetryMigrationDatabase = {
      execAsync: async (source) => {
        if (source === 'PRAGMA foreign_keys = OFF;') foreignKeys = 0;
        if (source === 'PRAGMA foreign_keys = ON;') foreignKeys = 1;
        if (source === 'ROLLBACK;') operations.push('rollback');
        if (source === 'BEGIN IMMEDIATE;') {
          operations.push('begin_native_open');
          throw new Error('BEGIN_RESPONSE_LOST');
        }
      },
      getFirstAsync: async <T,>(source: string) =>
        (source === 'PRAGMA foreign_keys' ? ({ foreign_keys: foreignKeys } as T) : null),
      getAllAsync: async <T,>() => [] as T[],
    };

    await expect(migrateTelemetryDatabaseCore(database)).rejects.toThrow(
      'BEGIN_RESPONSE_LOST',
    );
    expect(operations).toEqual(['begin_native_open', 'rollback']);
    expect(foreignKeys).toBe(1);
  });

  it('surfaces an unverifiable migration when BEGIN and rollback responses are lost', async () => {
    let foreignKeys = 1;
    const operations: string[] = [];
    const database: TelemetryMigrationDatabase = {
      execAsync: async (source) => {
        if (source === 'PRAGMA foreign_keys = OFF;') foreignKeys = 0;
        if (source === 'PRAGMA foreign_keys = ON;' && foreignKeys === 1) {
          foreignKeys = 1;
        }
        if (source === 'BEGIN IMMEDIATE;') {
          operations.push('begin_native_open');
          throw new Error('BEGIN_RESPONSE_LOST');
        }
        if (source === 'ROLLBACK;') {
          operations.push('rollback_response_lost');
          throw new Error('ROLLBACK_RESPONSE_LOST');
        }
      },
      getFirstAsync: async <T,>(source: string) =>
        (source === 'PRAGMA foreign_keys' ? ({ foreign_keys: foreignKeys } as T) : null),
      getAllAsync: async <T,>() => [] as T[],
    };

    await expect(migrateTelemetryDatabaseCore(database)).rejects.toThrow(
      'DATABASE_MIGRATION_ROLLBACK_FAILED',
    );
    expect(operations).toEqual(['begin_native_open', 'rollback_response_lost']);
  });

  it('rejects a v3 terminal parent whose child is still batched', async () => {
    const database = new DatabaseSync(':memory:');
    const asyncDatabase = asAsyncDatabase(database);
    await migrateTelemetryDatabaseCore(asyncDatabase);
    database.exec(`
      DROP TRIGGER validate_terminal_outbox_delivery;
      DROP TRIGGER immutable_terminal_outbox_delivery;
      DROP INDEX active_upload_batch_fifo;
      PRAGMA user_version = 3;
    `);
    const ids = {
      session: '90000000-0000-4000-8000-000000000001',
      installation: '90000000-0000-4000-8000-000000000002',
      tenant: '90000000-0000-4000-8000-000000000003',
      device: '90000000-0000-4000-8000-000000000004',
      trip: '90000000-0000-4000-8000-000000000005',
      consent: '90000000-0000-4000-8000-000000000006',
      event: '90000000-0000-4000-8000-000000000007',
      batch: '90000000-0000-4000-8000-000000000008',
      owner: '90000000-0000-4000-8000-000000000009',
    };
    const now = '2026-07-23T11:00:00.000Z';
    database
      .prepare(
        `INSERT INTO trip_session_projection (
           session_id, installation_id, tenant_id, mobility_device_id,
           server_trip_id, consent_revision_id, upload_eligibility,
           started_at, ended_at, state, updated_at
         ) VALUES (?, ?, ?, ?, ?, ?, 'server_bound', ?, ?, 'stopped', ?)`,
      )
      .run(
        ids.session,
        ids.installation,
        ids.tenant,
        ids.device,
        ids.trip,
        ids.consent,
        now,
        now,
        now,
      );
    database
      .prepare(
        `INSERT INTO trip_event_log (
           event_id, session_id, event_sequence, sample_sequence, event_type,
           occurred_at, latitude, longitude, horizontal_accuracy_m,
           altitude_m, speed_mps, heading_degrees, is_mock_location,
           payload_json, created_at
         ) VALUES (?, ?, 0, 0, 'location_sample', ?, 37.5, 127, 5,
                   NULL, NULL, NULL, 0, '{}', ?)`,
      )
      .run(ids.event, ids.session, now, now);
    database
      .prepare(
        `INSERT INTO outbox_delivery (event_id, delivery_scope, state)
         VALUES (?, 'telemetry_upload', 'pending')`,
      )
      .run(ids.event);
    const body = buildImmutableTelemetryBatch({
      clientBatchId: ids.batch,
      sentAt: now,
      scope: {
        tenantId: ids.tenant,
        deviceId: ids.device,
        tripId: ids.trip,
        clientSessionId: ids.session,
        installationId: ids.installation,
        consentRevisionId: ids.consent,
      },
      samples: [
        {
          clientSampleId: ids.event,
          sequence: 0,
          capturedAt: now,
          latitude: 37.5,
          longitude: 127,
          horizontalAccuracyM: 5,
          isMockLocation: false,
        },
      ],
    }).body;
    database
      .prepare(
        `INSERT INTO telemetry_upload_batch (
           client_batch_id, session_id, installation_id, tenant_id, device_id,
           server_trip_id, consent_revision_id, body_json, body_sha256,
           sample_count, state, created_at, updated_at
         ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 'pending', ?, ?)`,
      )
      .run(
        ids.batch,
        ids.session,
        ids.installation,
        ids.tenant,
        ids.device,
        ids.trip,
        ids.consent,
        body,
        'a'.repeat(64),
        now,
        now,
      );
    database
      .prepare(
        `INSERT INTO telemetry_upload_batch_item (
           client_batch_id, session_id, position, event_id
         ) VALUES (?, ?, 0, ?)`,
      )
      .run(ids.batch, ids.session, ids.event);
    database
      .prepare(
        `UPDATE telemetry_upload_batch
         SET state = 'leased', attempt_count = 1, lease_owner_id = ?,
             lease_expires_at = '2026-07-23T11:02:00.000Z', updated_at = ?`,
      )
      .run(ids.owner, now);
    database
      .prepare(
        `UPDATE telemetry_upload_batch
         SET state = 'held', lease_owner_id = NULL, lease_expires_at = NULL,
             last_error_code = 'payload_rejected', updated_at = ?`,
      )
      .run(now);

    await expect(migrateTelemetryDatabaseCore(asyncDatabase)).rejects.toThrow(
      'DATABASE_TERMINAL_OUTBOX_DELIVERY_INVALID',
    );
    expect(database.prepare('PRAGMA user_version').get()).toEqual({ user_version: 3 });
    expect(database.prepare('PRAGMA foreign_keys').get()).toEqual({ foreign_keys: 1 });
    expect(
      database
        .prepare(`SELECT 1 FROM sqlite_master WHERE type = 'index' AND name = ?`)
        .get('active_upload_batch_fifo'),
    ).toBeUndefined();
    database.close();
  });

  it('rejects a v3 terminal batch with a position gap', async () => {
    const database = new DatabaseSync(':memory:');
    const asyncDatabase = asAsyncDatabase(database);
    await migrateTelemetryDatabaseCore(asyncDatabase);
    seedHeldV3Batch(database, [0, 2]);

    await expect(migrateTelemetryDatabaseCore(asyncDatabase)).rejects.toThrow(
      'DATABASE_UPLOAD_BATCH_BINDING_INVALID',
    );
    expect(database.prepare('PRAGMA user_version').get()).toEqual({ user_version: 3 });
    database.close();
  });

  it('rejects a v3 terminal batch with an out-of-range position', async () => {
    const database = new DatabaseSync(':memory:');
    const asyncDatabase = asAsyncDatabase(database);
    await migrateTelemetryDatabaseCore(asyncDatabase);
    seedHeldV3Batch(database, [1, 2]);

    await expect(migrateTelemetryDatabaseCore(asyncDatabase)).rejects.toThrow(
      'DATABASE_UPLOAD_BATCH_BINDING_INVALID',
    );
    expect(database.prepare('PRAGMA user_version').get()).toEqual({ user_version: 3 });
    database.close();
  });

  it('rejects a v3 terminal batch with a fractional position', async () => {
    const database = new DatabaseSync(':memory:');
    const asyncDatabase = asAsyncDatabase(database);
    await migrateTelemetryDatabaseCore(asyncDatabase);
    seedHeldV3Batch(database, [0, 0.5, 2]);

    await expect(migrateTelemetryDatabaseCore(asyncDatabase)).rejects.toThrow(
      'DATABASE_UPLOAD_BATCH_BINDING_INVALID',
    );
    expect(database.prepare('PRAGMA user_version').get()).toEqual({ user_version: 3 });
    database.close();
  });

  it('rejects a v3 leased batch with a fractional position before v4 promotion', async () => {
    const database = new DatabaseSync(':memory:');
    const asyncDatabase = asAsyncDatabase(database);
    await migrateTelemetryDatabaseCore(asyncDatabase);
    seedHeldV3Batch(database, [0, 0.5, 2], 'leased');

    await expect(migrateTelemetryDatabaseCore(asyncDatabase)).rejects.toThrow(
      'DATABASE_UPLOAD_BATCH_BINDING_INVALID',
    );
    expect(database.prepare('PRAGMA user_version').get()).toEqual({ user_version: 3 });
    database.close();
  });

  it('classifies a malformed nested sample as an invalid body without leaking a JSON error', async () => {
    const database = new DatabaseSync(':memory:');
    const asyncDatabase = asAsyncDatabase(database);
    await migrateTelemetryDatabaseCore(asyncDatabase);
    seedHeldV3Batch(database, [0, 1]);
    database.exec(`
      DROP TRIGGER immutable_terminal_upload_batch;
      DROP TRIGGER immutable_upload_batch_body;
    `);
    const malformedNestedBody = JSON.stringify({
      schemaVersion: 'telemetry-batch.v2',
      clientBatchId: '90000000-0000-4000-8000-000000000100',
      tenantId: '90000000-0000-4000-8000-000000000013',
      deviceId: '90000000-0000-4000-8000-000000000014',
      tripId: '90000000-0000-4000-8000-000000000015',
      clientSessionId: '90000000-0000-4000-8000-000000000011',
      installationId: '90000000-0000-4000-8000-000000000012',
      consentRevisionId: '90000000-0000-4000-8000-000000000016',
      sentAt: '2026-07-23T11:00:01.000Z',
      samples: ['{', '{'],
    });
    database
      .prepare(`UPDATE telemetry_upload_batch SET body_json = ?`)
      .run(malformedNestedBody);

    await expect(migrateTelemetryDatabaseCore(asyncDatabase)).rejects.toThrow(
      'DATABASE_UPLOAD_BATCH_BODY_INVALID',
    );
    expect(database.prepare('PRAGMA user_version').get()).toEqual({ user_version: 3 });
    database.close();
  });
});
