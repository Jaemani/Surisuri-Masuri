// @ts-expect-error Node 22 provides this test-only module; the Expo app intentionally excludes Node types.
import { createHash } from 'node:crypto';
// @ts-expect-error Node 22 provides this test-only module; the Expo app intentionally excludes Node types.
import { DatabaseSync } from 'node:sqlite';

import { describe, expect, it } from 'vitest';

import { CREATE_TELEMETRY_SCHEMA_V3_SQL } from './databaseSchema';
import { buildImmutableTelemetryBatch } from './syncProtocol';
import {
  leaseNextUploadBatchCore,
  type UploadLeaseDatabase,
  type UploadLeaseDependencies,
  type UploadLeaseTransaction,
} from './uploadLease';

type NodeDatabase = InstanceType<typeof DatabaseSync>;
type SqlValue = string | number | null;

const ids = {
  session: '40000000-0000-4000-8000-000000000001',
  installation: '40000000-0000-4000-8000-000000000002',
  tenant: '40000000-0000-4000-8000-000000000003',
  device: '40000000-0000-4000-8000-000000000004',
  trip: '40000000-0000-4000-8000-000000000005',
  consent: '40000000-0000-4000-8000-000000000006',
  batch: '40000000-0000-4000-8000-000000000007',
  leaseOwner: '40000000-0000-4000-8000-000000000008',
  oldLeaseOwner: '40000000-0000-4000-8000-000000000009',
};

const createdAt = '2026-07-23T08:00:00.000Z';
const now = '2026-07-23T08:05:00.000Z';
const leaseExpiresAt = '2026-07-23T08:07:00.000Z';

function numberedUuid(sequence: number): string {
  return `50000000-0000-4000-8000-${String(sequence).padStart(12, '0')}`;
}

function sha256(body: string): string {
  return createHash('sha256').update(body, 'utf8').digest('hex');
}

function openDatabase(): NodeDatabase {
  const database = new DatabaseSync(':memory:');
  database.exec('PRAGMA foreign_keys = ON;');
  database.exec(CREATE_TELEMETRY_SCHEMA_V3_SQL);
  return database;
}

function seedBatch(
  database: NodeDatabase,
  input: {
    sampleCount?: number;
    itemCount?: number;
    bodyTransform?: (body: string) => string;
    digest?: (body: string, originalBody: string) => string;
  } = {},
): { body: string; digest: string; eventIds: string[]; originalBody: string } {
  const sampleCount = input.sampleCount ?? 1;
  const itemCount = input.itemCount ?? sampleCount;
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

  const eventIds: string[] = [];
  for (let sequence = 0; sequence < sampleCount; sequence += 1) {
    const eventId = numberedUuid(sequence + 1);
    eventIds.push(eventId);
    database
      .prepare(
        `INSERT INTO trip_event_log (
           event_id, session_id, event_sequence, sample_sequence, event_type,
           occurred_at, latitude, longitude, horizontal_accuracy_m,
           altitude_m, speed_mps, heading_degrees, is_mock_location,
           payload_json, created_at
         ) VALUES (?, ?, ?, ?, 'location_sample', ?, ?, ?, 5, NULL, NULL, NULL, 1, '{}', ?)`,
      )
      .run(
        eventId,
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
      .run(eventId);
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
      isMockLocation: true,
    })),
  });
  const body = input.bodyTransform?.(immutable.body) ?? immutable.body;
  const digest = input.digest?.(body, immutable.body) ?? sha256(body);
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
      digest,
      sampleCount,
      createdAt,
      createdAt,
    );

  for (let position = 0; position < itemCount; position += 1) {
    database
      .prepare(
        `INSERT INTO telemetry_upload_batch_item (
           client_batch_id, session_id, position, event_id
         ) VALUES (?, ?, ?, ?)`,
      )
      .run(ids.batch, ids.session, position, eventIds[position]);
  }

  return { body, digest, eventIds, originalBody: immutable.body };
}

function transactionFor(database: NodeDatabase): UploadLeaseTransaction {
  return {
    getFirstAsync: async <T,>(source: string, ...params: SqlValue[]) =>
      (database.prepare(source).get(...params) as T | undefined) ?? null,
    runAsync: async (source: string, ...params: SqlValue[]) => {
      const result = database.prepare(source).run(...params);
      return { changes: Number(result.changes) };
    },
  };
}

