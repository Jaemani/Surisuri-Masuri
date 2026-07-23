export const CURRENT_TELEMETRY_SCHEMA_VERSION = 3;

// Uses a safe JSON substitute so malformed JSON is reported as an invalid row
// instead of making the migration audit itself throw a value-bearing error.
export const FIND_INVALID_TELEMETRY_UPLOAD_BATCH_SQL = `
  WITH candidate AS (
    SELECT
      batch.*,
      CASE WHEN json_valid(batch.body_json) = 1 THEN batch.body_json ELSE '{}' END
        AS safe_body_json
    FROM telemetry_upload_batch AS batch
  )
  SELECT 1 AS invalid
  FROM candidate
  WHERE NOT COALESCE((
    json_valid(candidate.body_json) = 1
    AND (SELECT COUNT(*) FROM json_each(candidate.safe_body_json)) = 10
    AND json_extract(candidate.safe_body_json, '$.schemaVersion') = 'telemetry-batch.v2'
    AND json_extract(candidate.safe_body_json, '$.clientBatchId') = candidate.client_batch_id
    AND json_extract(candidate.safe_body_json, '$.tenantId') = candidate.tenant_id
    AND json_extract(candidate.safe_body_json, '$.deviceId') = candidate.device_id
    AND json_extract(candidate.safe_body_json, '$.tripId') = candidate.server_trip_id
    AND json_extract(candidate.safe_body_json, '$.clientSessionId') = candidate.session_id
    AND json_extract(candidate.safe_body_json, '$.installationId') = candidate.installation_id
    AND json_extract(candidate.safe_body_json, '$.consentRevisionId') = candidate.consent_revision_id
    AND json_type(candidate.safe_body_json, '$.sentAt') = 'text'
    AND json_type(candidate.safe_body_json, '$.samples') = 'array'
    AND json_array_length(candidate.safe_body_json, '$.samples') = candidate.sample_count
    AND NOT EXISTS (
      SELECT 1 FROM json_each(candidate.safe_body_json, '$.samples') AS sample
      WHERE json_type(sample.value) != 'object'
        OR (SELECT COUNT(*) FROM json_each(sample.value)) != 12
    )
  ), 0)
  LIMIT 1
`;

