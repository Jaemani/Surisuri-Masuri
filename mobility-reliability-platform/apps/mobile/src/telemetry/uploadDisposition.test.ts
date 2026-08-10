// @ts-expect-error Node 22 provides this test-only module; the Expo app intentionally excludes Node types.
import { createHash } from 'node:crypto';
// @ts-expect-error Node 22 provides this test-only module; the Expo app intentionally excludes Node types.
import { DatabaseSync } from 'node:sqlite';

import { describe, expect, it } from 'vitest';

import { CREATE_TELEMETRY_SCHEMA_V4_SQL } from './databaseSchema';
import { buildImmutableTelemetryBatch } from './syncProtocol';
import {
  applyUploadDispositionCore,
  correlateUploadDispositionCore,
  createUploadDispositionCommand,
  type UploadDispositionDatabase,
  type UploadDispositionReadDatabase,
  type UploadDispositionTransaction,
} from './uploadDisposition';
import type { UploadLeaseReference } from './uploadLease';

type NodeDatabase = InstanceType<typeof DatabaseSync>;
type SqlValue = string | number | null;

const ids = {
  session: '60000000-0000-4000-8000-000000000001',
  installation: '60000000-0000-4000-8000-000000000002',
  tenant: '60000000-0000-4000-8000-000000000003',
  device: '60000000-0000-4000-8000-000000000004',
  trip: '60000000-0000-4000-8000-000000000005',
  consent: '60000000-0000-4000-8000-000000000006',
  batch: '60000000-0000-4000-8000-000000000007',
  leaseOwner: '60000000-0000-4000-8000-000000000008',
  takeoverOwner: '60000000-0000-4000-8000-000000000009',
  receipt: '60000000-0000-7000-8000-000000000010',
  serverBatch: '60000000-0000-7000-8000-000000000011',
};

const createdAt = '2026-07-23T08:00:00.000Z';
const leaseExpiresAt = '2026-07-23T08:07:00.000Z';
const observedAt = '2026-07-23T08:06:00.000Z';
const retryAt = '2026-07-23T08:07:00.000Z';

function numberedUuid(sequence: number): string {
  return `70000000-0000-4000-8000-${String(sequence).padStart(12, '0')}`;
}

function sha256(body: string): string {
  return createHash('sha256').update(body, 'utf8').digest('hex');
}

function openDatabase(): NodeDatabase {
  const database = new DatabaseSync(':memory:');
  database.exec('PRAGMA foreign_keys = ON;');
  database.exec(CREATE_TELEMETRY_SCHEMA_V4_SQL);
  return database;
}

function seedLeasedBatch(
  database: NodeDatabase,
  sampleCount = 2,
): { lease: UploadLeaseReference; eventIds: string[] } {
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
      createdAt,
      createdAt,
      createdAt,
    );

  const eventIds = Array.from({ length: sampleCount }, (_, index) =>
    numberedUuid(index + 1),
  );
  for (let sequence = 0; sequence < sampleCount; sequence += 1) {
    database
      .prepare(
        `INSERT INTO trip_event_log (
           event_id, session_id, event_sequence, sample_sequence, event_type,
           occurred_at, latitude, longitude, horizontal_accuracy_m,
           altitude_m, speed_mps, heading_degrees, is_mock_location,
           payload_json, created_at
         ) VALUES (?, ?, ?, ?, 'location_sample', ?, ?, ?, 5, NULL, NULL, NULL, 0, '{}', ?)`,
      )
      .run(
        eventIds[sequence],
        ids.session,
        sequence,
        sequence,
        createdAt,
        37.5 + sequence / 10_000,
        127 + sequence / 10_000,
        createdAt,
      );
    database
      .prepare(
        `INSERT INTO outbox_delivery (event_id, delivery_scope, state)
         VALUES (?, 'telemetry_upload', 'pending')`,
      )
      .run(eventIds[sequence]);
  }

  const immutable = buildImmutableTelemetryBatch({
    clientBatchId: ids.batch,
    sentAt: createdAt,
    scope: {
      tenantId: ids.tenant,
      deviceId: ids.device,
      tripId: ids.trip,
      clientSessionId: ids.session,
      installationId: ids.installation,
      consentRevisionId: ids.consent,
    },
    samples: eventIds.map((eventId, sequence) => ({
      clientSampleId: eventId,
      sequence,
      capturedAt: createdAt,
      latitude: 37.5 + sequence / 10_000,
      longitude: 127 + sequence / 10_000,
      horizontalAccuracyM: 5,
      altitudeM: null,
      speedMps: null,
      headingDegrees: null,
      activityHint: 'unknown',
      isMockLocation: false,
    })),
  });
  const digest = sha256(immutable.body);
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
      immutable.body,
      digest,
      sampleCount,
      createdAt,
      createdAt,
    );
  for (let position = 0; position < sampleCount; position += 1) {
    database
      .prepare(
        `INSERT INTO telemetry_upload_batch_item (
           client_batch_id, session_id, position, event_id
         ) VALUES (?, ?, ?, ?)`,
      )
      .run(ids.batch, ids.session, position, eventIds[position]);
  }
  database
    .prepare(
      `UPDATE telemetry_upload_batch
       SET state = 'leased', attempt_count = 1,
           lease_owner_id = ?, lease_expires_at = ?, updated_at = ?
       WHERE client_batch_id = ?`,
    )
    .run(ids.leaseOwner, leaseExpiresAt, observedAt, ids.batch);

  return {
    lease: {
      clientBatchId: ids.batch,
      sessionId: ids.session,
      sampleCount,
      attemptCount: 1,
      leaseOwnerId: ids.leaseOwner,
      leaseExpiresAt,
      body: immutable.body,
      bodySha256: digest,
    },
    eventIds,
  };
}

