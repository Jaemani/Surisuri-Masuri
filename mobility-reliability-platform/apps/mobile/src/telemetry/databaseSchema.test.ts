// @ts-expect-error Node 22 provides this test-only module; the Expo app intentionally excludes Node types.
import { DatabaseSync } from 'node:sqlite';

import { describe, expect, it } from 'vitest';

import {
  CREATE_TELEMETRY_SCHEMA_V2_SQL,
  CURRENT_TELEMETRY_SCHEMA_VERSION,
  MIGRATE_TELEMETRY_V1_TO_V2_SQL,
} from './databaseSchema';
import { buildImmutableTelemetryBatch } from './syncProtocol';

const ids = {
  session: '10000000-0000-4000-8000-000000000001',
  installation: '10000000-0000-4000-8000-000000000002',
  tenant: '10000000-0000-4000-8000-000000000003',
  device: '10000000-0000-4000-8000-000000000004',
  trip: '10000000-0000-4000-8000-000000000005',
  consent: '10000000-0000-4000-8000-000000000006',
  event: '10000000-0000-4000-8000-000000000007',
  batch: '10000000-0000-4000-8000-000000000008',
  secondEvent: '10000000-0000-4000-8000-000000000009',
  receipt: '10000000-0000-7000-8000-000000000010',
  serverBatch: '10000000-0000-7000-8000-000000000011',
  leaseOwner: '10000000-0000-4000-8000-000000000012',
};

const now = '2026-07-23T04:00:00.000Z';

function openDatabase(): InstanceType<typeof DatabaseSync> {
  const database = new DatabaseSync(':memory:');
  database.exec('PRAGMA foreign_keys = ON;');
  return database;
}

function insertSession(
  database: InstanceType<typeof DatabaseSync>,
  eligibility: 'development_local_only' | 'server_bound',
): void {
  database
    .prepare(
      `INSERT INTO trip_session_projection (
        session_id, installation_id, tenant_id, mobility_device_id,
        server_trip_id, consent_revision_id, upload_eligibility,
        started_at, state, updated_at
      ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'recording', ?)`,
    )
    .run(
      ids.session,
      ids.installation,
      eligibility === 'server_bound' ? ids.tenant : null,
      eligibility === 'server_bound' ? ids.device : null,
      eligibility === 'server_bound' ? ids.trip : null,
      eligibility === 'server_bound' ? ids.consent : null,
      eligibility,
      now,
      now,
    );
}

function createV1Database(database: InstanceType<typeof DatabaseSync>): void {
  database.exec(`
    CREATE TABLE trip_session_projection (
      session_id TEXT PRIMARY KEY NOT NULL,
      installation_id TEXT NOT NULL,
      tenant_id TEXT,
      actor_id TEXT,
      mobility_device_id TEXT,
      consent_version TEXT,
      upload_eligibility TEXT NOT NULL CHECK (upload_eligibility IN ('development_local_only')),
      started_at TEXT NOT NULL,
      ended_at TEXT,
      state TEXT NOT NULL CHECK (state IN ('recording', 'stopped')),
      next_event_sequence INTEGER NOT NULL DEFAULT 0,
      next_sample_sequence INTEGER NOT NULL DEFAULT 0,
      accepted_sample_count INTEGER NOT NULL DEFAULT 0,
      rejected_sample_count INTEGER NOT NULL DEFAULT 0,
      last_sample_at TEXT,
      updated_at TEXT NOT NULL
    );
    CREATE UNIQUE INDEX one_recording_session
      ON trip_session_projection(state) WHERE state = 'recording';
    CREATE TABLE trip_event_log (
      event_id TEXT PRIMARY KEY NOT NULL,
      session_id TEXT NOT NULL,
      event_sequence INTEGER NOT NULL,
      sample_sequence INTEGER,
      event_type TEXT NOT NULL,
      occurred_at TEXT NOT NULL,
      latitude REAL,
      longitude REAL,
      horizontal_accuracy_m REAL,
      altitude_m REAL,
      speed_mps REAL,
      heading_degrees REAL,
      is_mock_location INTEGER,
      payload_json TEXT NOT NULL,
      created_at TEXT NOT NULL,
      FOREIGN KEY (session_id) REFERENCES trip_session_projection(session_id),
      UNIQUE (session_id, event_sequence),
      UNIQUE (session_id, sample_sequence)
    );
    CREATE TABLE outbox_delivery (
      event_id TEXT PRIMARY KEY NOT NULL,
      state TEXT NOT NULL DEFAULT 'pending',
      attempt_count INTEGER NOT NULL DEFAULT 0,
      next_attempt_at TEXT,
      acknowledged_at TEXT,
      last_error_code TEXT,
      FOREIGN KEY (event_id) REFERENCES trip_event_log(event_id)
    );
    CREATE INDEX pending_outbox ON outbox_delivery(state, next_attempt_at);
    CREATE TABLE app_metadata (key TEXT PRIMARY KEY NOT NULL, value TEXT NOT NULL);
    PRAGMA user_version = 1;
  `);
}