export const CREATE_TELEMETRY_SCHEMA_V3_SQL = `
  CREATE TABLE IF NOT EXISTS trip_session_projection (
    session_id TEXT PRIMARY KEY NOT NULL,
    installation_id TEXT NOT NULL,
    tenant_id TEXT,
    actor_id TEXT,
    mobility_device_id TEXT,
    server_trip_id TEXT,
    consent_revision_id TEXT,
    upload_eligibility TEXT NOT NULL CHECK (
      upload_eligibility IN ('development_local_only', 'server_bound')
    ),
    started_at TEXT NOT NULL,
    ended_at TEXT,
    state TEXT NOT NULL CHECK (state IN ('recording', 'stopped')),
    next_event_sequence INTEGER NOT NULL DEFAULT 0,
    next_sample_sequence INTEGER NOT NULL DEFAULT 0,
    accepted_sample_count INTEGER NOT NULL DEFAULT 0,
    rejected_sample_count INTEGER NOT NULL DEFAULT 0,
    last_sample_at TEXT,
    updated_at TEXT NOT NULL,
    UNIQUE (
      session_id, installation_id, tenant_id, mobility_device_id,
      server_trip_id, consent_revision_id
    ),
    CHECK (
      (upload_eligibility = 'development_local_only'
        AND tenant_id IS NULL
        AND mobility_device_id IS NULL
        AND server_trip_id IS NULL
        AND consent_revision_id IS NULL)
      OR
      (upload_eligibility = 'server_bound'
        AND tenant_id IS NOT NULL
        AND mobility_device_id IS NOT NULL
        AND server_trip_id IS NOT NULL
        AND consent_revision_id IS NOT NULL)
    )
  );

  CREATE UNIQUE INDEX IF NOT EXISTS one_recording_session
    ON trip_session_projection(state)
      WHERE state = 'recording';

  CREATE TRIGGER IF NOT EXISTS immutable_session_upload_scope
    BEFORE UPDATE OF
      session_id, installation_id, tenant_id, mobility_device_id, server_trip_id,
      consent_revision_id, upload_eligibility
    ON trip_session_projection
    WHEN
      OLD.session_id IS NOT NEW.session_id
      OR OLD.installation_id IS NOT NEW.installation_id
      OR OLD.tenant_id IS NOT NEW.tenant_id
      OR OLD.mobility_device_id IS NOT NEW.mobility_device_id
      OR OLD.server_trip_id IS NOT NEW.server_trip_id
      OR OLD.consent_revision_id IS NOT NEW.consent_revision_id
      OR OLD.upload_eligibility IS NOT NEW.upload_eligibility
    BEGIN
      SELECT RAISE(ABORT, 'SESSION_UPLOAD_SCOPE_IMMUTABLE');
    END;

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

  CREATE TRIGGER IF NOT EXISTS immutable_trip_event
    BEFORE UPDATE ON trip_event_log
    BEGIN
      SELECT RAISE(ABORT, 'TRIP_EVENT_IMMUTABLE');
    END;

  CREATE TRIGGER IF NOT EXISTS retain_trip_event
    BEFORE DELETE ON trip_event_log
    BEGIN
      SELECT RAISE(ABORT, 'TRIP_EVENT_RETENTION_REQUIRED');
    END;

  CREATE TABLE IF NOT EXISTS outbox_delivery (
    event_id TEXT PRIMARY KEY NOT NULL,
    delivery_scope TEXT NOT NULL CHECK (
      delivery_scope IN ('local_only', 'telemetry_upload')
    ),
    state TEXT NOT NULL CHECK (
      state IN ('not_applicable', 'pending', 'batched', 'acknowledged', 'held')
    ),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TEXT,
    acknowledged_at TEXT,
    last_error_code TEXT,
    FOREIGN KEY (event_id) REFERENCES trip_event_log(event_id),
    CHECK (
      (delivery_scope = 'local_only' AND state = 'not_applicable')
      OR
      (delivery_scope = 'telemetry_upload' AND state != 'not_applicable')
    ),
    CHECK (
      (state = 'acknowledged' AND acknowledged_at IS NOT NULL)
      OR
      (state != 'acknowledged' AND acknowledged_at IS NULL)
    )
  );

  CREATE INDEX IF NOT EXISTS pending_outbox
    ON outbox_delivery(delivery_scope, state, next_attempt_at);

  CREATE TRIGGER IF NOT EXISTS immutable_outbox_delivery_scope
    BEFORE UPDATE OF delivery_scope ON outbox_delivery
    WHEN OLD.delivery_scope IS NOT NEW.delivery_scope
    BEGIN
      SELECT RAISE(ABORT, 'OUTBOX_DELIVERY_SCOPE_IMMUTABLE');
    END;

  CREATE TRIGGER IF NOT EXISTS validate_initial_outbox_state
    BEFORE INSERT ON outbox_delivery
    WHEN NOT COALESCE((
      NEW.attempt_count = 0
      AND NEW.next_attempt_at IS NULL
      AND NEW.acknowledged_at IS NULL
      AND NEW.last_error_code IS NULL
      AND (
        (NEW.delivery_scope = 'local_only' AND NEW.state = 'not_applicable')
        OR
        (NEW.delivery_scope = 'telemetry_upload' AND NEW.state = 'pending')
      )
    ), 0)
    BEGIN
      SELECT RAISE(ABORT, 'OUTBOX_INITIAL_STATE_INVALID');
    END;

  CREATE TRIGGER IF NOT EXISTS retain_outbox_delivery
    BEFORE DELETE ON outbox_delivery
    BEGIN
      SELECT RAISE(ABORT, 'OUTBOX_DELIVERY_RETENTION_REQUIRED');
    END;

  CREATE TRIGGER IF NOT EXISTS require_server_bound_upload_delivery
    BEFORE INSERT ON outbox_delivery
    WHEN NEW.delivery_scope = 'telemetry_upload'
      AND NOT EXISTS (
        SELECT 1
        FROM trip_event_log AS event
        JOIN trip_session_projection AS session
          ON session.session_id = event.session_id
        WHERE event.event_id = NEW.event_id
          AND event.event_type = 'location_sample'
          AND session.upload_eligibility = 'server_bound'
      )
    BEGIN
      SELECT RAISE(ABORT, 'SERVER_BOUND_LOCATION_REQUIRED');
    END;

  CREATE TRIGGER IF NOT EXISTS require_upload_delivery_for_server_bound_location
    BEFORE INSERT ON outbox_delivery
    WHEN NEW.delivery_scope = 'local_only'
      AND EXISTS (
        SELECT 1
        FROM trip_event_log AS event
        JOIN trip_session_projection AS session
          ON session.session_id = event.session_id
        WHERE event.event_id = NEW.event_id
          AND event.event_type = 'location_sample'
          AND session.upload_eligibility = 'server_bound'
      )
    BEGIN
      SELECT RAISE(ABORT, 'SERVER_BOUND_LOCATION_MUST_UPLOAD');
    END;

  CREATE TABLE IF NOT EXISTS telemetry_upload_batch (
    client_batch_id TEXT PRIMARY KEY NOT NULL,
    session_id TEXT NOT NULL,
    installation_id TEXT NOT NULL,
    tenant_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    server_trip_id TEXT NOT NULL,
    consent_revision_id TEXT NOT NULL,
    body_json TEXT NOT NULL,
    body_sha256 TEXT NOT NULL CHECK (length(body_sha256) = 64),
    sample_count INTEGER NOT NULL CHECK (sample_count BETWEEN 1 AND 500),
    state TEXT NOT NULL CHECK (
      state IN ('pending', 'leased', 'acknowledged', 'held')
    ),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    lease_owner_id TEXT,
    lease_expires_at TEXT,
    next_attempt_at TEXT,
    receipt_id TEXT,
    server_batch_id TEXT,
    server_state TEXT CHECK (server_state IN ('stored', 'queued', 'projected')),
    replay INTEGER CHECK (replay IN (0, 1)),
    acknowledged_at TEXT,
    last_error_code TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (client_batch_id, session_id),
    FOREIGN KEY (
      session_id, installation_id, tenant_id, device_id,
      server_trip_id, consent_revision_id
    ) REFERENCES trip_session_projection (
      session_id, installation_id, tenant_id, mobility_device_id,
      server_trip_id, consent_revision_id
    ),
    CHECK (
      (state = 'leased' AND lease_owner_id IS NOT NULL AND lease_expires_at IS NOT NULL)
      OR
      (state != 'leased' AND lease_owner_id IS NULL AND lease_expires_at IS NULL)
    ),
    CHECK (
      (state = 'acknowledged'
        AND receipt_id IS NOT NULL
        AND server_batch_id IS NOT NULL
        AND server_state IS NOT NULL
        AND replay IS NOT NULL
        AND acknowledged_at IS NOT NULL)
      OR
      (state != 'acknowledged'
        AND receipt_id IS NULL
        AND server_batch_id IS NULL
        AND server_state IS NULL
        AND replay IS NULL
        AND acknowledged_at IS NULL)
    )
  );

  CREATE INDEX IF NOT EXISTS due_upload_batch
    ON telemetry_upload_batch(state, next_attempt_at, created_at);

  CREATE TRIGGER IF NOT EXISTS immutable_upload_batch_body
    BEFORE UPDATE OF
      client_batch_id, session_id, installation_id, tenant_id, device_id,
      server_trip_id, consent_revision_id, body_json, body_sha256,
      sample_count, created_at
    ON telemetry_upload_batch
    BEGIN
      SELECT RAISE(ABORT, 'UPLOAD_BATCH_BODY_IMMUTABLE');
    END;

  CREATE TRIGGER IF NOT EXISTS validate_initial_upload_batch_state
    BEFORE INSERT ON telemetry_upload_batch
    WHEN NOT COALESCE((
      NEW.state = 'pending'
      AND NEW.attempt_count = 0
      AND NEW.lease_owner_id IS NULL
      AND NEW.lease_expires_at IS NULL
      AND NEW.next_attempt_at IS NULL
      AND NEW.receipt_id IS NULL
      AND NEW.server_batch_id IS NULL
      AND NEW.server_state IS NULL
      AND NEW.replay IS NULL
      AND NEW.acknowledged_at IS NULL
      AND NEW.last_error_code IS NULL
      AND json_valid(NEW.body_json) = 1
      AND (SELECT COUNT(*) FROM json_each(NEW.body_json)) = 10
      AND json_extract(NEW.body_json, '$.schemaVersion') = 'telemetry-batch.v2'
      AND json_extract(NEW.body_json, '$.clientBatchId') = NEW.client_batch_id
      AND json_extract(NEW.body_json, '$.tenantId') = NEW.tenant_id
      AND json_extract(NEW.body_json, '$.deviceId') = NEW.device_id
      AND json_extract(NEW.body_json, '$.tripId') = NEW.server_trip_id
      AND json_extract(NEW.body_json, '$.clientSessionId') = NEW.session_id
      AND json_extract(NEW.body_json, '$.installationId') = NEW.installation_id
      AND json_extract(NEW.body_json, '$.consentRevisionId') = NEW.consent_revision_id
      AND json_type(NEW.body_json, '$.sentAt') = 'text'
      AND json_type(NEW.body_json, '$.samples') = 'array'
      AND json_array_length(NEW.body_json, '$.samples') = NEW.sample_count
      AND NOT EXISTS (
        SELECT 1 FROM json_each(NEW.body_json, '$.samples') AS sample
        WHERE json_type(sample.value) != 'object'
          OR (SELECT COUNT(*) FROM json_each(sample.value)) != 12
      )
    ), 0)
    BEGIN
      SELECT RAISE(ABORT, 'UPLOAD_BATCH_INITIAL_STATE_INVALID');
    END;

  CREATE TRIGGER IF NOT EXISTS retain_upload_batch_identity
    BEFORE DELETE ON telemetry_upload_batch
    BEGIN
      SELECT RAISE(ABORT, 'UPLOAD_BATCH_RETENTION_REQUIRED');
    END;

  CREATE TABLE IF NOT EXISTS telemetry_upload_batch_item (
    client_batch_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    position INTEGER NOT NULL CHECK (position BETWEEN 0 AND 499),
    event_id TEXT NOT NULL UNIQUE,
    PRIMARY KEY (client_batch_id, position),
    FOREIGN KEY (client_batch_id, session_id)
      REFERENCES telemetry_upload_batch(client_batch_id, session_id),
    FOREIGN KEY (event_id, session_id)
      REFERENCES trip_event_log(event_id, session_id)
  );

  CREATE UNIQUE INDEX IF NOT EXISTS event_session_identity
    ON trip_event_log(event_id, session_id);

  CREATE TRIGGER IF NOT EXISTS require_uploadable_batch_item
    BEFORE INSERT ON telemetry_upload_batch_item
    WHEN
      NOT EXISTS (
        SELECT 1 FROM trip_event_log
        WHERE event_id = NEW.event_id
          AND session_id = NEW.session_id
          AND event_type = 'location_sample'
      )
      OR NOT EXISTS (
        SELECT 1 FROM outbox_delivery
        WHERE event_id = NEW.event_id
          AND delivery_scope = 'telemetry_upload'
          AND state = 'pending'
      )
      OR NOT EXISTS (
        SELECT 1
        FROM telemetry_upload_batch AS batch
        JOIN trip_event_log AS event
          ON event.event_id = NEW.event_id
         AND event.session_id = NEW.session_id
        WHERE batch.client_batch_id = NEW.client_batch_id
          AND batch.session_id = NEW.session_id
          AND json_extract(
            batch.body_json,
            '$.samples[' || NEW.position || '].clientSampleId'
          ) = event.event_id
          AND json_extract(
            batch.body_json,
            '$.samples[' || NEW.position || '].sequence'
          ) = event.sample_sequence
          AND json_extract(
            batch.body_json,
            '$.samples[' || NEW.position || '].capturedAt'
          ) = event.occurred_at
          AND json_extract(
            batch.body_json,
            '$.samples[' || NEW.position || '].latitude'
          ) = event.latitude
          AND json_extract(
            batch.body_json,
            '$.samples[' || NEW.position || '].longitude'
          ) = event.longitude
          AND (
            (event.horizontal_accuracy_m IS NULL
              AND json_type(
                batch.body_json,
                '$.samples[' || NEW.position || '].horizontalAccuracyM'
              ) = 'null')
            OR json_extract(
              batch.body_json,
              '$.samples[' || NEW.position || '].horizontalAccuracyM'
            ) = event.horizontal_accuracy_m
          )
          AND (
            (event.altitude_m IS NULL
              AND json_type(
                batch.body_json,
                '$.samples[' || NEW.position || '].altitudeM'
              ) = 'null')
            OR json_extract(
              batch.body_json,
              '$.samples[' || NEW.position || '].altitudeM'
            ) = event.altitude_m
          )
          AND (
            (event.speed_mps IS NULL
              AND json_type(
                batch.body_json,
                '$.samples[' || NEW.position || '].speedMps'
              ) = 'null')
            OR json_extract(
              batch.body_json,
              '$.samples[' || NEW.position || '].speedMps'
            ) = event.speed_mps
          )
          AND (
            (event.heading_degrees IS NULL
              AND json_type(
                batch.body_json,
                '$.samples[' || NEW.position || '].headingDegrees'
              ) = 'null')
            OR json_extract(
              batch.body_json,
              '$.samples[' || NEW.position || '].headingDegrees'
            ) = event.heading_degrees
          )
          AND json_extract(
            batch.body_json,
            '$.samples[' || NEW.position || '].activityHint'
          ) = 'unknown'
          AND (
            (event.is_mock_location IS NULL
              AND json_type(
                batch.body_json,
                '$.samples[' || NEW.position || '].isMockLocation'
              ) = 'null')
            OR (
              json_type(
                batch.body_json,
                '$.samples[' || NEW.position || '].isMockLocation'
              ) IN ('true', 'false')
              AND json_extract(
                batch.body_json,
                '$.samples[' || NEW.position || '].isMockLocation'
              ) = event.is_mock_location
            )
          )
          AND json_extract(
            batch.body_json,
            '$.samples[' || NEW.position || '].source'
          ) = 'phone_gps'
      )
    BEGIN
      SELECT RAISE(ABORT, 'EVENT_NOT_UPLOADABLE');
    END;

  CREATE TRIGGER IF NOT EXISTS mark_batch_item_batched
    AFTER INSERT ON telemetry_upload_batch_item
    BEGIN
      UPDATE outbox_delivery
      SET state = 'batched'
      WHERE event_id = NEW.event_id
        AND delivery_scope = 'telemetry_upload'
        AND state = 'pending';
    END;

  CREATE TRIGGER IF NOT EXISTS retain_upload_batch_item
    BEFORE DELETE ON telemetry_upload_batch_item
    BEGIN
      SELECT RAISE(ABORT, 'UPLOAD_BATCH_ITEM_RETENTION_REQUIRED');
    END;

  CREATE TRIGGER IF NOT EXISTS immutable_upload_batch_item
    BEFORE UPDATE ON telemetry_upload_batch_item
    BEGIN
      SELECT RAISE(ABORT, 'UPLOAD_BATCH_ITEM_IMMUTABLE');
    END;

  CREATE TRIGGER IF NOT EXISTS require_batch_item_for_batched_delivery
    BEFORE UPDATE OF state ON outbox_delivery
    WHEN NEW.state = 'batched'
      AND OLD.state != 'batched'
      AND NOT EXISTS (
        SELECT 1 FROM telemetry_upload_batch_item
        WHERE event_id = NEW.event_id
      )
    BEGIN
      SELECT RAISE(ABORT, 'BATCH_ITEM_REQUIRED');
    END;

  CREATE TRIGGER IF NOT EXISTS enforce_upload_batch_cardinality
    BEFORE UPDATE OF state ON telemetry_upload_batch
    WHEN NEW.state IN ('leased', 'acknowledged', 'held')
      AND (
        (SELECT COUNT(*) FROM telemetry_upload_batch_item
         WHERE client_batch_id = NEW.client_batch_id) != NEW.sample_count
        OR (SELECT MIN(position) FROM telemetry_upload_batch_item
            WHERE client_batch_id = NEW.client_batch_id) != 0
        OR (SELECT MAX(position) FROM telemetry_upload_batch_item
            WHERE client_batch_id = NEW.client_batch_id) != NEW.sample_count - 1
      )
    BEGIN
      SELECT RAISE(ABORT, 'UPLOAD_BATCH_CARDINALITY_MISMATCH');
    END;

  CREATE TRIGGER IF NOT EXISTS enforce_upload_batch_state_transition
    BEFORE UPDATE OF state ON telemetry_upload_batch
    WHEN NEW.state IS NOT OLD.state
      AND NOT (
        (OLD.state = 'pending' AND NEW.state IN ('leased', 'held'))
        OR
        (OLD.state = 'leased' AND NEW.state IN ('pending', 'acknowledged', 'held'))
      )
    BEGIN
      SELECT RAISE(ABORT, 'UPLOAD_BATCH_STATE_TRANSITION_INVALID');
    END;

  CREATE TRIGGER IF NOT EXISTS immutable_terminal_upload_batch
    BEFORE UPDATE ON telemetry_upload_batch
    WHEN OLD.state IN ('acknowledged', 'held')
    BEGIN
      SELECT RAISE(ABORT, 'UPLOAD_BATCH_TERMINAL');
    END;

  CREATE TRIGGER IF NOT EXISTS enforce_outbox_state_transition
    BEFORE UPDATE OF state ON outbox_delivery
    WHEN NEW.state IS NOT OLD.state
      AND NOT (
        (OLD.state = 'pending' AND NEW.state = 'batched'
          AND EXISTS (
            SELECT 1 FROM telemetry_upload_batch_item
            WHERE event_id = NEW.event_id
          ))
        OR
        (OLD.state = 'batched' AND NEW.state = 'acknowledged'
          AND EXISTS (
            SELECT 1
            FROM telemetry_upload_batch_item AS item
            JOIN telemetry_upload_batch AS batch
              ON batch.client_batch_id = item.client_batch_id
            WHERE item.event_id = NEW.event_id
              AND batch.state = 'acknowledged'
          ))
        OR
        (OLD.state = 'batched' AND NEW.state = 'held'
          AND EXISTS (
            SELECT 1
            FROM telemetry_upload_batch_item AS item
            JOIN telemetry_upload_batch AS batch
              ON batch.client_batch_id = item.client_batch_id
            WHERE item.event_id = NEW.event_id
              AND batch.state = 'held'
          ))
      )
    BEGIN
      SELECT RAISE(ABORT, 'OUTBOX_STATE_TRANSITION_INVALID');
    END;

  CREATE TABLE IF NOT EXISTS app_metadata (
    key TEXT PRIMARY KEY NOT NULL,
    value TEXT NOT NULL
  );

  PRAGMA user_version = 3;
`;

