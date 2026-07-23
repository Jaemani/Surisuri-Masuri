// @ts-expect-error Node 22 provides this test-only module; the Expo app intentionally excludes Node types.
import { createHash } from 'node:crypto';
// @ts-expect-error Node 22 provides this test-only module; the Expo app intentionally excludes Node types.
import { DatabaseSync } from 'node:sqlite';

import { describe, expect, it } from 'vitest';

import { CREATE_TELEMETRY_SCHEMA_V3_SQL } from './databaseSchema';
import {
  materializeNextUploadBatchCore,
  type UploadMaterializerDatabase,
  type UploadMaterializerDependencies,
  type UploadMaterializerTransaction,
} from './uploadMaterializer';

type NodeDatabase = InstanceType<typeof DatabaseSync>;
type SqlValue = string | number | null;

const ids = {
  sessionA: '20000000-0000-4000-8000-000000000001',
  sessionB: '20000000-0000-4000-8000-000000000002',
  localSession: '20000000-0000-4000-8000-000000000003',
  installation: '20000000-0000-4000-8000-000000000004',
  tenant: '20000000-0000-4000-8000-000000000005',
  device: '20000000-0000-4000-8000-000000000006',
  tripA: '20000000-0000-4000-8000-000000000007',
  tripB: '20000000-0000-4000-8000-000000000008',
  consent: '20000000-0000-4000-8000-000000000009',
  batchA: '20000000-0000-4000-8000-000000000010',
};

const now = '2026-07-23T08:00:00.000Z';

function numberedUuid(sequence: number): string {
  return `30000000-0000-4000-8000-${String(sequence).padStart(12, '0')}`;
}

function openDatabase(): NodeDatabase {
  const database = new DatabaseSync(':memory:');
  database.exec('PRAGMA foreign_keys = ON;');
  database.exec(CREATE_TELEMETRY_SCHEMA_V3_SQL);
  return database;
}

function insertSession(
  database: NodeDatabase,
  input: {
    sessionId: string;
    eligibility: 'development_local_only' | 'server_bound';
    tripId?: string;
    startedAt?: string;
  },
): void {
  database
    .prepare(
      `INSERT INTO trip_session_projection (
         session_id, installation_id, tenant_id, mobility_device_id,
         server_trip_id, consent_revision_id, upload_eligibility,
         started_at, ended_at, state, updated_at
       ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'stopped', ?)`,
    )
    .run(
      input.sessionId,
      ids.installation,
      input.eligibility === 'server_bound' ? ids.tenant : null,
      input.eligibility === 'server_bound' ? ids.device : null,
      input.eligibility === 'server_bound' ? (input.tripId ?? ids.tripA) : null,
      input.eligibility === 'server_bound' ? ids.consent : null,
      input.eligibility,
      input.startedAt ?? now,
      input.startedAt ?? now,
      input.startedAt ?? now,
    );
}

function insertLocation(
  database: NodeDatabase,
  input: {
    eventId: string;
    sessionId: string;
    sequence: number;
    eligibility?: 'development_local_only' | 'server_bound';
    capturedAt?: string;
    createdAt?: string;
    latitude?: number;
    longitude?: number;
    accuracy?: number | null;
    altitude?: number | null;
    speed?: number | null;
    heading?: number | null;
    isMockLocation?: number | null;
  },
): void {
  const capturedAt = input.capturedAt ?? now;
  database
    .prepare(
      `INSERT INTO trip_event_log (
         event_id, session_id, event_sequence, sample_sequence, event_type,
         occurred_at, latitude, longitude, horizontal_accuracy_m, altitude_m,
         speed_mps, heading_degrees, is_mock_location, payload_json, created_at
       ) VALUES (?, ?, ?, ?, 'location_sample', ?, ?, ?, ?, ?, ?, ?, ?, '{}', ?)`,
    )
    .run(
      input.eventId,
      input.sessionId,
      input.sequence,
      input.sequence,
      capturedAt,
      input.latitude ?? 37.5,
      input.longitude ?? 127,
      input.accuracy === undefined ? 5 : input.accuracy,
      input.altitude ?? null,
      input.speed ?? null,
      input.heading ?? null,
      input.isMockLocation ?? null,
      input.createdAt ?? capturedAt,
    );

  const serverBound = (input.eligibility ?? 'server_bound') === 'server_bound';
  database
    .prepare(
      `INSERT INTO outbox_delivery (event_id, delivery_scope, state)
       VALUES (?, ?, ?)`,
    )
    .run(
      input.eventId,
      serverBound ? 'telemetry_upload' : 'local_only',
      serverBound ? 'pending' : 'not_applicable',
    );
}