function transactionFor(database: NodeDatabase): UploadDispositionTransaction {
  return {
    getFirstAsync: async <T,>(source: string, ...params: SqlValue[]) =>
      (database.prepare(source).get(...params) as T | undefined) ?? null,
    runAsync: async (source: string, ...params: SqlValue[]) => {
      const result = database.prepare(source).run(...params);
      return { changes: Number(result.changes) };
    },
  };
}

function asAsyncDatabase(
  database: NodeDatabase,
  mutateTransaction?: (
    transaction: UploadDispositionTransaction,
  ) => UploadDispositionTransaction,
): UploadDispositionDatabase {
  return {
    withExclusiveTransactionAsync: async (task) => {
      database.exec('BEGIN IMMEDIATE;');
      try {
        const transaction = transactionFor(database);
        await task(mutateTransaction?.(transaction) ?? transaction);
        database.exec('COMMIT;');
      } catch (error) {
        database.exec('ROLLBACK;');
        throw error;
      }
    },
  };
}

function asReadDatabase(database: NodeDatabase): UploadDispositionReadDatabase {
  return {
    getFirstAsync: async <T,>(source: string, ...params: SqlValue[]) =>
      (database.prepare(source).get(...params) as T | undefined) ?? null,
  };
}

function acknowledgment(lease: UploadLeaseReference) {
  return {
    kind: 'acknowledged' as const,
    acknowledgment: {
      receiptId: ids.receipt,
      batchId: ids.serverBatch,
      clientBatchId: lease.clientBatchId,
      state: 'stored' as const,
      sampleCount: lease.sampleCount,
      replay: false,
    },
  };
}