// The caller must disable foreign_keys before beginning this transaction,
// run foreign_key_check before commit, then re-enable enforcement. Existing
// v1 rows are deliberately migrated to local-only/non-deliverable state.
export const MIGRATE_TELEMETRY_V1_TO_V2_SQL = `
  CREATE TABLE trip_session_projection_v2 (
    session_id TEXT PRIMARY KEY NOT NULL,
    installation_id TEXT NOT NULL,
    tenant_id TEXT,
    actor_id TEXT,
    mobility_device_id TEXT,
    server_trip_id TEXT,
    consent_revision_id TEXT,
    upload_eligibility TEXT NOT NULL CHECK (
      upload_eligibility IN ('development_local_only', 'server_bound')
    ),
    started_at TEXT NOT NULL,
    ended_at TEXT,
    state TEXT NOT NULL CHECK (state IN ('recording', 'stopped')),
    next_event_sequence INTEGER NOT NULL DEFAULT 0,
    next_sample_sequence INTEGER NOT NULL DEFAULT 0,
    accepted_sample_count INTEGER NOT NULL DEFAULT 0,
    rejected_sample_count INTEGER NOT NULL DEFAULT 0,
    last_sample_at TEXT,
    updated_at TEXT NOT NULL,
    UNIQUE (
      session_id, installation_id, tenant_id, mobility_device_id,
      server_trip_id, consent_revision_id
    ),
    CHECK (
      (upload_eligibility = 'development_local_only'
        AND tenant_id IS NULL
        AND mobility_device_id IS NULL
        AND server_trip_id IS NULL
        AND consent_revision_id IS NULL)
      OR
      (upload_eligibility = 'server_bound'
        AND tenant_id IS NOT NULL
        AND mobility_device_id IS NOT NULL
        AND server_trip_id IS NOT NULL
        AND consent_revision_id IS NOT NULL)
    )
  );

  INSERT INTO trip_session_projection_v2 (
    session_id, installation_id, tenant_id, actor_id, mobility_device_id,
    server_trip_id, consent_revision_id, upload_eligibility,
    started_at, ended_at, state, next_event_sequence, next_sample_sequence,
    accepted_sample_count, rejected_sample_count, last_sample_at, updated_at
  )
  SELECT
    session_id, installation_id, NULL, actor_id, NULL,
    NULL, NULL, 'development_local_only',
    started_at, ended_at, state, next_event_sequence, next_sample_sequence,
    accepted_sample_count, rejected_sample_count, last_sample_at, updated_at
  FROM trip_session_projection;

  DROP TABLE trip_session_projection;
  ALTER TABLE trip_session_projection_v2 RENAME TO trip_session_projection;

  CREATE UNIQUE INDEX one_recording_session
    ON trip_session_projection(state)
    WHERE state = 'recording';

  CREATE TRIGGER immutable_session_upload_scope
    BEFORE UPDATE OF
      session_id, installation_id, tenant_id, mobility_device_id, server_trip_id,
      consent_revision_id, upload_eligibility
    ON trip_session_projection
    WHEN
      OLD.session_id IS NOT NEW.session_id
      OR OLD.installation_id IS NOT NEW.installation_id
      OR OLD.tenant_id IS NOT NEW.tenant_id
      OR OLD.mobility_device_id IS NOT NEW.mobility_device_id
      OR OLD.server_trip_id IS NOT NEW.server_trip_id
      OR OLD.consent_revision_id IS NOT NEW.consent_revision_id
      OR OLD.upload_eligibility IS NOT NEW.upload_eligibility
    BEGIN
      SELECT RAISE(ABORT, 'SESSION_UPLOAD_SCOPE_IMMUTABLE');
    END;

  CREATE TRIGGER immutable_trip_event
    BEFORE UPDATE ON trip_event_log
    BEGIN
      SELECT RAISE(ABORT, 'TRIP_EVENT_IMMUTABLE');
    END;

  CREATE TRIGGER retain_trip_event
    BEFORE DELETE ON trip_event_log
    BEGIN
      SELECT RAISE(ABORT, 'TRIP_EVENT_RETENTION_REQUIRED');
    END;

  CREATE TABLE outbox_delivery_v2 (
    event_id TEXT PRIMARY KEY NOT NULL,
    delivery_scope TEXT NOT NULL CHECK (
      delivery_scope IN ('local_only', 'telemetry_upload')
    ),
    state TEXT NOT NULL CHECK (
      state IN ('not_applicable', 'pending', 'batched', 'acknowledged', 'held')
    ),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TEXT,
    acknowledged_at TEXT,
    last_error_code TEXT,
    FOREIGN KEY (event_id) REFERENCES trip_event_log(event_id),
    CHECK (
      (delivery_scope = 'local_only' AND state = 'not_applicable')
      OR
      (delivery_scope = 'telemetry_upload' AND state != 'not_applicable')
    ),
    CHECK (
      (state = 'acknowledged' AND acknowledged_at IS NOT NULL)
      OR
      (state != 'acknowledged' AND acknowledged_at IS NULL)
    )
  );

  INSERT INTO outbox_delivery_v2 (
    event_id, delivery_scope, state, attempt_count,
    next_attempt_at, acknowledged_at, last_error_code
  )
  SELECT event_id, 'local_only', 'not_applicable', 0, NULL, NULL, NULL
  FROM outbox_delivery;

  DROP TABLE outbox_delivery;
  ALTER TABLE outbox_delivery_v2 RENAME TO outbox_delivery;

  CREATE INDEX pending_outbox
    ON outbox_delivery(delivery_scope, state, next_attempt_at);

  CREATE TRIGGER immutable_outbox_delivery_scope
    BEFORE UPDATE OF delivery_scope ON outbox_delivery
    WHEN OLD.delivery_scope IS NOT NEW.delivery_scope
    BEGIN
      SELECT RAISE(ABORT, 'OUTBOX_DELIVERY_SCOPE_IMMUTABLE');
    END;

  CREATE TRIGGER validate_initial_outbox_state
    BEFORE INSERT ON outbox_delivery
    WHEN NOT (
      NEW.attempt_count = 0
      AND NEW.next_attempt_at IS NULL
      AND NEW.acknowledged_at IS NULL
      AND NEW.last_error_code IS NULL
      AND (
        (NEW.delivery_scope = 'local_only' AND NEW.state = 'not_applicable')
        OR
        (NEW.delivery_scope = 'telemetry_upload' AND NEW.state = 'pending')
      )
    )
    BEGIN
      SELECT RAISE(ABORT, 'OUTBOX_INITIAL_STATE_INVALID');
    END;

  CREATE TRIGGER retain_outbox_delivery
    BEFORE DELETE ON outbox_delivery
    BEGIN
      SELECT RAISE(ABORT, 'OUTBOX_DELIVERY_RETENTION_REQUIRED');
    END;

  CREATE TRIGGER require_server_bound_upload_delivery
    BEFORE INSERT ON outbox_delivery
    WHEN NEW.delivery_scope = 'telemetry_upload'
      AND NOT EXISTS (
        SELECT 1
        FROM trip_event_log AS event
        JOIN trip_session_projection AS session
          ON session.session_id = event.session_id
        WHERE event.event_id = NEW.event_id
          AND event.event_type = 'location_sample'
          AND session.upload_eligibility = 'server_bound'
      )
    BEGIN
      SELECT RAISE(ABORT, 'SERVER_BOUND_LOCATION_REQUIRED');
    END;

  CREATE TRIGGER require_upload_delivery_for_server_bound_location
    BEFORE INSERT ON outbox_delivery
    WHEN NEW.delivery_scope = 'local_only'
      AND EXISTS (
        SELECT 1
        FROM trip_event_log AS event
        JOIN trip_session_projection AS session
          ON session.session_id = event.session_id
        WHERE event.event_id = NEW.event_id
          AND event.event_type = 'location_sample'
          AND session.upload_eligibility = 'server_bound'
      )
    BEGIN
      SELECT RAISE(ABORT, 'SERVER_BOUND_LOCATION_MUST_UPLOAD');
    END;

  CREATE TABLE telemetry_upload_batch (
    client_batch_id TEXT PRIMARY KEY NOT NULL,
    session_id TEXT NOT NULL,
    installation_id TEXT NOT NULL,
    tenant_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    server_trip_id TEXT NOT NULL,
    consent_revision_id TEXT NOT NULL,
    body_json TEXT NOT NULL,
    body_sha256 TEXT NOT NULL CHECK (length(body_sha256) = 64),
    sample_count INTEGER NOT NULL CHECK (sample_count BETWEEN 1 AND 500),
    state TEXT NOT NULL CHECK (
      state IN ('pending', 'leased', 'acknowledged', 'held')
    ),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    lease_owner_id TEXT,
    lease_expires_at TEXT,
    next_attempt_at TEXT,
    receipt_id TEXT,
    server_batch_id TEXT,
    server_state TEXT CHECK (server_state IN ('stored', 'queued', 'projected')),
    replay INTEGER CHECK (replay IN (0, 1)),
    acknowledged_at TEXT,
    last_error_code TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (client_batch_id, session_id),
    FOREIGN KEY (
      session_id, installation_id, tenant_id, device_id,
      server_trip_id, consent_revision_id
    ) REFERENCES trip_session_projection (
      session_id, installation_id, tenant_id, mobility_device_id,
      server_trip_id, consent_revision_id
    ),
    CHECK (
      (state = 'leased' AND lease_owner_id IS NOT NULL AND lease_expires_at IS NOT NULL)
      OR
      (state != 'leased' AND lease_owner_id IS NULL AND lease_expires_at IS NULL)
    ),
    CHECK (
      (state = 'acknowledged'
        AND receipt_id IS NOT NULL
        AND server_batch_id IS NOT NULL
        AND server_state IS NOT NULL
        AND replay IS NOT NULL
        AND acknowledged_at IS NOT NULL)
      OR
      (state != 'acknowledged'
        AND receipt_id IS NULL
        AND server_batch_id IS NULL
        AND server_state IS NULL
        AND replay IS NULL
        AND acknowledged_at IS NULL)
    )
  );

  CREATE INDEX due_upload_batch
    ON telemetry_upload_batch(state, next_attempt_at, created_at);

  CREATE TRIGGER immutable_upload_batch_body
    BEFORE UPDATE OF
      client_batch_id, session_id, installation_id, tenant_id, device_id,
      server_trip_id, consent_revision_id, body_json, body_sha256,
      sample_count, created_at
    ON telemetry_upload_batch
    BEGIN
      SELECT RAISE(ABORT, 'UPLOAD_BATCH_BODY_IMMUTABLE');
    END;

  CREATE TRIGGER validate_initial_upload_batch_state
    BEFORE INSERT ON telemetry_upload_batch
    WHEN NOT (
      NEW.state = 'pending'
      AND NEW.attempt_count = 0
      AND NEW.lease_owner_id IS NULL
      AND NEW.lease_expires_at IS NULL
      AND NEW.next_attempt_at IS NULL
      AND NEW.receipt_id IS NULL
      AND NEW.server_batch_id IS NULL
      AND NEW.server_state IS NULL
      AND NEW.replay IS NULL
      AND NEW.acknowledged_at IS NULL
      AND NEW.last_error_code IS NULL
      AND json_valid(NEW.body_json) = 1
      AND (SELECT COUNT(*) FROM json_each(NEW.body_json)) = 10
      AND json_extract(NEW.body_json, '$.schemaVersion') = 'telemetry-batch.v2'
      AND json_extract(NEW.body_json, '$.clientBatchId') = NEW.client_batch_id
      AND json_extract(NEW.body_json, '$.tenantId') = NEW.tenant_id
      AND json_extract(NEW.body_json, '$.deviceId') = NEW.device_id
      AND json_extract(NEW.body_json, '$.tripId') = NEW.server_trip_id
      AND json_extract(NEW.body_json, '$.clientSessionId') = NEW.session_id
      AND json_extract(NEW.body_json, '$.installationId') = NEW.installation_id
      AND json_extract(NEW.body_json, '$.consentRevisionId') = NEW.consent_revision_id
      AND json_type(NEW.body_json, '$.sentAt') = 'text'
      AND json_type(NEW.body_json, '$.samples') = 'array'
      AND json_array_length(NEW.body_json, '$.samples') = NEW.sample_count
      AND NOT EXISTS (
        SELECT 1 FROM json_each(NEW.body_json, '$.samples') AS sample
        WHERE json_type(sample.value) != 'object'
          OR (SELECT COUNT(*) FROM json_each(sample.value)) != 12
      )
    )
    BEGIN
      SELECT RAISE(ABORT, 'UPLOAD_BATCH_INITIAL_STATE_INVALID');
    END;

  CREATE TRIGGER retain_upload_batch_identity
    BEFORE DELETE ON telemetry_upload_batch
    BEGIN
      SELECT RAISE(ABORT, 'UPLOAD_BATCH_RETENTION_REQUIRED');
    END;

  CREATE TABLE telemetry_upload_batch_item (
    client_batch_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    position INTEGER NOT NULL CHECK (position BETWEEN 0 AND 499),
    event_id TEXT NOT NULL UNIQUE,
    PRIMARY KEY (client_batch_id, position),
    FOREIGN KEY (client_batch_id, session_id)
      REFERENCES telemetry_upload_batch(client_batch_id, session_id),
    FOREIGN KEY (event_id, session_id)
      REFERENCES trip_event_log(event_id, session_id)
  );

  CREATE UNIQUE INDEX event_session_identity
    ON trip_event_log(event_id, session_id);

  CREATE TRIGGER require_uploadable_batch_item
    BEFORE INSERT ON telemetry_upload_batch_item
    WHEN
      NOT EXISTS (
        SELECT 1 FROM trip_event_log
        WHERE event_id = NEW.event_id
          AND session_id = NEW.session_id
          AND event_type = 'location_sample'
      )
      OR NOT EXISTS (
        SELECT 1 FROM outbox_delivery
        WHERE event_id = NEW.event_id
          AND delivery_scope = 'telemetry_upload'
          AND state = 'pending'
      )
      OR NOT EXISTS (
        SELECT 1
        FROM telemetry_upload_batch AS batch
        JOIN trip_event_log AS event
          ON event.event_id = NEW.event_id
         AND event.session_id = NEW.session_id
        WHERE batch.client_batch_id = NEW.client_batch_id
          AND batch.session_id = NEW.session_id
          AND json_extract(
            batch.body_json,
            '$.samples[' || NEW.position || '].clientSampleId'
          ) = event.event_id
          AND json_extract(
            batch.body_json,
            '$.samples[' || NEW.position || '].sequence'
          ) = event.sample_sequence
          AND json_extract(
            batch.body_json,
            '$.samples[' || NEW.position || '].capturedAt'
          ) = event.occurred_at
          AND json_extract(
            batch.body_json,
            '$.samples[' || NEW.position || '].latitude'
          ) = event.latitude
          AND json_extract(
            batch.body_json,
            '$.samples[' || NEW.position || '].longitude'
          ) = event.longitude
          AND (
            (event.horizontal_accuracy_m IS NULL
              AND json_type(
                batch.body_json,
                '$.samples[' || NEW.position || '].horizontalAccuracyM'
              ) = 'null')
            OR json_extract(
              batch.body_json,
              '$.samples[' || NEW.position || '].horizontalAccuracyM'
            ) = event.horizontal_accuracy_m
          )
          AND (
            (event.altitude_m IS NULL
              AND json_type(
                batch.body_json,
                '$.samples[' || NEW.position || '].altitudeM'
              ) = 'null')
            OR json_extract(
              batch.body_json,
              '$.samples[' || NEW.position || '].altitudeM'
            ) = event.altitude_m
          )
          AND (
            (event.speed_mps IS NULL
              AND json_type(
                batch.body_json,
                '$.samples[' || NEW.position || '].speedMps'
              ) = 'null')
            OR json_extract(
              batch.body_json,
              '$.samples[' || NEW.position || '].speedMps'
            ) = event.speed_mps
          )
          AND (
            (event.heading_degrees IS NULL
              AND json_type(
                batch.body_json,
                '$.samples[' || NEW.position || '].headingDegrees'
              ) = 'null')
            OR json_extract(
              batch.body_json,
              '$.samples[' || NEW.position || '].headingDegrees'
            ) = event.heading_degrees
          )
          AND json_extract(
            batch.body_json,
            '$.samples[' || NEW.position || '].activityHint'
          ) = 'unknown'
          AND (
            (event.is_mock_location IS NULL
              AND json_type(
                batch.body_json,
                '$.samples[' || NEW.position || '].isMockLocation'
              ) = 'null')
            OR (
              json_type(
                batch.body_json,
                '$.samples[' || NEW.position || '].isMockLocation'
              ) IN ('true', 'false')
              AND json_extract(
                batch.body_json,
                '$.samples[' || NEW.position || '].isMockLocation'
              ) = event.is_mock_location
            )
          )
          AND json_extract(
            batch.body_json,
            '$.samples[' || NEW.position || '].source'
          ) = 'phone_gps'
      )
    BEGIN
      SELECT RAISE(ABORT, 'EVENT_NOT_UPLOADABLE');
    END;

  CREATE TRIGGER mark_batch_item_batched
    AFTER INSERT ON telemetry_upload_batch_item
    BEGIN
      UPDATE outbox_delivery
      SET state = 'batched'
      WHERE event_id = NEW.event_id
        AND delivery_scope = 'telemetry_upload'
        AND state = 'pending';
    END;

  CREATE TRIGGER retain_upload_batch_item
    BEFORE DELETE ON telemetry_upload_batch_item
    BEGIN
      SELECT RAISE(ABORT, 'UPLOAD_BATCH_ITEM_RETENTION_REQUIRED');
    END;

  CREATE TRIGGER immutable_upload_batch_item
    BEFORE UPDATE ON telemetry_upload_batch_item
    BEGIN
      SELECT RAISE(ABORT, 'UPLOAD_BATCH_ITEM_IMMUTABLE');
    END;

  CREATE TRIGGER require_batch_item_for_batched_delivery
    BEFORE UPDATE OF state ON outbox_delivery
    WHEN NEW.state = 'batched'
      AND OLD.state != 'batched'
      AND NOT EXISTS (
        SELECT 1 FROM telemetry_upload_batch_item
        WHERE event_id = NEW.event_id
      )
    BEGIN
      SELECT RAISE(ABORT, 'BATCH_ITEM_REQUIRED');
    END;

  CREATE TRIGGER enforce_upload_batch_cardinality
    BEFORE UPDATE OF state ON telemetry_upload_batch
    WHEN NEW.state IN ('leased', 'acknowledged', 'held')
      AND (
        (SELECT COUNT(*) FROM telemetry_upload_batch_item
         WHERE client_batch_id = NEW.client_batch_id) != NEW.sample_count
        OR (SELECT MIN(position) FROM telemetry_upload_batch_item
            WHERE client_batch_id = NEW.client_batch_id) != 0
        OR (SELECT MAX(position) FROM telemetry_upload_batch_item
            WHERE client_batch_id = NEW.client_batch_id) != NEW.sample_count - 1
      )
    BEGIN
      SELECT RAISE(ABORT, 'UPLOAD_BATCH_CARDINALITY_MISMATCH');
    END;

  CREATE TRIGGER enforce_upload_batch_state_transition
    BEFORE UPDATE OF state ON telemetry_upload_batch
    WHEN NEW.state IS NOT OLD.state
      AND NOT (
        (OLD.state = 'pending' AND NEW.state IN ('leased', 'held'))
        OR
        (OLD.state = 'leased' AND NEW.state IN ('pending', 'acknowledged', 'held'))
      )
    BEGIN
      SELECT RAISE(ABORT, 'UPLOAD_BATCH_STATE_TRANSITION_INVALID');
    END;

  CREATE TRIGGER immutable_terminal_upload_batch
    BEFORE UPDATE ON telemetry_upload_batch
    WHEN OLD.state IN ('acknowledged', 'held')
    BEGIN
      SELECT RAISE(ABORT, 'UPLOAD_BATCH_TERMINAL');
    END;

  CREATE TRIGGER enforce_outbox_state_transition
    BEFORE UPDATE OF state ON outbox_delivery
    WHEN NEW.state IS NOT OLD.state
      AND NOT (
        (OLD.state = 'pending' AND NEW.state = 'batched'
          AND EXISTS (
            SELECT 1 FROM telemetry_upload_batch_item
            WHERE event_id = NEW.event_id
          ))
        OR
        (OLD.state = 'batched' AND NEW.state = 'acknowledged'
          AND EXISTS (
            SELECT 1
            FROM telemetry_upload_batch_item AS item
            JOIN telemetry_upload_batch AS batch
              ON batch.client_batch_id = item.client_batch_id
            WHERE item.event_id = NEW.event_id
              AND batch.state = 'acknowledged'
          ))
        OR
        (OLD.state = 'batched' AND NEW.state = 'held'
          AND EXISTS (
            SELECT 1
            FROM telemetry_upload_batch_item AS item
            JOIN telemetry_upload_batch AS batch
              ON batch.client_batch_id = item.client_batch_id
            WHERE item.event_id = NEW.event_id
              AND batch.state = 'held'
          ))
      )
    BEGIN
      SELECT RAISE(ABORT, 'OUTBOX_STATE_TRANSITION_INVALID');
    END;

  PRAGMA user_version = 2;
`;

