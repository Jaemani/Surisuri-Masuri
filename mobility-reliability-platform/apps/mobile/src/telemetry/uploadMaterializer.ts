import {
  buildImmutableTelemetryBatch,
  MAX_TELEMETRY_BATCH_SAMPLES,
} from './syncProtocol';

type SqlValue = string | number | null;

export type UploadMaterializerTransaction = {
  getFirstAsync<T>(source: string, ...params: SqlValue[]): Promise<T | null>;
  getAllAsync<T>(source: string, ...params: SqlValue[]): Promise<T[]>;
  runAsync(source: string, ...params: SqlValue[]): Promise<unknown>;
};

export type UploadMaterializerDatabase = {
  withExclusiveTransactionAsync(
    task: (transaction: UploadMaterializerTransaction) => Promise<void>,
  ): Promise<void>;
};

export type UploadMaterializerDependencies = {
  createClientBatchId(): string;
  now(): string;
  sha256(body: string): Promise<string>;
};

export type UploadBatchReference = {
  clientBatchId: string;
  sessionId: string;
  sampleCount: number;
  state: 'pending' | 'leased';
};

export type UploadBatchMaterializationResult =
  | { kind: 'none' }
  | { kind: 'existing'; batch: UploadBatchReference }
  | { kind: 'created'; batch: UploadBatchReference };

type ExistingBatchRow = {
  client_batch_id: string;
  session_id: string;
  sample_count: number;
  state: 'pending' | 'leased';
};

type UploadScopeRow = {
  session_id: string;
  installation_id: string;
  tenant_id: string;
  device_id: string;
  server_trip_id: string;
  consent_revision_id: string;
};

type PendingSampleRow = {
  event_id: string;
  sample_sequence: number;
  occurred_at: string;
  latitude: number;
  longitude: number;
  horizontal_accuracy_m: number | null;
  altitude_m: number | null;
  speed_mps: number | null;
  heading_degrees: number | null;
  is_mock_location: number | null;
};

function toBatchReference(row: ExistingBatchRow): UploadBatchReference {
  return {
    clientBatchId: row.client_batch_id,
    sessionId: row.session_id,
    sampleCount: row.sample_count,
    state: row.state,
  };
}

function readMockLocation(value: number | null): boolean | null {
  if (value === null) return null;
  if (value === 0) return false;
  if (value === 1) return true;
  throw new Error('UPLOAD_SAMPLE_MOCK_LOCATION_INVALID');
}

function requireLowercaseSha256(value: string): void {
  if (!/^[0-9a-f]{64}$/.test(value)) {
    throw new Error('UPLOAD_BATCH_DIGEST_INVALID');
  }
}

/**
 * Materializes at most one batch. Existing pending/leased work always wins so
 * an app restart cannot regenerate a different body for the same work.
 */
