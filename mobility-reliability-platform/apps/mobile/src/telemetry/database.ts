import * as Crypto from 'expo-crypto';
import * as SQLite from 'expo-sqlite';

import type { NormalizedLocationSample, SampleRejectionReason } from './samplePolicy';

const DATABASE_NAME = 'mobility-telemetry-v1.sqlite';
const CURRENT_SCHEMA_VERSION = 1;

export type TripSessionSummary = {
  sessionId: string;
  startedAt: string;
  endedAt: string | null;
  state: 'recording' | 'stopped';
  nextEventSequence: number;
  nextSampleSequence: number;
  acceptedSampleCount: number;
  rejectedSampleCount: number;
  uploadEligibility: 'development_local_only';
  lastSampleAt: string | null;
};

type SessionRow = {
  session_id: string;
  started_at: string;
  ended_at: string | null;
  state: 'recording' | 'stopped';
  next_event_sequence: number;
  next_sample_sequence: number;
  accepted_sample_count: number;
  rejected_sample_count: number;
  upload_eligibility: 'development_local_only';
  last_sample_at: string | null;
};

let databasePromise: Promise<SQLite.SQLiteDatabase> | undefined;

function toSummary(row: SessionRow): TripSessionSummary {
  return {
    sessionId: row.session_id,
    startedAt: row.started_at,
    endedAt: row.ended_at,
    state: row.state,
    nextEventSequence: row.next_event_sequence,
    nextSampleSequence: row.next_sample_sequence,
    acceptedSampleCount: row.accepted_sample_count,
    rejectedSampleCount: row.rejected_sample_count,
    uploadEligibility: row.upload_eligibility,
    lastSampleAt: row.last_sample_at,
  };
}

async function migrate(database: SQLite.SQLiteDatabase): Promise<void> {
  await database.execAsync(`
    PRAGMA journal_mode = WAL;
    PRAGMA foreign_keys = ON;
  `);

  const versionRow = await database.getFirstAsync<{ user_version: number }>(
    'PRAGMA user_version',
  );
  const schemaVersion = versionRow?.user_version ?? 0;
  if (schemaVersion > CURRENT_SCHEMA_VERSION) {
    throw new Error('DATABASE_SCHEMA_NEWER_THAN_APP');
  }
  if (schemaVersion === CURRENT_SCHEMA_VERSION) return;

  const existingV0Table = await database.getFirstAsync<{ name: string }>(
    `SELECT name FROM sqlite_master
     WHERE type = 'table' AND name = 'trip_session_projection'`,
  );
  if (existingV0Table) {
    throw new Error('UNVERSIONED_DEVELOPMENT_DATABASE_REQUIRES_CLEAR');
  }

  await database.execAsync(`
    CREATE TABLE IF NOT EXISTS trip_session_projection (
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

    CREATE UNIQUE INDEX IF NOT EXISTS one_recording_session
      ON trip_session_projection(state)
      WHERE state = 'recording';

    CREATE TABLE IF NOT EXISTS trip_event_log (
      event_id TEXT PRIMARY KEY NOT NULL,
      session_id TEXT NOT NULL,
      event_sequence INTEGER NOT NULL,
      sample_sequence INTEGER,
      event_type TEXT NOT NULL CHECK (
        event_type IN ('session_started', 'location_sample', 'sample_rejected', 'session_stopped')
      ),
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

    CREATE TABLE IF NOT EXISTS outbox_delivery (
      event_id TEXT PRIMARY KEY NOT NULL,
      state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'sending', 'acknowledged')),
      attempt_count INTEGER NOT NULL DEFAULT 0,
      next_attempt_at TEXT,
      acknowledged_at TEXT,
      last_error_code TEXT,
      FOREIGN KEY (event_id) REFERENCES trip_event_log(event_id)
    );

    CREATE INDEX IF NOT EXISTS pending_outbox
      ON outbox_delivery(state, next_attempt_at);

    CREATE TABLE IF NOT EXISTS app_metadata (
      key TEXT PRIMARY KEY NOT NULL,
      value TEXT NOT NULL
    );

    PRAGMA user_version = 1;
  `);
}

export async function getTelemetryDatabase(): Promise<SQLite.SQLiteDatabase> {
  if (!databasePromise) {
    databasePromise = SQLite.openDatabaseAsync(DATABASE_NAME).then(async (database) => {
      await migrate(database);
      return database;
    });
  }

  return databasePromise;
}