// Schema v2 accepted a NULL body scope because `WHEN NOT (...)` evaluates to
// NULL, not true, when one of the JSON comparisons is NULL. Recreate the
// trigger with an explicit fail-closed COALESCE before enabling v3.
export const MIGRATE_TELEMETRY_V2_TO_V3_SQL = `
  DROP TRIGGER IF EXISTS validate_initial_upload_batch_state;

  CREATE TRIGGER validate_initial_upload_batch_state
    BEFORE INSERT ON telemetry_upload_batch
    WHEN NOT COALESCE((
      NEW.state = 'pending'
      AND NEW.attempt_count = 0
      AND NEW.lease_owner_id IS NULL
      AND NEW.lease_expires_at IS NULL
      AND NEW.next_attempt_at IS NULL
      AND NEW.receipt_id IS NULL
      AND NEW.server_batch_id IS NULL
      AND NEW.server_state IS NULL
      AND NEW.replay IS NULL
      AND NEW.acknowledged_at IS NULL
      AND NEW.last_error_code IS NULL
      AND json_valid(NEW.body_json) = 1
      AND (SELECT COUNT(*) FROM json_each(NEW.body_json)) = 10
      AND json_extract(NEW.body_json, '$.schemaVersion') = 'telemetry-batch.v2'
      AND json_extract(NEW.body_json, '$.clientBatchId') = NEW.client_batch_id
      AND json_extract(NEW.body_json, '$.tenantId') = NEW.tenant_id
      AND json_extract(NEW.body_json, '$.deviceId') = NEW.device_id
      AND json_extract(NEW.body_json, '$.tripId') = NEW.server_trip_id
      AND json_extract(NEW.body_json, '$.clientSessionId') = NEW.session_id
      AND json_extract(NEW.body_json, '$.installationId') = NEW.installation_id
      AND json_extract(NEW.body_json, '$.consentRevisionId') = NEW.consent_revision_id
      AND json_type(NEW.body_json, '$.sentAt') = 'text'
      AND json_type(NEW.body_json, '$.samples') = 'array'
      AND json_array_length(NEW.body_json, '$.samples') = NEW.sample_count
      AND NOT EXISTS (
        SELECT 1 FROM json_each(NEW.body_json, '$.samples') AS sample
        WHERE json_type(sample.value) != 'object'
          OR (SELECT COUNT(*) FROM json_each(sample.value)) != 12
      )
    ), 0)
    BEGIN
      SELECT RAISE(ABORT, 'UPLOAD_BATCH_INITIAL_STATE_INVALID');
    END;

  PRAGMA user_version = 3;
`;