function migrateV1LikeRuntime(database: InstanceType<typeof DatabaseSync>): void {
  database.exec('PRAGMA foreign_keys = OFF;');
  try {
    database.exec(`BEGIN IMMEDIATE;${MIGRATE_TELEMETRY_V1_TO_V2_SQL}`);
    if (database.prepare('PRAGMA foreign_key_check').all().length > 0) {
      throw new Error('DATABASE_FOREIGN_KEY_CHECK_FAILED');
    }
    database.exec('COMMIT;');
  } catch (error) {
    database.exec('ROLLBACK;');
    throw error;
  } finally {
    database.exec('PRAGMA foreign_keys = ON;');
  }
}

function insertLocationEvent(
  database: InstanceType<typeof DatabaseSync>,
  deliveryScope: 'local_only' | 'telemetry_upload',
): void {
  database
    .prepare(
      `INSERT INTO trip_event_log (
        event_id, session_id, event_sequence, sample_sequence, event_type,
        occurred_at, latitude, longitude, horizontal_accuracy_m, payload_json, created_at
      ) VALUES (?, ?, 0, 0, 'location_sample', ?, 37.5, 127.0, 5, '{}', ?)`,
    )
    .run(ids.event, ids.session, now, now);
  database
    .prepare(
      `INSERT INTO outbox_delivery (event_id, delivery_scope, state)
       VALUES (?, ?, ?)`,
    )
    .run(
      ids.event,
      deliveryScope,
      deliveryScope === 'telemetry_upload' ? 'pending' : 'not_applicable',
    );
}

function buildBatchBody(sampleCount = 1): string {
  return buildImmutableTelemetryBatch({
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
    samples: Array.from({ length: sampleCount }, (_, sequence) => ({
      clientSampleId:
        sequence === 0
          ? ids.event
          : sequence === 1
            ? ids.secondEvent
            : `10000000-0000-4000-8000-${String(sequence).padStart(12, '0')}`,
      sequence,
      capturedAt: now,
      latitude: 37.5,
      longitude: 127,
      horizontalAccuracyM: 5,
      altitudeM: null,
      speedMps: null,
      headingDegrees: null,
      isMockLocation: null,
    })),
  }).body;
}

function insertUploadBatch(
  database: InstanceType<typeof DatabaseSync>,
  sampleCount = 1,
): void {
  const body = buildBatchBody(sampleCount);
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
      sampleCount,
      now,
      now,
    );
}