describe('telemetry upload disposition', () => {
  it('releases an exact lease to pending with caller-supplied future backoff', async () => {
    const database = openDatabase();
    const { lease } = seedLeasedBatch(database);
    const command = createUploadDispositionCommand({
      lease,
      disposition: { kind: 'retry', code: 'network_failure' },
      observedAt,
      retryAt,
    });

    await applyUploadDispositionCore(asAsyncDatabase(database), command);

    expect(
      database
        .prepare(
          `SELECT state, attempt_count, lease_owner_id, lease_expires_at,
                  next_attempt_at, last_error_code
           FROM telemetry_upload_batch`,
        )
        .get(),
    ).toEqual({
      state: 'pending',
      attempt_count: 1,
      lease_owner_id: null,
      lease_expires_at: null,
      next_attempt_at: retryAt,
      last_error_code: 'network_failure',
    });
    expect(database.prepare(`SELECT DISTINCT state FROM outbox_delivery`).all()).toEqual([
      { state: 'batched' },
    ]);
    await expect(
      correlateUploadDispositionCore(asReadDatabase(database), command),
    ).resolves.toEqual({ kind: 'committed' });
    database.close();
  });

  it('atomically acknowledges the parent before every bound outbox row', async () => {
    const database = openDatabase();
    const { lease } = seedLeasedBatch(database);
    const command = createUploadDispositionCommand({
      lease,
      disposition: acknowledgment(lease),
      observedAt,
    });

    await applyUploadDispositionCore(asAsyncDatabase(database), command);

    expect(
      database
        .prepare(
          `SELECT state, receipt_id, server_batch_id, server_state, replay,
                  acknowledged_at, lease_owner_id, lease_expires_at
           FROM telemetry_upload_batch`,
        )
        .get(),
    ).toEqual({
      state: 'acknowledged',
      receipt_id: ids.receipt,
      server_batch_id: ids.serverBatch,
      server_state: 'stored',
      replay: 0,
      acknowledged_at: observedAt,
      lease_owner_id: null,
      lease_expires_at: null,
    });
    expect(
      database
        .prepare(
          `SELECT state, acknowledged_at, last_error_code
           FROM outbox_delivery ORDER BY event_id`,
        )
        .all(),
    ).toEqual([
      { state: 'acknowledged', acknowledged_at: observedAt, last_error_code: null },
      { state: 'acknowledged', acknowledged_at: observedAt, last_error_code: null },
    ]);
    await expect(
      correlateUploadDispositionCore(asReadDatabase(database), command),
    ).resolves.toEqual({ kind: 'committed' });
    database.close();
  });

  it('atomically holds the parent before every bound outbox row', async () => {
    const database = openDatabase();
    const { lease } = seedLeasedBatch(database);
    const command = createUploadDispositionCommand({
      lease,
      disposition: { kind: 'hold', code: 'payload_rejected' },
      observedAt,
    });

    await applyUploadDispositionCore(asAsyncDatabase(database), command);

    expect(
      database
        .prepare(
          `SELECT state, last_error_code, lease_owner_id, lease_expires_at
           FROM telemetry_upload_batch`,
        )
        .get(),
    ).toEqual({
      state: 'held',
      last_error_code: 'payload_rejected',
      lease_owner_id: null,
      lease_expires_at: null,
    });
    expect(
      database
        .prepare(
          `SELECT state, acknowledged_at, last_error_code
           FROM outbox_delivery ORDER BY event_id`,
        )
        .all(),
    ).toEqual([
      { state: 'held', acknowledged_at: null, last_error_code: 'payload_rejected' },
      { state: 'held', acknowledged_at: null, last_error_code: 'payload_rejected' },
    ]);
    database.close();
  });

  it('rejects malformed acknowledgment authority before opening a transaction', () => {
    const database = openDatabase();
    const { lease } = seedLeasedBatch(database);

    expect(() =>
      createUploadDispositionCommand({
        lease,
        disposition: {
          ...acknowledgment(lease),
          acknowledgment: {
            ...acknowledgment(lease).acknowledgment,
            sampleCount: lease.sampleCount + 1,
          },
        },
        observedAt,
      }),
    ).toThrow('UPLOAD_ACKNOWLEDGMENT_INVALID');
    expect(database.prepare(`SELECT state FROM telemetry_upload_batch`).get()).toEqual({
      state: 'leased',
    });
    database.close();
  });

  it.each([
    ['owner', `UPDATE telemetry_upload_batch SET lease_owner_id = '${ids.takeoverOwner}'`],
    ['attempt', `UPDATE telemetry_upload_batch SET attempt_count = 2`],
  ])('rejects stale %s authority without mutation', async (_field, mutation) => {
    const database = openDatabase();
    const { lease } = seedLeasedBatch(database);
    const command = createUploadDispositionCommand({
      lease,
      disposition: { kind: 'retry', code: 'server_unavailable' },
      observedAt,
      retryAt,
    });
    database.exec(mutation);

    await expect(
      applyUploadDispositionCore(asAsyncDatabase(database), command),
    ).rejects.toThrow('UPLOAD_DISPOSITION_AUTHORITY_CONFLICT');
    expect(database.prepare(`SELECT state FROM telemetry_upload_batch`).get()).toEqual({
      state: 'leased',
    });
    database.close();
  });

  it('rejects a lease reference whose immutable body digest is stale', async () => {
    const database = openDatabase();
    const { lease } = seedLeasedBatch(database);
    const command = createUploadDispositionCommand({
      lease: { ...lease, bodySha256: '0'.repeat(64) },
      disposition: { kind: 'retry', code: 'server_unavailable' },
      observedAt,
      retryAt,
    });

    await expect(
      applyUploadDispositionCore(asAsyncDatabase(database), command),
    ).rejects.toThrow('UPLOAD_DISPOSITION_AUTHORITY_CONFLICT');
    expect(database.prepare(`SELECT state FROM telemetry_upload_batch`).get()).toEqual({
      state: 'leased',
    });
    database.close();
  });

  it.each([
    [
      'expiry',
      (lease: UploadLeaseReference) => ({
        ...lease,
        leaseExpiresAt: '2026-07-23T08:07:01.000Z',
      }),
    ],
    [
      'session',
      (lease: UploadLeaseReference) => ({ ...lease, sessionId: ids.trip }),
    ],
    [
      'sample count',
      (lease: UploadLeaseReference) => ({ ...lease, sampleCount: lease.sampleCount + 1 }),
    ],
  ] as const)('rejects stale %s lease identity', async (_name, transform) => {
    const database = openDatabase();
    const { lease } = seedLeasedBatch(database);
    const command = createUploadDispositionCommand({
      lease: transform(lease),
      disposition: { kind: 'retry', code: 'server_unavailable' },
      observedAt,
      retryAt,
    });

    await expect(
      applyUploadDispositionCore(asAsyncDatabase(database), command),
    ).rejects.toThrow('UPLOAD_DISPOSITION_AUTHORITY_CONFLICT');
    database.close();
  });

  it('rejects a disposition timestamp older than the persisted lease state', async () => {
    const database = openDatabase();
    const { lease } = seedLeasedBatch(database);
    const command = createUploadDispositionCommand({
      lease,
      disposition: { kind: 'retry', code: 'network_failure' },
      observedAt: '2026-07-23T08:05:59.999Z',
      retryAt,
    });

    await expect(
      applyUploadDispositionCore(asAsyncDatabase(database), command),
    ).rejects.toThrow('UPLOAD_DISPOSITION_AUTHORITY_CONFLICT');
    await expect(
      correlateUploadDispositionCore(asReadDatabase(database), command),
    ).resolves.toEqual({ kind: 'unverifiable' });
    database.close();
  });

  it('accepts an exact acknowledgment after lease expiry but rejects it after takeover', async () => {
    const expiredObservedAt = '2026-07-23T08:08:00.000Z';
    const accepted = openDatabase();
    const acceptedSeed = seedLeasedBatch(accepted);
    const acceptedCommand = createUploadDispositionCommand({
      lease: acceptedSeed.lease,
      disposition: acknowledgment(acceptedSeed.lease),
      observedAt: expiredObservedAt,
    });
    await expect(
      applyUploadDispositionCore(asAsyncDatabase(accepted), acceptedCommand),
    ).resolves.toBeUndefined();
    accepted.close();

    const takenOver = openDatabase();
    const takeoverSeed = seedLeasedBatch(takenOver);
    const staleCommand = createUploadDispositionCommand({
      lease: takeoverSeed.lease,
      disposition: acknowledgment(takeoverSeed.lease),
      observedAt: expiredObservedAt,
    });
    takenOver
      .prepare(
        `UPDATE telemetry_upload_batch
         SET attempt_count = 2, lease_owner_id = ?,
             lease_expires_at = '2026-07-23T08:10:00.000Z'`,
      )
      .run(ids.takeoverOwner);
    await expect(
      applyUploadDispositionCore(asAsyncDatabase(takenOver), staleCommand),
    ).rejects.toThrow('UPLOAD_DISPOSITION_AUTHORITY_CONFLICT');
    takenOver.close();
  });

  it('rolls back the parent when the child cardinality write is incomplete', async () => {
    const database = openDatabase();
    const { lease } = seedLeasedBatch(database);
    const command = createUploadDispositionCommand({
      lease,
      disposition: acknowledgment(lease),
      observedAt,
    });
    let writes = 0;

    await expect(
      applyUploadDispositionCore(
        asAsyncDatabase(database, (transaction) => ({
          ...transaction,
          runAsync: async (source, ...params) => {
            writes += 1;
            const result = await transaction.runAsync(source, ...params);
            return writes === 2 ? { changes: result.changes - 1 } : result;
          },
        })),
        command,
      ),
    ).rejects.toThrow('UPLOAD_ACKNOWLEDGMENT_BINDING_INCOMPLETE');
    expect(database.prepare(`SELECT state FROM telemetry_upload_batch`).get()).toEqual({
      state: 'leased',
    });
    expect(database.prepare(`SELECT DISTINCT state FROM outbox_delivery`).all()).toEqual([
      { state: 'batched' },
    ]);
    database.close();
  });

  it('correlates an untouched lease as not committed', async () => {
    const database = openDatabase();
    const { lease } = seedLeasedBatch(database);
    const command = createUploadDispositionCommand({
      lease,
      disposition: { kind: 'hold', code: 'authorization_rejected' },
      observedAt,
    });

    await expect(
      correlateUploadDispositionCore(asReadDatabase(database), command),
    ).resolves.toEqual({ kind: 'not_committed' });
    database.close();
  });

  it('returns unverifiable for missing, taken-over, or differently committed authority', async () => {
    const authoritySource = openDatabase();
    const { lease } = seedLeasedBatch(authoritySource);
    const command = createUploadDispositionCommand({
      lease,
      disposition: { kind: 'retry', code: 'rate_limited' },
      observedAt,
      retryAt,
    });
    authoritySource.close();

    const missing = openDatabase();
    await expect(
      correlateUploadDispositionCore(asReadDatabase(missing), command),
    ).resolves.toEqual({ kind: 'unverifiable' });
    missing.close();

    const changed = openDatabase();
    seedLeasedBatch(changed);
    changed
      .prepare(`UPDATE telemetry_upload_batch SET lease_owner_id = ?`)
      .run(ids.takeoverOwner);
    await expect(
      correlateUploadDispositionCore(asReadDatabase(changed), command),
    ).resolves.toEqual({ kind: 'unverifiable' });
    changed.close();
  });

  it('recovers a simulated lost commit response through a fresh read correlation', async () => {
    const database = openDatabase();
    const { lease } = seedLeasedBatch(database);
    const command = createUploadDispositionCommand({
      lease,
      disposition: acknowledgment(lease),
      observedAt,
    });
    const responseLostDatabase: UploadDispositionDatabase = {
      withExclusiveTransactionAsync: async (task) => {
        database.exec('BEGIN IMMEDIATE;');
        await task(transactionFor(database));
        database.exec('COMMIT;');
        throw new Error('SIMULATED_COMMIT_RESPONSE_LOSS');
      },
    };

    await expect(
      applyUploadDispositionCore(responseLostDatabase, command),
    ).rejects.toThrow('SIMULATED_COMMIT_RESPONSE_LOSS');
    await expect(
      correlateUploadDispositionCore(asReadDatabase(database), command),
    ).resolves.toEqual({ kind: 'committed' });
    database.close();
  });

  it('does not correlate a retry reconstructed with a different observation time', async () => {
    const database = openDatabase();
    const { lease } = seedLeasedBatch(database);
    const committedCommand = createUploadDispositionCommand({
      lease,
      disposition: { kind: 'retry', code: 'network_failure' },
      observedAt,
      retryAt,
    });
    await applyUploadDispositionCore(asAsyncDatabase(database), committedCommand);
    const reconstructed = createUploadDispositionCommand({
      lease,
      disposition: { kind: 'retry', code: 'network_failure' },
      observedAt: '2026-07-23T08:05:59.999Z',
      retryAt,
    });

    await expect(
      correlateUploadDispositionCore(asReadDatabase(database), reconstructed),
    ).resolves.toEqual({ kind: 'unverifiable' });
    database.close();
  });

  it('fails closed when exact terminal child metadata cannot be proven', async () => {
    const database = openDatabase();
    const { lease } = seedLeasedBatch(database);
    const command = createUploadDispositionCommand({
      lease,
      disposition: acknowledgment(lease),
      observedAt,
    });
    await applyUploadDispositionCore(asAsyncDatabase(database), command);
    const metadataBlindRead: UploadDispositionReadDatabase = {
      getFirstAsync: async <T,>(source: string, ...params: SqlValue[]) => {
        const row = database.prepare(source).get(...params) as Record<string, unknown>;
        return { ...row, clean_acknowledged_count: 0 } as T;
      },
    };

    await expect(
      correlateUploadDispositionCore(metadataBlindRead, command),
    ).resolves.toEqual({ kind: 'unverifiable' });
    database.close();
  });

  it('does not correlate a binding whose position is stored as a SQLite real', async () => {
    const database = openDatabase();
    const { lease } = seedLeasedBatch(database, 3);
    const command = createUploadDispositionCommand({
      lease,
      disposition: { kind: 'hold', code: 'authorization_rejected' },
      observedAt,
    });
    database.exec(`
      DROP TRIGGER immutable_upload_batch_item;
      PRAGMA ignore_check_constraints = ON;
      UPDATE telemetry_upload_batch_item
      SET position = 0.5
      WHERE client_batch_id = '${ids.batch}' AND position = 1;
      PRAGMA ignore_check_constraints = OFF;
    `);

    await expect(
      correlateUploadDispositionCore(asReadDatabase(database), command),
    ).resolves.toEqual({ kind: 'unverifiable' });
    await expect(
      applyUploadDispositionCore(asAsyncDatabase(database), command),
    ).rejects.toThrow('UPLOAD_DISPOSITION_AUTHORITY_CONFLICT');
    database.close();
  });

  it('rejects undefined reauthentication and malformed retry policy inputs', () => {
    const database = openDatabase();
    const { lease } = seedLeasedBatch(database);

    expect(() =>
      createUploadDispositionCommand({
        lease,
        disposition: { kind: 'reauthenticate', code: 'unauthenticated' },
        observedAt,
      }),
    ).toThrow('UPLOAD_REAUTHENTICATION_POLICY_UNDEFINED');
    expect(() =>
      createUploadDispositionCommand({
        lease,
        disposition: { kind: 'retry', code: 'network_failure' },
        observedAt,
      }),
    ).toThrow('UPLOAD_RETRY_DISPOSITION_INVALID');
    expect(() =>
      createUploadDispositionCommand({
        lease,
        disposition: { kind: 'retry', code: 'network_failure' },
        observedAt,
        retryAt: observedAt,
      }),
    ).toThrow('UPLOAD_RETRY_TIME_INVALID');
    expect(() =>
      createUploadDispositionCommand({
        lease,
        disposition: { kind: 'retry', code: 'network_failure' },
        observedAt,
        retryAt: '2026-07-23T08:21:00.001Z',
      }),
    ).toThrow('UPLOAD_RETRY_TIME_INVALID');
    database.close();
  });

  it('fails before mutation when transaction foreign keys are disabled', async () => {
    const database = openDatabase();
    const { lease } = seedLeasedBatch(database);
    const command = createUploadDispositionCommand({
      lease,
      disposition: { kind: 'retry', code: 'network_failure' },
      observedAt,
      retryAt,
    });
    database.exec('PRAGMA foreign_keys = OFF;');

    await expect(
      applyUploadDispositionCore(asAsyncDatabase(database), command),
    ).rejects.toThrow('UPLOAD_DATABASE_FOREIGN_KEYS_DISABLED');
    expect(database.prepare(`SELECT state FROM telemetry_upload_batch`).get()).toEqual({
      state: 'leased',
    });
    database.close();
  });

  it('revalidates a structurally forged command before opening a transaction', async () => {
    const database = openDatabase();
    const { lease } = seedLeasedBatch(database);
    const valid = createUploadDispositionCommand({
      lease,
      disposition: { kind: 'retry', code: 'network_failure' },
      observedAt,
      retryAt,
    });
    const forged = {
      ...valid,
      disposition: { kind: 'unknown', code: 'network_failure' },
    } as unknown as typeof valid;

    await expect(
      applyUploadDispositionCore(asAsyncDatabase(database), forged),
    ).rejects.toThrow('UPLOAD_RETRY_DISPOSITION_INVALID');
    expect(database.prepare(`SELECT state FROM telemetry_upload_batch`).get()).toEqual({
      state: 'leased',
    });
    database.close();
  });
});