async function insertOutboxEvent(
  database: SQLite.SQLiteDatabase,
  event: {
    eventId: string;
    sessionId: string;
    eventSequence: number;
    sampleSequence?: number | null;
    eventType: 'session_started' | 'location_sample' | 'sample_rejected' | 'session_stopped';
    occurredAt: string;
    latitude?: number | null;
    longitude?: number | null;
    horizontalAccuracyM?: number | null;
    altitudeM?: number | null;
    speedMps?: number | null;
    headingDegrees?: number | null;
    isMockLocation?: boolean | null;
    payloadJson: string;
  },
): Promise<void> {
  await database.runAsync(
    `INSERT INTO trip_event_log (
      event_id, session_id, event_sequence, sample_sequence, event_type, occurred_at,
      latitude, longitude, horizontal_accuracy_m, altitude_m, speed_mps,
      heading_degrees, is_mock_location, payload_json, created_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
    event.eventId,
    event.sessionId,
    event.eventSequence,
    event.sampleSequence ?? null,
    event.eventType,
    event.occurredAt,
    event.latitude ?? null,
    event.longitude ?? null,
    event.horizontalAccuracyM ?? null,
    event.altitudeM ?? null,
    event.speedMps ?? null,
    event.headingDegrees ?? null,
    event.isMockLocation === null || event.isMockLocation === undefined
      ? null
      : Number(event.isMockLocation),
    event.payloadJson,
    new Date().toISOString(),
  );
  await database.runAsync(
    `INSERT INTO outbox_delivery (event_id, state, attempt_count)
     VALUES (?, 'pending', 0)`,
    event.eventId,
  );
}

export async function startTripSession(): Promise<TripSessionSummary> {
  const database = await getTelemetryDatabase();
  const sessionId = Crypto.randomUUID();
  const eventId = Crypto.randomUUID();
  const now = new Date().toISOString();
  let installation = await database.getFirstAsync<{ value: string }>(
    `SELECT value FROM app_metadata WHERE key = 'installation_id'`,
  );
  if (!installation) {
    const candidate = Crypto.randomUUID();
    await database.runAsync(
      `INSERT OR IGNORE INTO app_metadata (key, value) VALUES ('installation_id', ?)`,
      candidate,
    );
    installation = await database.getFirstAsync<{ value: string }>(
      `SELECT value FROM app_metadata WHERE key = 'installation_id'`,
    );
  }
  if (!installation) {
    throw new Error('INSTALLATION_ID_UNAVAILABLE');
  }

  await database.withExclusiveTransactionAsync(async (transaction) => {
    await transaction.runAsync(
      `INSERT INTO trip_session_projection (
        session_id, installation_id, upload_eligibility, started_at, state,
        next_event_sequence, next_sample_sequence,
        accepted_sample_count, rejected_sample_count, updated_at
      ) VALUES (?, ?, 'development_local_only', ?, 'recording', 1, 0, 0, 0, ?)`,
      sessionId,
      installation.value,
      now,
      now,
    );
    await insertOutboxEvent(transaction, {
      eventId,
      sessionId,
      eventSequence: 0,
      eventType: 'session_started',
      occurredAt: now,
      payloadJson: JSON.stringify({ captureMode: 'manual', schemaVersion: 1 }),
    });
  });

  return {
    sessionId,
    startedAt: now,
    endedAt: null,
    state: 'recording',
    nextEventSequence: 1,
    nextSampleSequence: 0,
    acceptedSampleCount: 0,
    rejectedSampleCount: 0,
    uploadEligibility: 'development_local_only',
    lastSampleAt: null,
  };
}

export async function appendLocationSample(
  sessionId: string,
  sample: NormalizedLocationSample,
): Promise<TripSessionSummary> {
  const database = await getTelemetryDatabase();
  let updated: TripSessionSummary | undefined;

  await database.withExclusiveTransactionAsync(async (transaction) => {
    const row = await transaction.getFirstAsync<SessionRow>(
      `SELECT * FROM trip_session_projection
       WHERE session_id = ? AND state = 'recording'`,
      sessionId,
    );
    if (!row) {
      throw new Error('SESSION_NOT_RECORDING');
    }

    const eventId = Crypto.randomUUID();
    const occurredAt = new Date(sample.timestamp).toISOString();
    await insertOutboxEvent(transaction, {
      eventId,
      sessionId,
      eventSequence: row.next_event_sequence,
      sampleSequence: row.next_sample_sequence,
      eventType: 'location_sample',
      occurredAt,
      latitude: sample.latitude,
      longitude: sample.longitude,
      horizontalAccuracyM: sample.accuracy,
      altitudeM: sample.altitude,
      speedMps: sample.speed,
      headingDegrees: sample.heading,
      isMockLocation: sample.isMockLocation,
      payloadJson: JSON.stringify({ source: 'phone_gps', schemaVersion: 1 }),
    });

    const updatedAt = new Date().toISOString();
    await transaction.runAsync(
      `UPDATE trip_session_projection
       SET next_event_sequence = next_event_sequence + 1,
           next_sample_sequence = next_sample_sequence + 1,
           accepted_sample_count = accepted_sample_count + 1,
           last_sample_at = ?,
           updated_at = ?
       WHERE session_id = ?`,
      occurredAt,
      updatedAt,
      sessionId,
    );

    updated = {
      ...toSummary(row),
      nextEventSequence: row.next_event_sequence + 1,
      nextSampleSequence: row.next_sample_sequence + 1,
      acceptedSampleCount: row.accepted_sample_count + 1,
      lastSampleAt: occurredAt,
    };
  });

  if (!updated) {
    throw new Error('SAMPLE_APPEND_FAILED');
  }
  return updated;
}

export async function recordRejectedSample(
  sessionId: string,
  reason: SampleRejectionReason,
): Promise<TripSessionSummary> {
  const database = await getTelemetryDatabase();
  let updated: TripSessionSummary | undefined;

  await database.withExclusiveTransactionAsync(async (transaction) => {
    const row = await transaction.getFirstAsync<SessionRow>(
      `SELECT * FROM trip_session_projection
       WHERE session_id = ? AND state = 'recording'`,
      sessionId,
    );
    if (!row) {
      throw new Error('SESSION_NOT_RECORDING');
    }

    const occurredAt = new Date().toISOString();
    await insertOutboxEvent(transaction, {
      eventId: Crypto.randomUUID(),
      sessionId,
      eventSequence: row.next_event_sequence,
      eventType: 'sample_rejected',
      occurredAt,
      payloadJson: JSON.stringify({ reason, schemaVersion: 1 }),
    });
    await transaction.runAsync(
      `UPDATE trip_session_projection
       SET next_event_sequence = next_event_sequence + 1,
           rejected_sample_count = rejected_sample_count + 1,
           updated_at = ?
       WHERE session_id = ?`,
      occurredAt,
      sessionId,
    );

    updated = {
      ...toSummary(row),
      nextEventSequence: row.next_event_sequence + 1,
      rejectedSampleCount: row.rejected_sample_count + 1,
    };
  });

  if (!updated) {
    throw new Error('REJECTED_SAMPLE_APPEND_FAILED');
  }
  return updated;
}

export async function stopTripSession(
  sessionId: string,
  reason: 'user_stopped' | 'watch_start_failed',
): Promise<TripSessionSummary> {
  const database = await getTelemetryDatabase();
  let stopped: TripSessionSummary | undefined;

  await database.withExclusiveTransactionAsync(async (transaction) => {
    const row = await transaction.getFirstAsync<SessionRow>(
      `SELECT * FROM trip_session_projection
       WHERE session_id = ? AND state = 'recording'`,
      sessionId,
    );
    if (!row) {
      throw new Error('SESSION_NOT_RECORDING');
    }

    const now = new Date().toISOString();
    await insertOutboxEvent(transaction, {
      eventId: Crypto.randomUUID(),
      sessionId,
      eventSequence: row.next_event_sequence,
      eventType: 'session_stopped',
      occurredAt: now,
      payloadJson: JSON.stringify({ reason, schemaVersion: 1 }),
    });
    await transaction.runAsync(
      `UPDATE trip_session_projection
       SET ended_at = ?, state = 'stopped',
           next_event_sequence = next_event_sequence + 1, updated_at = ?
       WHERE session_id = ?`,
      now,
      now,
      sessionId,
    );

    stopped = {
      ...toSummary(row),
      endedAt: now,
      state: 'stopped',
      nextEventSequence: row.next_event_sequence + 1,
    };
  });

  if (!stopped) {
    throw new Error('SESSION_STOP_FAILED');
  }
  return stopped;
}

export async function getActiveTripSession(): Promise<TripSessionSummary | null> {
  const database = await getTelemetryDatabase();
  const row = await database.getFirstAsync<SessionRow>(
    `SELECT * FROM trip_session_projection
     WHERE state = 'recording'
     ORDER BY started_at DESC
     LIMIT 1`,
  );
  return row ? toSummary(row) : null;
}

export async function getPendingOutboxCount(): Promise<number> {
  const database = await getTelemetryDatabase();
  const row = await database.getFirstAsync<{ count: number }>(
    `SELECT COUNT(*) AS count FROM outbox_delivery WHERE state = 'pending'`,
  );
  return row?.count ?? 0;
}