function asAsyncDatabase(database: NodeDatabase): UploadLeaseDatabase {
  return {
    withExclusiveTransactionAsync: async (task) => {
      database.exec('BEGIN IMMEDIATE;');
      try {
        await task(transactionFor(database));
        database.exec('COMMIT;');
      } catch (error) {
        database.exec('ROLLBACK;');
        throw error;
      }
    },
  };
}

function asSerializedAsyncDatabase(database: NodeDatabase): UploadLeaseDatabase {
  let tail = Promise.resolve();
  return {
    withExclusiveTransactionAsync: (task) => {
      const run = tail.then(async () => {
        database.exec('BEGIN IMMEDIATE;');
        try {
          await task(transactionFor(database));
          database.exec('COMMIT;');
        } catch (error) {
          database.exec('ROLLBACK;');
          throw error;
        }
      });
      tail = run.catch(() => undefined);
      return run;
    },
  };
}

function dependencies(
  input: Partial<UploadLeaseDependencies> = {},
): UploadLeaseDependencies {
  return {
    createLeaseOwnerId: input.createLeaseOwnerId ?? (() => ids.leaseOwner),
    leaseExpiresAt: input.leaseExpiresAt ?? (() => leaseExpiresAt),
    now: input.now ?? (() => now),
    sha256: input.sha256 ?? (async (body) => sha256(body)),
  };
}