export async function materializeNextUploadBatchCore(
  database: UploadMaterializerDatabase,
  dependencies: UploadMaterializerDependencies,
): Promise<UploadBatchMaterializationResult> {
  let result: UploadBatchMaterializationResult = { kind: 'none' };

  await database.withExclusiveTransactionAsync(async (transaction) => {
    const foreignKeys = await transaction.getFirstAsync<{ foreign_keys: number }>(
      'PRAGMA foreign_keys',
    );
    if (foreignKeys?.foreign_keys !== 1) {
      throw new Error('UPLOAD_DATABASE_FOREIGN_KEYS_DISABLED');
    }

    const existing = await transaction.getFirstAsync<ExistingBatchRow>(
      `SELECT client_batch_id, session_id, sample_count, state
       FROM telemetry_upload_batch
       WHERE state IN ('pending', 'leased')
       ORDER BY created_at ASC, client_batch_id ASC
       LIMIT 1`,
    );
    if (existing) {
      result = { kind: 'existing', batch: toBatchReference(existing) };
      return;
    }

    const scope = await transaction.getFirstAsync<UploadScopeRow>(
      `SELECT
         session.session_id,
         session.installation_id,
         session.tenant_id,
         session.mobility_device_id AS device_id,
         session.server_trip_id,
         session.consent_revision_id
       FROM outbox_delivery AS delivery
       JOIN trip_event_log AS event ON event.event_id = delivery.event_id
       JOIN trip_session_projection AS session ON session.session_id = event.session_id
       WHERE delivery.delivery_scope = 'telemetry_upload'
         AND delivery.state = 'pending'
         AND event.event_type = 'location_sample'
         AND event.sample_sequence IS NOT NULL
         AND session.upload_eligibility = 'server_bound'
       ORDER BY event.created_at ASC, event.event_sequence ASC, event.event_id ASC
       LIMIT 1`,
    );
    if (!scope) return;

    const samples = await transaction.getAllAsync<PendingSampleRow>(
      `SELECT
         event.event_id,
         event.sample_sequence,
         event.occurred_at,
         event.latitude,
         event.longitude,
         event.horizontal_accuracy_m,
         event.altitude_m,
         event.speed_mps,
         event.heading_degrees,
         event.is_mock_location
       FROM outbox_delivery AS delivery
       JOIN trip_event_log AS event ON event.event_id = delivery.event_id
       WHERE delivery.delivery_scope = 'telemetry_upload'
         AND delivery.state = 'pending'
         AND event.event_type = 'location_sample'
         AND event.sample_sequence IS NOT NULL
         AND event.session_id = ?
       ORDER BY event.sample_sequence ASC, event.event_id ASC
       LIMIT ?`,
      scope.session_id,
      MAX_TELEMETRY_BATCH_SAMPLES,
    );
    if (samples.length === 0) return;

    const clientBatchId = dependencies.createClientBatchId();
    const createdAt = dependencies.now();
    const immutable = buildImmutableTelemetryBatch({
      clientBatchId,
      sentAt: createdAt,
      scope: {
        tenantId: scope.tenant_id,
        deviceId: scope.device_id,
        tripId: scope.server_trip_id,
        clientSessionId: scope.session_id,
        installationId: scope.installation_id,
        consentRevisionId: scope.consent_revision_id,
      },
      samples: samples.map((sample) => ({
        clientSampleId: sample.event_id,
        sequence: sample.sample_sequence,
        capturedAt: sample.occurred_at,
        latitude: sample.latitude,
        longitude: sample.longitude,
        horizontalAccuracyM: sample.horizontal_accuracy_m,
        altitudeM: sample.altitude_m,
        speedMps: sample.speed_mps,
        headingDegrees: sample.heading_degrees,
        activityHint: 'unknown',
        isMockLocation: readMockLocation(sample.is_mock_location),
      })),
    });
    let bodySha256: string;
    try {
      bodySha256 = await dependencies.sha256(immutable.body);
    } catch {
      throw new Error('UPLOAD_BATCH_DIGEST_FAILED');
    }
    requireLowercaseSha256(bodySha256);

    await transaction.runAsync(
      `INSERT INTO telemetry_upload_batch (
         client_batch_id, session_id, installation_id, tenant_id, device_id,
         server_trip_id, consent_revision_id, body_json, body_sha256,
         sample_count, state, created_at, updated_at
       ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?)`,
      immutable.clientBatchId,
      scope.session_id,
      scope.installation_id,
      scope.tenant_id,
      scope.device_id,
      scope.server_trip_id,
      scope.consent_revision_id,
      immutable.body,
      bodySha256,
      immutable.sampleCount,
      createdAt,
      createdAt,
    );

    for (const [position, sample] of samples.entries()) {
      await transaction.runAsync(
        `INSERT INTO telemetry_upload_batch_item (
           client_batch_id, session_id, position, event_id
         ) VALUES (?, ?, ?, ?)`,
        immutable.clientBatchId,
        scope.session_id,
        position,
        sample.event_id,
      );
    }

    const bound = await transaction.getFirstAsync<{ count: number }>(
      `SELECT COUNT(*) AS count
       FROM telemetry_upload_batch_item AS item
       JOIN outbox_delivery AS delivery ON delivery.event_id = item.event_id
       WHERE item.client_batch_id = ?
         AND item.session_id = ?
         AND delivery.delivery_scope = 'telemetry_upload'
         AND delivery.state = 'batched'`,
      immutable.clientBatchId,
      scope.session_id,
    );
    if (bound?.count !== immutable.sampleCount) {
      throw new Error('UPLOAD_BATCH_BINDING_INCOMPLETE');
    }

    result = {
      kind: 'created',
      batch: {
        clientBatchId: immutable.clientBatchId,
        sessionId: scope.session_id,
        sampleCount: immutable.sampleCount,
        state: 'pending',
      },
    };
  });

  return result;
}