function asAsyncDatabase(
  database: NodeDatabase,
  options: { failOnBatchItem?: number } = {},
): UploadMaterializerDatabase {
  return {
    withExclusiveTransactionAsync: async (task) => {
      database.exec('BEGIN IMMEDIATE;');
      let batchItemInsert = 0;
      const transaction: UploadMaterializerTransaction = {
        getFirstAsync: async <T,>(source: string, ...params: SqlValue[]) =>
          (database.prepare(source).get(...params) as T | undefined) ?? null,
        getAllAsync: async <T,>(source: string, ...params: SqlValue[]) =>
          database.prepare(source).all(...params) as T[],
        runAsync: async (source: string, ...params: SqlValue[]) => {
          if (source.includes('INSERT INTO telemetry_upload_batch_item')) {
            batchItemInsert += 1;
            if (batchItemInsert === options.failOnBatchItem) {
              throw new Error('UPLOAD_BATCH_ITEM_WRITE_FAILED');
            }
          }
          return database.prepare(source).run(...params);
        },
      };

      try {
        await task(transaction);
        database.exec('COMMIT;');
      } catch (error) {
        database.exec('ROLLBACK;');
        throw error;
      }
    },
  };
}

function sha256(body: string): string {
  return createHash('sha256').update(body, 'utf8').digest('hex');
}

function dependencies(
  input: Partial<UploadMaterializerDependencies> = {},
): UploadMaterializerDependencies {
  return {
    createClientBatchId: input.createClientBatchId ?? (() => ids.batchA),
    now: input.now ?? (() => now),
    sha256: input.sha256 ?? (async (body) => sha256(body)),
  };
}