describe('telemetry upload batch lease', () => {
  it('returns none without invoking clock or authority providers when no batch exists', async () => {
    const database = openDatabase();
    const mustNotRun = () => {
      throw new Error('DEPENDENCY_MUST_NOT_RUN');
    };

    await expect(
      leaseNextUploadBatchCore(asAsyncDatabase(database), {
        createLeaseOwnerId: mustNotRun,
        leaseExpiresAt: mustNotRun,
        now: mustNotRun,
        sha256: async () => mustNotRun(),
      }),
    ).resolves.toEqual({ kind: 'none' });
    database.close();
  });

  it('rehashes the exact stored body and atomically grants a bounded lease', async () => {
    const database = openDatabase();
    const seeded = seedBatch(database);
    let hashedBody: string | undefined;

    const result = await leaseNextUploadBatchCore(
      asAsyncDatabase(database),
      dependencies({
        sha256: async (body) => {
          hashedBody = body;
          return sha256(body);
        },
      }),
    );

    expect(hashedBody).toBe(seeded.body);
    expect(result).toEqual({
      kind: 'leased',
      lease: {
        clientBatchId: ids.batch,
        sessionId: ids.session,
        sampleCount: 1,
        attemptCount: 1,
        leaseOwnerId: ids.leaseOwner,
        leaseExpiresAt,
        body: seeded.body,
        bodySha256: seeded.digest,
      },
    });
    expect(
      database
        .prepare(
          `SELECT state, attempt_count, lease_owner_id, lease_expires_at,
                  body_json, body_sha256
           FROM telemetry_upload_batch`,
        )
        .get(),
    ).toEqual({
      state: 'leased',
      attempt_count: 1,
      lease_owner_id: ids.leaseOwner,
      lease_expires_at: leaseExpiresAt,
      body_json: seeded.body,
      body_sha256: seeded.digest,
    });
    expect(database.prepare(`SELECT state FROM outbox_delivery`).get()).toEqual({
      state: 'batched',
    });
    database.close();
  });

  it('holds the batch and all bound outbox rows on a digest mismatch', async () => {
    const database = openDatabase();
    seedBatch(database, { digest: () => '0'.repeat(64) });

    const result = await leaseNextUploadBatchCore(
      asAsyncDatabase(database),
      dependencies(),
    );

    expect(result).toEqual({
      kind: 'held',
      clientBatchId: ids.batch,
      reason: 'local_body_digest_mismatch',
    });
    expect(
      database
        .prepare(
          `SELECT state, attempt_count, lease_owner_id, lease_expires_at,
                  last_error_code
           FROM telemetry_upload_batch`,
        )
        .get(),
    ).toEqual({
      state: 'held',
      attempt_count: 0,
      lease_owner_id: null,
      lease_expires_at: null,
      last_error_code: 'local_body_digest_mismatch',
    });
    expect(
      database.prepare(`SELECT state, last_error_code FROM outbox_delivery`).get(),
    ).toEqual({ state: 'held', last_error_code: 'local_body_digest_mismatch' });
    expect(JSON.stringify(result)).not.toContain('37.5');
    expect(JSON.stringify(result)).not.toContain('127');
    database.close();
  });

  it('durably holds a malformed stored digest without invoking the hash provider', async () => {
    const database = openDatabase();
    seedBatch(database, { digest: () => 'A'.repeat(64) });

    await expect(
      leaseNextUploadBatchCore(
        asAsyncDatabase(database),
        dependencies({
          sha256: async () => {
            throw new Error('HASH_PROVIDER_MUST_NOT_RUN');
          },
        }),
      ),
    ).resolves.toMatchObject({ kind: 'held' });
    expect(database.prepare(`SELECT state FROM telemetry_upload_batch`).get()).toEqual({
      state: 'held',
    });
    database.close();
  });

  it('hashes stored bytes without parsing or reserializing JSON', async () => {
    const database = openDatabase();
    const seeded = seedBatch(database, {
      bodyTransform: (body) => `${body}\n`,
      digest: (_body, originalBody) => sha256(originalBody),
    });
    let hashedBody: string | undefined;

    const result = await leaseNextUploadBatchCore(
      asAsyncDatabase(database),
      dependencies({
        sha256: async (body) => {
          hashedBody = body;
          return sha256(body);
        },
      }),
    );

    expect(seeded.body).not.toBe(seeded.originalBody);
    expect(hashedBody).toBe(seeded.body);
    expect(result.kind).toBe('held');
    database.close();
  });

  it('rolls back without hold or attempt mutation when the hash provider fails', async () => {
    const database = openDatabase();
    seedBatch(database);

    await expect(
      leaseNextUploadBatchCore(
        asAsyncDatabase(database),
        dependencies({
          sha256: async () => {
            throw new Error('provider unavailable');
          },
        }),
      ),
    ).rejects.toThrow('UPLOAD_BATCH_DIGEST_FAILED');
    expect(
      database
        .prepare(
          `SELECT state, attempt_count, lease_owner_id, last_error_code
           FROM telemetry_upload_batch`,
        )
        .get(),
    ).toEqual({
      state: 'pending',
      attempt_count: 0,
      lease_owner_id: null,
      last_error_code: null,
    });
    expect(database.prepare(`SELECT state FROM outbox_delivery`).get()).toEqual({
      state: 'batched',
    });
    database.close();
  });

  it.each([
    ['positive real', `UPDATE telemetry_upload_batch SET attempt_count = 1.5`],
    ['text', `UPDATE telemetry_upload_batch SET attempt_count = 'abc'`],
    [
      'unsafe 64-bit integer',
      `UPDATE telemetry_upload_batch SET attempt_count = 9223372036854775807`,
    ],
  ])('holds malformed persisted attempt metadata: %s', async (_name, updateSource) => {
    const database = openDatabase();
    seedBatch(database);
    database.exec(updateSource);

    const result = await leaseNextUploadBatchCore(
      asAsyncDatabase(database),
      dependencies({
        sha256: async () => {
          throw new Error('HASH_PROVIDER_MUST_NOT_RUN');
        },
      }),
    );

    expect(result).toEqual({
      kind: 'held',
      clientBatchId: ids.batch,
      reason: 'local_attempt_metadata_invalid',
    });
    expect(
      database
        .prepare(
          `SELECT state, last_error_code, CAST(attempt_count AS TEXT) AS attempt_count_text
           FROM telemetry_upload_batch`,
        )
        .get(),
    ).toMatchObject({
      state: 'held',
      last_error_code: 'local_attempt_metadata_invalid',
    });
    expect(database.prepare(`SELECT state FROM outbox_delivery`).get()).toEqual({
      state: 'held',
    });
    database.close();
  });

  it('rejects a malformed provider digest without treating it as stored corruption', async () => {
    const database = openDatabase();
    seedBatch(database);

    await expect(
      leaseNextUploadBatchCore(
        asAsyncDatabase(database),
        dependencies({ sha256: async () => 'A'.repeat(64) }),
      ),
    ).rejects.toThrow('UPLOAD_BATCH_DIGEST_INVALID');
    expect(database.prepare(`SELECT state, attempt_count FROM telemetry_upload_batch`).get()).toEqual({
      state: 'pending',
      attempt_count: 0,
    });
    database.close();
  });

  it('does not hash or mutate a pending batch before its backoff is due', async () => {
    const database = openDatabase();
    seedBatch(database);
    database
      .prepare(`UPDATE telemetry_upload_batch SET next_attempt_at = ?`)
      .run('2026-07-23T08:06:00.000Z');

    const result = await leaseNextUploadBatchCore(
      asAsyncDatabase(database),
      dependencies({
        sha256: async () => {
          throw new Error('HASH_PROVIDER_MUST_NOT_RUN');
        },
      }),
    );

    expect(result).toEqual({ kind: 'none' });
    expect(database.prepare(`SELECT state, attempt_count FROM telemetry_upload_batch`).get()).toEqual({
      state: 'pending',
      attempt_count: 0,
    });
    database.close();
  });

  it('holds malformed persisted retry metadata instead of comparing it as text', async () => {
    const database = openDatabase();
    seedBatch(database);
    database
      .prepare(`UPDATE telemetry_upload_batch SET next_attempt_at = 'invalid-high-value'`)
      .run();

    const result = await leaseNextUploadBatchCore(
      asAsyncDatabase(database),
      dependencies({
        sha256: async () => {
          throw new Error('HASH_PROVIDER_MUST_NOT_RUN');
        },
      }),
    );

    expect(result).toEqual({
      kind: 'held',
      clientBatchId: ids.batch,
      reason: 'local_retry_metadata_invalid',
    });
    expect(database.prepare(`SELECT state, last_error_code FROM telemetry_upload_batch`).get()).toEqual({
      state: 'held',
      last_error_code: 'local_retry_metadata_invalid',
    });
    expect(database.prepare(`SELECT state FROM outbox_delivery`).get()).toEqual({
      state: 'held',
    });
    database.close();
  });

  it('does not take over an unexpired lease', async () => {
    const database = openDatabase();
    seedBatch(database);
    database
      .prepare(
        `UPDATE telemetry_upload_batch
         SET state = 'leased', attempt_count = 1,
             lease_owner_id = ?, lease_expires_at = ?, updated_at = ?`,
      )
      .run(ids.oldLeaseOwner, '2026-07-23T08:06:00.000Z', createdAt);

    const result = await leaseNextUploadBatchCore(
      asAsyncDatabase(database),
      dependencies({
        sha256: async () => {
          throw new Error('HASH_PROVIDER_MUST_NOT_RUN');
        },
      }),
    );

    expect(result).toEqual({ kind: 'none' });
    expect(
      database.prepare(`SELECT attempt_count, lease_owner_id FROM telemetry_upload_batch`).get(),
    ).toEqual({ attempt_count: 1, lease_owner_id: ids.oldLeaseOwner });
    database.close();
  });

  it.each(['0', 'invalid-high-value']) (
    'holds malformed persisted lease expiry %s without granting authority',
    async (invalidExpiry) => {
      const database = openDatabase();
      seedBatch(database);
      database
        .prepare(
          `UPDATE telemetry_upload_batch
           SET state = 'leased', attempt_count = 1,
               lease_owner_id = ?, lease_expires_at = ?, updated_at = ?`,
        )
        .run(ids.oldLeaseOwner, invalidExpiry, createdAt);

      const result = await leaseNextUploadBatchCore(
        asAsyncDatabase(database),
        dependencies({
          sha256: async () => {
            throw new Error('HASH_PROVIDER_MUST_NOT_RUN');
          },
        }),
      );

      expect(result).toEqual({
        kind: 'held',
        clientBatchId: ids.batch,
        reason: 'local_lease_metadata_invalid',
      });
      expect(
        database.prepare(`SELECT state, attempt_count, last_error_code FROM telemetry_upload_batch`).get(),
      ).toEqual({
        state: 'held',
        attempt_count: 1,
        last_error_code: 'local_lease_metadata_invalid',
      });
      database.close();
    },
  );

  it('holds a malformed persisted lease owner without hashing the body', async () => {
    const database = openDatabase();
    seedBatch(database);
    database
      .prepare(
        `UPDATE telemetry_upload_batch
         SET state = 'leased', attempt_count = 1,
             lease_owner_id = 'not-a-uuid', lease_expires_at = ?, updated_at = ?`,
      )
      .run('2026-07-23T08:04:59.000Z', createdAt);

    const result = await leaseNextUploadBatchCore(
      asAsyncDatabase(database),
      dependencies({
        sha256: async () => {
          throw new Error('HASH_PROVIDER_MUST_NOT_RUN');
        },
      }),
    );

    expect(result).toMatchObject({ kind: 'held', reason: 'local_lease_metadata_invalid' });
    expect(database.prepare(`SELECT state FROM outbox_delivery`).get()).toEqual({
      state: 'held',
    });
    database.close();
  });

  it('rehashes and takes over an expired lease exactly once', async () => {
    const database = openDatabase();
    const seeded = seedBatch(database);
    database
      .prepare(
        `UPDATE telemetry_upload_batch
         SET state = 'leased', attempt_count = 1,
             lease_owner_id = ?, lease_expires_at = ?, updated_at = ?`,
      )
      .run(ids.oldLeaseOwner, '2026-07-23T08:04:59.000Z', createdAt);

    const result = await leaseNextUploadBatchCore(
      asAsyncDatabase(database),
      dependencies(),
    );

    expect(result).toMatchObject({
      kind: 'leased',
      lease: {
        attemptCount: 2,
        leaseOwnerId: ids.leaseOwner,
        body: seeded.body,
      },
    });
    expect(
      database
        .prepare(`SELECT state, attempt_count, lease_owner_id FROM telemetry_upload_batch`)
        .get(),
    ).toEqual({
      state: 'leased',
      attempt_count: 2,
      lease_owner_id: ids.leaseOwner,
    });
    database.close();
  });

  it('treats an existing lease expiring exactly now as due', async () => {
    const database = openDatabase();
    seedBatch(database);
    database
      .prepare(
        `UPDATE telemetry_upload_batch
         SET state = 'leased', attempt_count = 1,
             lease_owner_id = ?, lease_expires_at = ?, updated_at = ?`,
      )
      .run(ids.oldLeaseOwner, now, createdAt);

    const result = await leaseNextUploadBatchCore(
      asAsyncDatabase(database),
      dependencies(),
    );

    expect(result).toMatchObject({ kind: 'leased', lease: { attemptCount: 2 } });
    database.close();
  });

  it.each([
    ['invalid owner', { createLeaseOwnerId: (): string => 'not-a-uuid' }, 'UPLOAD_LEASE_OWNER_INVALID'],
    ['same-time expiry', { leaseExpiresAt: (): string => now }, 'UPLOAD_LEASE_EXPIRY_INVALID'],
    [
      'past expiry',
      { leaseExpiresAt: (): string => '2026-07-23T08:04:59.999Z' },
      'UPLOAD_LEASE_EXPIRY_INVALID',
    ],
    [
      'overlong expiry',
      { leaseExpiresAt: (): string => '2026-07-23T08:10:00.001Z' },
      'UPLOAD_LEASE_EXPIRY_INVALID',
    ],
    [
      'noncanonical expiry',
      { leaseExpiresAt: (): string => '2026-07-23T08:07:00Z' },
      'UPLOAD_LEASE_EXPIRY_INVALID',
    ],
  ] as const)(
    'rolls back a lease with %s',
    async (_name, dependencyOverride, expectedError) => {
      const database = openDatabase();
      seedBatch(database);

      await expect(
        leaseNextUploadBatchCore(
          asAsyncDatabase(database),
          dependencies(dependencyOverride),
        ),
      ).rejects.toThrow(expectedError);
      expect(database.prepare(`SELECT state, attempt_count FROM telemetry_upload_batch`).get()).toEqual({
        state: 'pending',
        attempt_count: 0,
      });
      database.close();
    },
  );

  it('accepts a lease window at the exact five-minute boundary', async () => {
    const database = openDatabase();
    seedBatch(database);

    const result = await leaseNextUploadBatchCore(
      asAsyncDatabase(database),
      dependencies({ leaseExpiresAt: () => '2026-07-23T08:10:00.000Z' }),
    );

    expect(result).toMatchObject({
      kind: 'leased',
      lease: { leaseExpiresAt: '2026-07-23T08:10:00.000Z' },
    });
    database.close();
  });

  it('rejects a noncanonical current time before selecting authority', async () => {
    const database = openDatabase();
    seedBatch(database);

    await expect(
      leaseNextUploadBatchCore(
        asAsyncDatabase(database),
        dependencies({ now: () => '2026-07-23T08:05:00Z' }),
      ),
    ).rejects.toThrow('UPLOAD_LEASE_NOW_INVALID');
    expect(database.prepare(`SELECT state, attempt_count FROM telemetry_upload_batch`).get()).toEqual({
      state: 'pending',
      attempt_count: 0,
    });
    database.close();
  });

  it('ignores terminal batches without invoking lease dependencies', async () => {
    const database = openDatabase();
    seedBatch(database);
    database
      .prepare(
        `UPDATE telemetry_upload_batch
         SET state = 'held', last_error_code = 'operator_hold', updated_at = ?`,
      )
      .run(now);
    database
      .prepare(
        `UPDATE outbox_delivery
         SET state = 'held', last_error_code = 'operator_hold'`,
      )
      .run();

    const result = await leaseNextUploadBatchCore(
      asAsyncDatabase(database),
      dependencies({
        sha256: async () => {
          throw new Error('HASH_PROVIDER_MUST_NOT_RUN');
        },
      }),
    );

    expect(result).toEqual({ kind: 'none' });
    database.close();
  });

  it('rolls back the lease when schema cardinality enforcement rejects it', async () => {
    const database = openDatabase();
    seedBatch(database, { sampleCount: 2, itemCount: 1 });

    await expect(
      leaseNextUploadBatchCore(asAsyncDatabase(database), dependencies()),
    ).rejects.toThrow(/UPLOAD_BATCH_CARDINALITY_MISMATCH/);
    expect(database.prepare(`SELECT state, attempt_count FROM telemetry_upload_batch`).get()).toEqual({
      state: 'pending',
      attempt_count: 0,
    });
    expect(database.prepare(`SELECT state FROM outbox_delivery ORDER BY event_id`).all()).toEqual([
      { state: 'batched' },
      { state: 'pending' },
    ]);
    database.close();
  });

  it('serializes concurrent callers to one lease winner', async () => {
    const database = openDatabase();
    seedBatch(database);
    const serialized = asSerializedAsyncDatabase(database);
    let ownerCalls = 0;
    const sharedDependencies = dependencies({
      createLeaseOwnerId: () => {
        ownerCalls += 1;
        return ids.leaseOwner;
      },
    });

    const results = await Promise.all([
      leaseNextUploadBatchCore(serialized, sharedDependencies),
      leaseNextUploadBatchCore(serialized, sharedDependencies),
    ]);

    expect(results.map((result) => result.kind).sort()).toEqual(['leased', 'none']);
    expect(ownerCalls).toBe(1);
    expect(database.prepare(`SELECT attempt_count FROM telemetry_upload_batch`).get()).toEqual({
      attempt_count: 1,
    });
    database.close();
  });

  it('fails before work selection when transaction foreign keys are disabled', async () => {
    const database = openDatabase();
    seedBatch(database);
    database.exec('PRAGMA foreign_keys = OFF;');

    await expect(
      leaseNextUploadBatchCore(asAsyncDatabase(database), dependencies()),
    ).rejects.toThrow('UPLOAD_DATABASE_FOREIGN_KEYS_DISABLED');
    expect(database.prepare(`SELECT state FROM telemetry_upload_batch`).get()).toEqual({
      state: 'pending',
    });
    database.close();
  });
});