describe('telemetry SQLite schema v2', () => {
  it('creates upload tables and enforces the server-bound scope union', () => {
    const database = openDatabase();
    database.exec(CREATE_TELEMETRY_SCHEMA_V2_SQL);

    expect(
      database.prepare('PRAGMA user_version').get() as { user_version: number },
    ).toEqual({ user_version: CURRENT_TELEMETRY_SCHEMA_VERSION });
    insertSession(database, 'server_bound');
    expect(() =>
      database
        .prepare(`UPDATE trip_session_projection SET session_id = ? WHERE session_id = ?`)
        .run(ids.secondEvent, ids.session),
    ).toThrow(/SESSION_UPLOAD_SCOPE_IMMUTABLE/);

    const missingScope = openDatabase();
    missingScope.exec(CREATE_TELEMETRY_SCHEMA_V2_SQL);
    expect(() =>
      missingScope
        .prepare(
          `INSERT INTO trip_session_projection (
            session_id, installation_id, upload_eligibility, started_at, state, updated_at
          ) VALUES (?, ?, 'server_bound', ?, 'recording', ?)`,
        )
        .run(ids.session, ids.installation, now, now),
    ).toThrow();

    database.close();
    missingScope.close();
  });

  it('rejects invalid delivery-state and batch-ack unions', () => {
    const database = openDatabase();
    database.exec(CREATE_TELEMETRY_SCHEMA_V2_SQL);
    insertSession(database, 'server_bound');
    database
      .prepare(
        `INSERT INTO trip_event_log (
          event_id, session_id, event_sequence, sample_sequence, event_type,
          occurred_at, latitude, longitude, horizontal_accuracy_m, payload_json, created_at
        ) VALUES (?, ?, 0, 0, 'location_sample', ?, 37.5, 127.0, 5, '{}', ?)`,
      )
      .run(ids.event, ids.session, now, now);

    expect(() =>
      database
        .prepare(
          `INSERT INTO outbox_delivery (event_id, delivery_scope, state)
           VALUES (?, 'local_only', 'pending')`,
        )
        .run(ids.event),
    ).toThrow();
    expect(() =>
      database
        .prepare(
          `INSERT INTO outbox_delivery (
            event_id, delivery_scope, state, acknowledged_at
          ) VALUES (?, 'telemetry_upload', 'acknowledged', ?)`,
        )
        .run(ids.event, now),
    ).toThrow(/OUTBOX_INITIAL_STATE_INVALID/);
    expect(() =>
      database
        .prepare(
          `INSERT INTO telemetry_upload_batch (
            client_batch_id, session_id, installation_id, tenant_id, device_id,
            server_trip_id, consent_revision_id, body_json, body_sha256,
            sample_count, state, created_at, updated_at
          ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 'acknowledged', ?, ?)`,
        )
        .run(
          ids.batch,
          ids.session,
          ids.installation,
          ids.tenant,
          ids.device,
          ids.trip,
          ids.consent,
          buildBatchBody(),
          'a'.repeat(64),
          now,
          now,
        ),
    ).toThrow();
    expect(() =>
      database
        .prepare(
          `INSERT INTO telemetry_upload_batch (
            client_batch_id, session_id, installation_id, tenant_id, device_id,
            server_trip_id, consent_revision_id, body_json, body_sha256,
            sample_count, state, receipt_id, server_batch_id, server_state,
            replay, acknowledged_at, created_at, updated_at
          ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 'acknowledged',
                    ?, ?, 'stored', 0, ?, ?, ?)`,
        )
        .run(
          ids.batch,
          ids.session,
          ids.installation,
          ids.tenant,
          ids.device,
          ids.trip,
          ids.consent,
          buildBatchBody(),
          'a'.repeat(64),
          ids.receipt,
          ids.serverBatch,
          now,
          now,
          now,
        ),
    ).toThrow(/UPLOAD_BATCH_INITIAL_STATE_INVALID/);

    database.close();
  });

  it('never allows a local-only session to be promoted or batched', () => {
    const database = openDatabase();
    database.exec(CREATE_TELEMETRY_SCHEMA_V2_SQL);
    insertSession(database, 'development_local_only');
    insertLocationEvent(database, 'local_only');
    database
      .prepare(
        `INSERT INTO trip_event_log (
          event_id, session_id, event_sequence, sample_sequence, event_type,
          occurred_at, latitude, longitude, horizontal_accuracy_m, payload_json, created_at
        ) VALUES (?, ?, 1, 1, 'location_sample', ?, 37.5, 127.0, 5, '{}', ?)`,
      )
      .run(ids.secondEvent, ids.session, now, now);

    expect(() =>
      database
        .prepare(
          `UPDATE trip_session_projection
           SET tenant_id = ?, mobility_device_id = ?, server_trip_id = ?,
               consent_revision_id = ?, upload_eligibility = 'server_bound'
           WHERE session_id = ?`,
        )
        .run(ids.tenant, ids.device, ids.trip, ids.consent, ids.session),
    ).toThrow(/SESSION_UPLOAD_SCOPE_IMMUTABLE/);
    expect(() => insertUploadBatch(database)).toThrow();
    expect(() =>
      database
        .prepare(
          `INSERT INTO outbox_delivery (event_id, delivery_scope, state)
           VALUES (?, 'telemetry_upload', 'pending')`,
        )
        .run(ids.secondEvent),
    ).toThrow(/SERVER_BOUND_LOCATION_REQUIRED/);
    expect(() =>
      database
        .prepare(
          `UPDATE outbox_delivery
           SET delivery_scope = 'telemetry_upload'
           WHERE event_id = ?`,
        )
        .run(ids.event),
    ).toThrow(/OUTBOX_DELIVERY_SCOPE_IMMUTABLE/);

    database.close();
  });

  it('atomically binds only pending server-scoped samples to an immutable batch', () => {
    const database = openDatabase();
    database.exec(CREATE_TELEMETRY_SCHEMA_V2_SQL);
    insertSession(database, 'server_bound');
    insertLocationEvent(database, 'telemetry_upload');
    database
      .prepare(
        `INSERT INTO trip_event_log (
          event_id, session_id, event_sequence, sample_sequence, event_type,
          occurred_at, latitude, longitude, horizontal_accuracy_m, payload_json, created_at
        ) VALUES (?, ?, 1, 1, 'location_sample', ?, 37.5, 127.0, 5, '{}', ?)`,
      )
      .run(ids.secondEvent, ids.session, now, now);
    expect(() =>
      database
        .prepare(
          `INSERT INTO outbox_delivery (event_id, delivery_scope, state)
           VALUES (?, 'local_only', 'not_applicable')`,
        )
        .run(ids.secondEvent),
    ).toThrow(/SERVER_BOUND_LOCATION_MUST_UPLOAD/);
    database
      .prepare(
        `INSERT INTO outbox_delivery (event_id, delivery_scope, state)
         VALUES (?, 'telemetry_upload', 'pending')`,
      )
      .run(ids.secondEvent);
    insertUploadBatch(database);

    expect(() =>
      database
        .prepare(`UPDATE outbox_delivery SET state = 'batched' WHERE event_id = ?`)
        .run(ids.event),
    ).toThrow(/OUTBOX_STATE_TRANSITION_INVALID|BATCH_ITEM_REQUIRED/);
    expect(() =>
      database
        .prepare(
          `INSERT INTO telemetry_upload_batch_item (
            client_batch_id, session_id, position, event_id
          ) VALUES (?, ?, 0, ?)`,
        )
        .run(ids.batch, ids.session, ids.secondEvent),
    ).toThrow(/EVENT_NOT_UPLOADABLE/);
    database
      .prepare(
        `INSERT INTO telemetry_upload_batch_item (
          client_batch_id, session_id, position, event_id
        ) VALUES (?, ?, 0, ?)`,
      )
      .run(ids.batch, ids.session, ids.event);

    expect(
      database.prepare('SELECT state FROM outbox_delivery WHERE event_id = ?').get(ids.event),
    ).toEqual({ state: 'batched' });
    expect(() =>
      database
        .prepare(`UPDATE trip_event_log SET session_id = ? WHERE event_id = ?`)
        .run(ids.trip, ids.event),
    ).toThrow(/TRIP_EVENT_IMMUTABLE/);
    expect(() =>
      database.prepare(`DELETE FROM trip_event_log WHERE event_id = ?`).run(ids.event),
    ).toThrow(/TRIP_EVENT_RETENTION_REQUIRED/);
    expect(() =>
      database
        .prepare(`UPDATE telemetry_upload_batch SET body_json = '{"changed":true}'`)
        .run(),
    ).toThrow(/UPLOAD_BATCH_BODY_IMMUTABLE/);
    expect(() =>
      database
        .prepare(`DELETE FROM telemetry_upload_batch_item WHERE event_id = ?`)
        .run(ids.event),
    ).toThrow(/UPLOAD_BATCH_ITEM_RETENTION_REQUIRED/);
    expect(() =>
      database.prepare(`DELETE FROM outbox_delivery WHERE event_id = ?`).run(ids.event),
    ).toThrow(/OUTBOX_DELIVERY_RETENTION_REQUIRED/);
    expect(() =>
      database
        .prepare(`UPDATE telemetry_upload_batch_item SET position = 1 WHERE event_id = ?`)
        .run(ids.event),
    ).toThrow(/UPLOAD_BATCH_ITEM_IMMUTABLE/);
    expect(() =>
      database
        .prepare(`DELETE FROM telemetry_upload_batch WHERE client_batch_id = ?`)
        .run(ids.batch),
    ).toThrow(/UPLOAD_BATCH_RETENTION_REQUIRED/);
    expect(() =>
      database
        .prepare(`UPDATE telemetry_upload_batch SET client_batch_id = ?`)
        .run(ids.secondEvent),
    ).toThrow(/UPLOAD_BATCH_BODY_IMMUTABLE/);
    database
      .prepare(
        `UPDATE telemetry_upload_batch
         SET state = 'leased', lease_owner_id = ?, lease_expires_at = ?,
             attempt_count = 1, updated_at = ?
         WHERE client_batch_id = ?`,
      )
      .run(ids.leaseOwner, '2026-07-23T04:05:00.000Z', now, ids.batch);
    expect(() =>
      database
        .prepare(
          `UPDATE outbox_delivery
           SET state = 'acknowledged', acknowledged_at = ?
           WHERE event_id = ?`,
        )
        .run(now, ids.event),
    ).toThrow(/OUTBOX_STATE_TRANSITION_INVALID/);
    database
      .prepare(
        `UPDATE telemetry_upload_batch
         SET state = 'acknowledged', lease_owner_id = NULL, lease_expires_at = NULL,
             receipt_id = ?, server_batch_id = ?, server_state = 'stored',
             replay = 0, acknowledged_at = ?, updated_at = ?
         WHERE client_batch_id = ?`,
      )
      .run(ids.receipt, ids.serverBatch, now, now, ids.batch);
    database
      .prepare(
        `UPDATE outbox_delivery
         SET state = 'acknowledged', acknowledged_at = ?
         WHERE event_id = ?`,
      )
      .run(now, ids.event);
    expect(
      database.prepare('SELECT state FROM outbox_delivery WHERE event_id = ?').get(ids.event),
    ).toEqual({ state: 'acknowledged' });
    expect(() =>
      database
        .prepare(`UPDATE telemetry_upload_batch SET last_error_code = 'late_change'`)
        .run(),
    ).toThrow(/UPLOAD_BATCH_TERMINAL/);
    expect(() =>
      database
        .prepare(`UPDATE trip_session_projection SET tenant_id = ? WHERE session_id = ?`)
        .run('20000000-0000-4000-8000-000000000001', ids.session),
    ).toThrow(/SESSION_UPLOAD_SCOPE_IMMUTABLE/);

    database.close();
  });

  it('refuses to lease a batch whose item cardinality is incomplete', () => {
    const database = openDatabase();
    database.exec(CREATE_TELEMETRY_SCHEMA_V2_SQL);
    insertSession(database, 'server_bound');
    insertLocationEvent(database, 'telemetry_upload');
    insertUploadBatch(database, 2);
    database
      .prepare(
        `INSERT INTO telemetry_upload_batch_item (
          client_batch_id, session_id, position, event_id
        ) VALUES (?, ?, 0, ?)`,
      )
      .run(ids.batch, ids.session, ids.event);

    expect(() =>
      database
        .prepare(
          `UPDATE telemetry_upload_batch
           SET state = 'leased', lease_owner_id = ?, lease_expires_at = ?
           WHERE client_batch_id = ?`,
        )
        .run(ids.leaseOwner, '2026-07-23T04:05:00.000Z', ids.batch),
    ).toThrow(/UPLOAD_BATCH_CARDINALITY_MISMATCH/);

    database.close();
  });

  it('migrates every v1 row to local-only and preserves foreign keys', () => {
    const database = openDatabase();
    createV1Database(database);
    database
      .prepare(
        `INSERT INTO trip_session_projection (
          session_id, installation_id, tenant_id, mobility_device_id, consent_version,
          upload_eligibility, started_at, state, next_event_sequence,
          next_sample_sequence, accepted_sample_count, updated_at
        ) VALUES (?, ?, ?, ?, 'legacy-consent', 'development_local_only', ?, 'recording', 1, 1, 1, ?)`,
      )
      .run(ids.session, ids.installation, ids.tenant, ids.device, now, now);
    database
      .prepare(
        `INSERT INTO trip_event_log (
          event_id, session_id, event_sequence, sample_sequence, event_type,
          occurred_at, latitude, longitude, horizontal_accuracy_m, payload_json, created_at
        ) VALUES (?, ?, 0, 0, 'location_sample', ?, 37.5, 127.0, 5, '{}', ?)`,
      )
      .run(ids.event, ids.session, now, now);
    database
      .prepare(`INSERT INTO outbox_delivery (event_id, state) VALUES (?, 'pending')`)
      .run(ids.event);
    database
      .prepare(`INSERT INTO app_metadata (key, value) VALUES ('installation_id', ?)`)
      .run(ids.installation);

    migrateV1LikeRuntime(database);

    expect(
      database
        .prepare(
          `SELECT upload_eligibility, tenant_id, mobility_device_id,
                  server_trip_id, consent_revision_id
           FROM trip_session_projection WHERE session_id = ?`,
        )
        .get(ids.session),
    ).toEqual({
      upload_eligibility: 'development_local_only',
      tenant_id: null,
      mobility_device_id: null,
      server_trip_id: null,
      consent_revision_id: null,
    });
    expect(
      database
        .prepare(
          `SELECT delivery_scope, state, attempt_count
           FROM outbox_delivery WHERE event_id = ?`,
        )
        .get(ids.event),
    ).toEqual({ delivery_scope: 'local_only', state: 'not_applicable', attempt_count: 0 });
    expect(database.prepare('PRAGMA foreign_key_check').all()).toEqual([]);
    expect(
      database
        .prepare(
          `SELECT event_sequence, sample_sequence, event_type, occurred_at,
                  latitude, longitude, horizontal_accuracy_m, payload_json
           FROM trip_event_log WHERE event_id = ?`,
        )
        .get(ids.event),
    ).toEqual({
      event_sequence: 0,
      sample_sequence: 0,
      event_type: 'location_sample',
      occurred_at: now,
      latitude: 37.5,
      longitude: 127,
      horizontal_accuracy_m: 5,
      payload_json: '{}',
    });
    expect(
      database.prepare(`SELECT value FROM app_metadata WHERE key = 'installation_id'`).get(),
    ).toEqual({ value: ids.installation });
    expect(() =>
      database
        .prepare(
          `UPDATE trip_session_projection
           SET tenant_id = ?, mobility_device_id = ?, server_trip_id = ?,
               consent_revision_id = ?, upload_eligibility = 'server_bound'
           WHERE session_id = ?`,
        )
        .run(ids.tenant, ids.device, ids.trip, ids.consent, ids.session),
    ).toThrow(/SESSION_UPLOAD_SCOPE_IMMUTABLE/);
    expect(
      database.prepare('PRAGMA user_version').get() as { user_version: number },
    ).toEqual({ user_version: CURRENT_TELEMETRY_SCHEMA_VERSION });
    const freshDatabase = openDatabase();
    freshDatabase.exec(CREATE_TELEMETRY_SCHEMA_V2_SQL);
    expect(
      database.prepare(`SELECT name FROM sqlite_master WHERE type = 'trigger' ORDER BY name`).all(),
    ).toEqual(
      freshDatabase
        .prepare(`SELECT name FROM sqlite_master WHERE type = 'trigger' ORDER BY name`)
        .all(),
    );

    freshDatabase.close();
    database.close();
  });

  it('rolls back the v1 migration when a foreign-key violation is discovered', () => {
    const database = openDatabase();
    createV1Database(database);
    database.exec('PRAGMA foreign_keys = OFF;');
    database
      .prepare(
        `INSERT INTO trip_event_log (
          event_id, session_id, event_sequence, sample_sequence, event_type,
          occurred_at, payload_json, created_at
        ) VALUES (?, ?, 0, 0, 'location_sample', ?, '{}', ?)`,
      )
      .run(ids.event, ids.session, now, now);
    database.exec('PRAGMA foreign_keys = ON;');

    expect(() => migrateV1LikeRuntime(database)).toThrow(
      /DATABASE_FOREIGN_KEY_CHECK_FAILED/,
    );
    expect(database.prepare('PRAGMA user_version').get()).toEqual({ user_version: 1 });
    expect(
      database
        .prepare(`SELECT name FROM pragma_table_info('trip_session_projection')`)
        .all()
        .some((column: unknown) => (column as { name: string }).name === 'consent_version'),
    ).toBe(true);

    database.close();
  });
});