describe('telemetry upload batch materializer', () => {
  it('atomically persists a canonical, ordered, exact-body batch', async () => {
    const database = openDatabase();
    insertSession(database, { sessionId: ids.sessionA, eligibility: 'server_bound' });
    insertLocation(database, {
      eventId: numberedUuid(2),
      sessionId: ids.sessionA,
      sequence: 1,
      altitude: 11.5,
      speed: 1.25,
      heading: 90,
      isMockLocation: 1,
    });
    insertLocation(database, {
      eventId: numberedUuid(1),
      sessionId: ids.sessionA,
      sequence: 0,
      accuracy: null,
      isMockLocation: 0,
    });
    let hashedBody: string | undefined;

    const result = await materializeNextUploadBatchCore(
      asAsyncDatabase(database),
      dependencies({
        sha256: async (body) => {
          hashedBody = body;
          return sha256(body);
        },
      }),
    );

    expect(result).toEqual({
      kind: 'created',
      batch: {
        clientBatchId: ids.batchA,
        sessionId: ids.sessionA,
        sampleCount: 2,
        state: 'pending',
      },
    });
    expect(result).not.toHaveProperty('batch.body');
    const stored = database
      .prepare(
        `SELECT body_json, body_sha256, sample_count, state
         FROM telemetry_upload_batch WHERE client_batch_id = ?`,
      )
      .get(ids.batchA) as {
      body_json: string;
      body_sha256: string;
      sample_count: number;
      state: string;
    };
    expect(stored.body_json).toBe(hashedBody);
    expect(stored.body_sha256).toBe(sha256(stored.body_json));
    expect(stored.sample_count).toBe(2);
    const body = JSON.parse(stored.body_json) as {
      deviceId: string;
      tripId: string;
      samples: Array<Record<string, unknown>>;
    };
    expect(body.deviceId).toBe(ids.device);
    expect(body.tripId).toBe(ids.tripA);
    expect(body.samples.map((sample) => sample.sequence)).toEqual([0, 1]);
    expect(body.samples.map((sample) => sample.isMockLocation)).toEqual([false, true]);
    expect(body.samples[0].horizontalAccuracyM).toBeNull();
    expect(body.samples[1]).toMatchObject({
      altitudeM: 11.5,
      speedMps: 1.25,
      headingDegrees: 90,
      activityHint: 'unknown',
      source: 'phone_gps',
    });
    expect(
      database
        .prepare(
          `SELECT position, event_id FROM telemetry_upload_batch_item
           ORDER BY position ASC`,
        )
        .all(),
    ).toEqual([
      { position: 0, event_id: numberedUuid(1) },
      { position: 1, event_id: numberedUuid(2) },
    ]);
    expect(
      database.prepare(`SELECT state FROM outbox_delivery ORDER BY event_id`).all(),
    ).toEqual([{ state: 'batched' }, { state: 'batched' }]);

    database.close();
  });

  it('rediscovers the stored active batch without regenerating its body', async () => {
    const database = openDatabase();
    insertSession(database, { sessionId: ids.sessionA, eligibility: 'server_bound' });
    insertLocation(database, {
      eventId: numberedUuid(1),
      sessionId: ids.sessionA,
      sequence: 0,
    });
    await materializeNextUploadBatchCore(asAsyncDatabase(database), dependencies());
    const before = database
      .prepare(
        `SELECT body_json, body_sha256, created_at FROM telemetry_upload_batch
         WHERE client_batch_id = ?`,
      )
      .get(ids.batchA);
    const mustNotRun = () => {
      throw new Error('DEPENDENCY_MUST_NOT_RUN');
    };

    const result = await materializeNextUploadBatchCore(asAsyncDatabase(database), {
      createClientBatchId: mustNotRun,
      now: mustNotRun,
      sha256: async () => mustNotRun(),
    });

    expect(result).toEqual({
      kind: 'existing',
      batch: {
        clientBatchId: ids.batchA,
        sessionId: ids.sessionA,
        sampleCount: 1,
        state: 'pending',
      },
    });
    expect(
      database
        .prepare(
          `SELECT body_json, body_sha256, created_at FROM telemetry_upload_batch
           WHERE client_batch_id = ?`,
        )
        .get(ids.batchA),
    ).toEqual(before);
    expect(database.prepare(`SELECT COUNT(*) AS count FROM telemetry_upload_batch`).get()).toEqual({
      count: 1,
    });

    database.close();
  });

  it('caps one single-flight batch at 500 samples and leaves the remainder pending', async () => {
    const database = openDatabase();
    insertSession(database, { sessionId: ids.sessionA, eligibility: 'server_bound' });
    database.exec('BEGIN;');
    for (let sequence = 500; sequence >= 0; sequence -= 1) {
      insertLocation(database, {
        eventId: numberedUuid(sequence + 1),
        sessionId: ids.sessionA,
        sequence,
      });
    }
    database.exec('COMMIT;');

    const result = await materializeNextUploadBatchCore(
      asAsyncDatabase(database),
      dependencies(),
    );

    expect(result.kind === 'created' ? result.batch.sampleCount : null).toBe(500);
    expect(
      database
        .prepare(`SELECT COUNT(*) AS count FROM outbox_delivery WHERE state = 'pending'`)
        .get(),
    ).toEqual({ count: 1 });
    const stored = database
      .prepare(`SELECT body_json FROM telemetry_upload_batch WHERE client_batch_id = ?`)
      .get(ids.batchA) as { body_json: string };
    const body = JSON.parse(stored.body_json) as { samples: Array<{ sequence: number }> };
    expect(body.samples).toHaveLength(500);
    expect(body.samples[0].sequence).toBe(0);
    expect(body.samples[499].sequence).toBe(499);

    database.close();
  });

  it('never mixes sessions and ignores development-local-only data', async () => {
    const database = openDatabase();
    insertSession(database, {
      sessionId: ids.sessionA,
      eligibility: 'server_bound',
      tripId: ids.tripA,
      startedAt: '2026-07-23T07:00:00.000Z',
    });
    insertSession(database, {
      sessionId: ids.sessionB,
      eligibility: 'server_bound',
      tripId: ids.tripB,
      startedAt: '2026-07-23T07:30:00.000Z',
    });
    insertSession(database, {
      sessionId: ids.localSession,
      eligibility: 'development_local_only',
    });
    insertLocation(database, {
      eventId: numberedUuid(1),
      sessionId: ids.sessionA,
      sequence: 0,
      createdAt: '2026-07-23T07:00:00.000Z',
    });
    insertLocation(database, {
      eventId: numberedUuid(2),
      sessionId: ids.sessionB,
      sequence: 0,
      createdAt: '2026-07-23T07:30:00.000Z',
    });
    insertLocation(database, {
      eventId: numberedUuid(3),
      sessionId: ids.localSession,
      sequence: 0,
      eligibility: 'development_local_only',
    });

    const result = await materializeNextUploadBatchCore(
      asAsyncDatabase(database),
      dependencies(),
    );

    expect(result.kind === 'created' ? result.batch.sessionId : null).toBe(ids.sessionA);
    expect(
      database.prepare(`SELECT event_id, state FROM outbox_delivery ORDER BY event_id`).all(),
    ).toEqual([
      { event_id: numberedUuid(1), state: 'batched' },
      { event_id: numberedUuid(2), state: 'pending' },
      { event_id: numberedUuid(3), state: 'not_applicable' },
    ]);

    database.close();
  });

  it('returns none without invoking generators when no uploadable data exists', async () => {
    const database = openDatabase();
    insertSession(database, {
      sessionId: ids.localSession,
      eligibility: 'development_local_only',
    });
    insertLocation(database, {
      eventId: numberedUuid(1),
      sessionId: ids.localSession,
      sequence: 0,
      eligibility: 'development_local_only',
    });
    const mustNotRun = () => {
      throw new Error('DEPENDENCY_MUST_NOT_RUN');
    };

    await expect(
      materializeNextUploadBatchCore(asAsyncDatabase(database), {
        createClientBatchId: mustNotRun,
        now: mustNotRun,
        sha256: async () => mustNotRun(),
      }),
    ).resolves.toEqual({ kind: 'none' });

    database.close();
  });

  it('fails before reading work when transaction foreign keys are disabled', async () => {
    const database = openDatabase();
    database.exec('PRAGMA foreign_keys = OFF;');

    await expect(
      materializeNextUploadBatchCore(asAsyncDatabase(database), dependencies()),
    ).rejects.toThrow('UPLOAD_DATABASE_FOREIGN_KEYS_DISABLED');

    database.close();
  });

  it('rolls back batch, items, and outbox when an item write fails', async () => {
    const database = openDatabase();
    insertSession(database, { sessionId: ids.sessionA, eligibility: 'server_bound' });
    for (let sequence = 0; sequence < 3; sequence += 1) {
      insertLocation(database, {
        eventId: numberedUuid(sequence + 1),
        sessionId: ids.sessionA,
        sequence,
      });
    }

    await expect(
      materializeNextUploadBatchCore(
        asAsyncDatabase(database, { failOnBatchItem: 2 }),
        dependencies(),
      ),
    ).rejects.toThrow('UPLOAD_BATCH_ITEM_WRITE_FAILED');
    expect(database.prepare(`SELECT COUNT(*) AS count FROM telemetry_upload_batch`).get()).toEqual({
      count: 0,
    });
    expect(
      database.prepare(`SELECT COUNT(*) AS count FROM telemetry_upload_batch_item`).get(),
    ).toEqual({ count: 0 });
    expect(
      database.prepare(`SELECT state FROM outbox_delivery ORDER BY event_id`).all(),
    ).toEqual([{ state: 'pending' }, { state: 'pending' }, { state: 'pending' }]);

    database.close();
  });

  it('fails closed on malformed digests and never includes coordinates in the error', async () => {
    const database = openDatabase();
    insertSession(database, { sessionId: ids.sessionA, eligibility: 'server_bound' });
    insertLocation(database, {
      eventId: numberedUuid(1),
      sessionId: ids.sessionA,
      sequence: 0,
      latitude: 37.1234567,
      longitude: 127.7654321,
    });

    let caught: unknown;
    try {
      await materializeNextUploadBatchCore(
        asAsyncDatabase(database),
        dependencies({ sha256: async () => 'A'.repeat(64) }),
      );
    } catch (error) {
      caught = error;
    }

    expect(caught).toBeInstanceOf(Error);
    expect((caught as Error).message).toBe('UPLOAD_BATCH_DIGEST_INVALID');
    expect((caught as Error).message).not.toContain('37.1234567');
    expect((caught as Error).message).not.toContain('127.7654321');
    expect(database.prepare(`SELECT COUNT(*) AS count FROM telemetry_upload_batch`).get()).toEqual({
      count: 0,
    });
    expect(database.prepare(`SELECT state FROM outbox_delivery`).get()).toEqual({
      state: 'pending',
    });

    database.close();
  });
});
