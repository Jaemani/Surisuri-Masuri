import { isLowercaseSha256, requireLowercaseSha256 } from './uploadDigest';
import { MAXIMUM_UPLOAD_RETRY_DELAY_MS } from './uploadRetryPolicy';

type SqlValue = string | number | null;

const MAX_UPLOAD_LEASE_DURATION_MS = 5 * 60 * 1_000;
const MAX_UPLOAD_CANDIDATE_SCAN = 100;
const DIGEST_MISMATCH_CODE = 'local_body_digest_mismatch';
const RETRY_METADATA_INVALID_CODE = 'local_retry_metadata_invalid';
const LEASE_METADATA_INVALID_CODE = 'local_lease_metadata_invalid';
const ATTEMPT_METADATA_INVALID_CODE = 'local_attempt_metadata_invalid';
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

type UploadHoldReason =
  | typeof DIGEST_MISMATCH_CODE
  | typeof RETRY_METADATA_INVALID_CODE
  | typeof LEASE_METADATA_INVALID_CODE
  | typeof ATTEMPT_METADATA_INVALID_CODE;

export type UploadLeaseTransaction = {
  getFirstAsync<T>(source: string, ...params: SqlValue[]): Promise<T | null>;
  getAllAsync<T>(source: string, ...params: SqlValue[]): Promise<T[]>;
  runAsync(
    source: string,
    ...params: SqlValue[]
  ): Promise<{ changes: number }>;
};

export type UploadLeaseDatabase = {
  withExclusiveTransactionAsync(
    task: (transaction: UploadLeaseTransaction) => Promise<void>,
  ): Promise<void>;
};

export type UploadLeaseReadDatabase = Pick<UploadLeaseTransaction, 'getFirstAsync'>;

export type UploadLeaseDependencies = {
  createLeaseOwnerId(): string;
  leaseExpiresAt(now: string): string;
  now(): string;
  sha256(body: string): Promise<string>;
};

export type UploadLeaseReference = {
  clientBatchId: string;
  sessionId: string;
  sampleCount: number;
  attemptCount: number;
  leaseOwnerId: string;
  leaseExpiresAt: string;
  body: string;
  bodySha256: string;
};

export type UploadLeaseResult =
  | { kind: 'none' }
  | {
      kind: 'held';
      clientBatchId: string;
      reason: UploadHoldReason;
    }
  | { kind: 'leased'; lease: UploadLeaseReference };

export type UploadLeaseCorrelation = { kind: 'committed' | 'unverifiable' };

type LeaseCandidateRow = {
  client_batch_id: string;
  session_id: string;
  sample_count: number;
  attempt_count_text: string;
  attempt_count_type: string;
  state: 'pending' | 'leased';
  lease_owner_id: string | null;
  lease_expires_at: string | null;
  next_attempt_at: string | null;
  body_json: string;
  body_sha256: string;
  updated_at: string;
};

const READ_LEASE_CANDIDATE_SQL = `
  SELECT
    client_batch_id, session_id, sample_count,
    CAST(attempt_count AS TEXT) AS attempt_count_text,
    typeof(attempt_count) AS attempt_count_type,
    state,
    lease_owner_id, lease_expires_at, next_attempt_at,
    body_json, body_sha256, updated_at
  FROM telemetry_upload_batch
  WHERE state IN ('pending', 'leased')
  ORDER BY created_at ASC, client_batch_id ASC
  LIMIT ?
`;

// SQL time ordering is only a bounded eligibility prefilter. The selected row
// still has to pass the canonical timestamp, control metadata, digest and CAS
// checks below before it can receive transport authority.
const READ_DUE_LEASE_CANDIDATE_SQL = `
  SELECT
    client_batch_id, session_id, sample_count,
    CAST(attempt_count AS TEXT) AS attempt_count_text,
    typeof(attempt_count) AS attempt_count_type,
    state,
    lease_owner_id, lease_expires_at, next_attempt_at,
    body_json, body_sha256, updated_at
  FROM telemetry_upload_batch
  WHERE
    (state = 'pending'
      AND (next_attempt_at IS NULL OR next_attempt_at <= ?))
    OR
    (state = 'leased'
      AND (lease_expires_at IS NULL OR lease_expires_at <= ?))
  ORDER BY created_at ASC, client_batch_id ASC
  LIMIT 1
`;

function canonicalUtcMilliseconds(value: string): number | null {
  const milliseconds = Date.parse(value);
  if (!Number.isFinite(milliseconds) || new Date(milliseconds).toISOString() !== value) {
    return null;
  }
  return milliseconds;
}

function requireCanonicalUtc(value: string, code: string): number {
  const milliseconds = canonicalUtcMilliseconds(value);
  if (milliseconds === null) throw new Error(code);
  return milliseconds;
}

function requireLeaseWindow(now: string, leaseExpiresAt: string): void {
  const nowMilliseconds = requireCanonicalUtc(now, 'UPLOAD_LEASE_NOW_INVALID');
  const expiryMilliseconds = requireCanonicalUtc(
    leaseExpiresAt,
    'UPLOAD_LEASE_EXPIRY_INVALID',
  );
  const duration = expiryMilliseconds - nowMilliseconds;
  if (duration <= 0 || duration > MAX_UPLOAD_LEASE_DURATION_MS) {
    throw new Error('UPLOAD_LEASE_EXPIRY_INVALID');
  }
}

function requireLeaseOwner(value: string): void {
  if (!UUID.test(value)) {
    throw new Error('UPLOAD_LEASE_OWNER_INVALID');
  }
}

function readAttemptCount(candidate: LeaseCandidateRow): number | null {
  if (
    candidate.attempt_count_type !== 'integer' ||
    !/^(0|[1-9][0-9]*)$/.test(candidate.attempt_count_text)
  ) {
    return null;
  }
  const attemptCount = Number(candidate.attempt_count_text);
  if (
    !Number.isSafeInteger(attemptCount) ||
    attemptCount < 0 ||
    attemptCount >= Number.MAX_SAFE_INTEGER
  ) {
    return null;
  }
  return attemptCount;
}

async function holdBatch(
  transaction: UploadLeaseTransaction,
  candidate: LeaseCandidateRow,
  now: string,
  reason: UploadHoldReason,
): Promise<void> {
  const batchUpdate =
    candidate.state === 'pending'
      ? await transaction.runAsync(
          `UPDATE telemetry_upload_batch
           SET state = 'held', lease_owner_id = NULL, lease_expires_at = NULL,
               next_attempt_at = NULL, last_error_code = ?, updated_at = ?
           WHERE client_batch_id = ?
             AND state = 'pending'
             AND typeof(attempt_count) = ?
             AND CAST(attempt_count AS TEXT) = ?
             AND body_json = ?
             AND body_sha256 = ?
             AND next_attempt_at IS ?`,
          reason,
          now,
          candidate.client_batch_id,
          candidate.attempt_count_type,
          candidate.attempt_count_text,
          candidate.body_json,
          candidate.body_sha256,
          candidate.next_attempt_at,
        )
      : await transaction.runAsync(
          `UPDATE telemetry_upload_batch
           SET state = 'held', lease_owner_id = NULL, lease_expires_at = NULL,
               next_attempt_at = NULL, last_error_code = ?, updated_at = ?
           WHERE client_batch_id = ?
             AND state = 'leased'
             AND typeof(attempt_count) = ?
             AND CAST(attempt_count AS TEXT) = ?
             AND lease_owner_id IS ?
             AND lease_expires_at IS ?
             AND body_json = ?
             AND body_sha256 = ?`,
          reason,
          now,
          candidate.client_batch_id,
          candidate.attempt_count_type,
          candidate.attempt_count_text,
          candidate.lease_owner_id,
          candidate.lease_expires_at,
          candidate.body_json,
          candidate.body_sha256,
        );
  if (batchUpdate.changes !== 1) {
    throw new Error('UPLOAD_BATCH_HOLD_CONFLICT');
  }

  const outboxUpdate = await transaction.runAsync(
    `UPDATE outbox_delivery
     SET state = 'held', next_attempt_at = NULL, last_error_code = ?
     WHERE state = 'batched'
       AND event_id IN (
         SELECT event_id FROM telemetry_upload_batch_item
         WHERE client_batch_id = ?
       )`,
    reason,
    candidate.client_batch_id,
  );
  if (outboxUpdate.changes !== candidate.sample_count) {
    throw new Error('UPLOAD_BATCH_HOLD_BINDING_INCOMPLETE');
  }
}

async function acquireLease(
  transaction: UploadLeaseTransaction,
  candidate: LeaseCandidateRow,
  now: string,
  leaseOwnerId: string,
  leaseExpiresAt: string,
  attemptCount: number,
): Promise<void> {
  const update =
    candidate.state === 'pending'
      ? await transaction.runAsync(
          `UPDATE telemetry_upload_batch
           SET state = 'leased', lease_owner_id = ?, lease_expires_at = ?,
               attempt_count = attempt_count + 1, next_attempt_at = NULL,
               last_error_code = NULL, updated_at = ?
           WHERE client_batch_id = ?
             AND state = 'pending'
             AND typeof(attempt_count) = 'integer'
             AND attempt_count = ?
             AND body_json = ?
             AND body_sha256 = ?
             AND next_attempt_at IS ?`,
          leaseOwnerId,
          leaseExpiresAt,
          now,
          candidate.client_batch_id,
          attemptCount,
          candidate.body_json,
          candidate.body_sha256,
          candidate.next_attempt_at,
        )
      : await transaction.runAsync(
          `UPDATE telemetry_upload_batch
           SET state = 'leased', lease_owner_id = ?, lease_expires_at = ?,
               attempt_count = attempt_count + 1, next_attempt_at = NULL,
               last_error_code = NULL, updated_at = ?
           WHERE client_batch_id = ?
             AND state = 'leased'
             AND typeof(attempt_count) = 'integer'
             AND attempt_count = ?
             AND lease_owner_id = ?
             AND lease_expires_at IS ?
             AND body_json = ?
             AND body_sha256 = ?`,
          leaseOwnerId,
          leaseExpiresAt,
          now,
          candidate.client_batch_id,
          attemptCount,
          candidate.lease_owner_id,
          candidate.lease_expires_at,
          candidate.body_json,
          candidate.body_sha256,
        );
  if (update.changes !== 1) {
    throw new Error('UPLOAD_BATCH_LEASE_CONFLICT');
  }
}

/**
 * Revalidates the exact stored UTF-8 body before granting transport authority.
 * The returned body is the same string that was hashed inside the transaction.
 */
export async function leaseNextUploadBatchCore(
  database: UploadLeaseDatabase,
  dependencies: UploadLeaseDependencies,
): Promise<UploadLeaseResult> {
  let result: UploadLeaseResult = { kind: 'none' };

  await database.withExclusiveTransactionAsync(async (transaction) => {
    const foreignKeys = await transaction.getFirstAsync<{ foreign_keys: number }>(
      'PRAGMA foreign_keys',
    );
    if (foreignKeys?.foreign_keys !== 1) {
      throw new Error('UPLOAD_DATABASE_FOREIGN_KEYS_DISABLED');
    }

    const candidates = await transaction.getAllAsync<LeaseCandidateRow>(
      READ_LEASE_CANDIDATE_SQL,
      MAX_UPLOAD_CANDIDATE_SCAN,
    );
    if (candidates.length === 0) return;

    const scanNow = dependencies.now();
    const scanNowMilliseconds = requireCanonicalUtc(
      scanNow,
      'UPLOAD_LEASE_NOW_INVALID',
    );
    type InspectedCandidate = {
      candidate: LeaseCandidateRow;
      attemptCount: number;
    };

    // A candidate row is eligible only after the persisted metadata has been
    // checked with the same canonical rules used by the CAS below. SQL is
    // deliberately only a cheap ordering/eligibility prefilter; it must never
    // grant upload authority by itself.
    const inspectCandidate = async (
      scannedCandidate: LeaseCandidateRow,
    ): Promise<InspectedCandidate | null> => {
      const persistedUpdatedAt = canonicalUtcMilliseconds(scannedCandidate.updated_at);
      const attemptCount = readAttemptCount(scannedCandidate);
      if (attemptCount === null) {
        await holdBatch(
          transaction,
          scannedCandidate,
          scanNow,
          ATTEMPT_METADATA_INVALID_CODE,
        );
        result = {
          kind: 'held',
          clientBatchId: scannedCandidate.client_batch_id,
          reason: ATTEMPT_METADATA_INVALID_CODE,
        };
        return null;
      }
      if (persistedUpdatedAt === null) {
        const reason =
          scannedCandidate.state === 'pending'
            ? RETRY_METADATA_INVALID_CODE
            : LEASE_METADATA_INVALID_CODE;
        await holdBatch(transaction, scannedCandidate, scanNow, reason);
        result = {
          kind: 'held',
          clientBatchId: scannedCandidate.client_batch_id,
          reason,
        };
        return null;
      }
      if (persistedUpdatedAt > scanNowMilliseconds) {
        throw new Error('UPLOAD_LEASE_CLOCK_ROLLBACK');
      }

      let unavailableUntil: number | null = null;
      if (scannedCandidate.state === 'pending') {
        if (scannedCandidate.next_attempt_at !== null) {
          unavailableUntil = canonicalUtcMilliseconds(scannedCandidate.next_attempt_at);
          if (
            unavailableUntil === null ||
            unavailableUntil <= persistedUpdatedAt ||
            unavailableUntil - persistedUpdatedAt > MAXIMUM_UPLOAD_RETRY_DELAY_MS
          ) {
            await holdBatch(
              transaction,
              scannedCandidate,
              scanNow,
              RETRY_METADATA_INVALID_CODE,
            );
            result = {
              kind: 'held',
              clientBatchId: scannedCandidate.client_batch_id,
              reason: RETRY_METADATA_INVALID_CODE,
            };
            return null;
          }
        }
      }

      if (scannedCandidate.state === 'leased') {
        unavailableUntil =
          scannedCandidate.lease_expires_at === null
            ? null
            : canonicalUtcMilliseconds(scannedCandidate.lease_expires_at);
        if (
          scannedCandidate.lease_owner_id === null ||
          !UUID.test(scannedCandidate.lease_owner_id) ||
          unavailableUntil === null ||
          unavailableUntil <= persistedUpdatedAt ||
          unavailableUntil - persistedUpdatedAt > MAX_UPLOAD_LEASE_DURATION_MS
        ) {
          await holdBatch(
            transaction,
            scannedCandidate,
            scanNow,
            LEASE_METADATA_INVALID_CODE,
          );
          result = {
            kind: 'held',
            clientBatchId: scannedCandidate.client_batch_id,
            reason: LEASE_METADATA_INVALID_CODE,
          };
          return null;
        }
      }

      if (unavailableUntil === null || unavailableUntil <= scanNowMilliseconds) {
        return { candidate: scannedCandidate, attemptCount };
      }

      return null;
    };

    // Preserve the bounded FIFO/integrity scan first. This ensures an older
    // poisoned row is held and an actionable row inside the first 100 rows is
    // chosen before consulting any global SQL prefilter.
    let inspected: InspectedCandidate | null = null;
    for (const scannedCandidate of candidates) {
      inspected = await inspectCandidate(scannedCandidate);
      if (result.kind === 'held') return;
      if (inspected) break;
    }

    // If every row in the bounded window is a valid future retry/lease, a due
    // row may still exist beyond row 100. Use SQL only to locate the oldest
    // such row, then run the exact same canonical validation above before any
    // digest check or lease CAS. Avoid re-inspecting a row already covered by
    // the bounded scan if the SQL timestamp prefilter disagrees with the JS
    // canonical timestamp rules.
    if (!inspected) {
      const dueCandidate = await transaction.getFirstAsync<LeaseCandidateRow>(
        READ_DUE_LEASE_CANDIDATE_SQL,
        scanNow,
        scanNow,
      );
      if (
        dueCandidate &&
        !candidates.some(
          (candidateInWindow) =>
            candidateInWindow.client_batch_id === dueCandidate.client_batch_id,
        )
      ) {
        inspected = await inspectCandidate(dueCandidate);
        if (result.kind === 'held') return;
      }
    }

    if (!inspected) return;

    const { candidate, attemptCount } = inspected;

    if (!isLowercaseSha256(candidate.body_sha256)) {
      await holdBatch(transaction, candidate, scanNow, DIGEST_MISMATCH_CODE);
      result = {
        kind: 'held',
        clientBatchId: candidate.client_batch_id,
        reason: DIGEST_MISMATCH_CODE,
      };
      return;
    }

    let calculatedDigest: string;
    try {
      calculatedDigest = await dependencies.sha256(candidate.body_json);
    } catch {
      throw new Error('UPLOAD_BATCH_DIGEST_FAILED');
    }
    requireLowercaseSha256(calculatedDigest);

    if (calculatedDigest !== candidate.body_sha256) {
      await holdBatch(transaction, candidate, scanNow, DIGEST_MISMATCH_CODE);
      result = {
        kind: 'held',
        clientBatchId: candidate.client_batch_id,
        reason: DIGEST_MISMATCH_CODE,
      };
      return;
    }

    const grantNow = dependencies.now();
    const grantNowMilliseconds = requireCanonicalUtc(
      grantNow,
      'UPLOAD_LEASE_NOW_INVALID',
    );
    if (grantNowMilliseconds < scanNowMilliseconds) {
      throw new Error('UPLOAD_LEASE_CLOCK_ROLLBACK');
    }
    const leaseOwnerId = dependencies.createLeaseOwnerId();
    const leaseExpiresAt = dependencies.leaseExpiresAt(grantNow);
    requireLeaseOwner(leaseOwnerId);
    requireLeaseWindow(grantNow, leaseExpiresAt);
    await acquireLease(
      transaction,
      candidate,
      grantNow,
      leaseOwnerId,
      leaseExpiresAt,
      attemptCount,
    );

    result = {
      kind: 'leased',
      lease: {
        clientBatchId: candidate.client_batch_id,
        sessionId: candidate.session_id,
        sampleCount: candidate.sample_count,
        attemptCount: attemptCount + 1,
        leaseOwnerId,
        leaseExpiresAt,
        body: candidate.body_json,
        bodySha256: candidate.body_sha256,
      },
    };
  });

  return result;
}

type LeaseCorrelationRow = {
  client_batch_id: string;
  session_id: string;
  sample_count: number;
  attempt_count_text: string;
  attempt_count_type: string;
  state: 'pending' | 'leased' | 'acknowledged' | 'held';
  lease_owner_id: string | null;
  lease_expires_at: string | null;
  next_attempt_at: string | null;
  body_json: string;
  body_sha256: string;
  last_error_code: string | null;
  item_count: number;
  minimum_position: number | null;
  maximum_position: number | null;
  non_integer_position_count: number;
  batched_count: number;
  held_count: number;
  clean_held_count: number;
};

const READ_LEASE_CORRELATION_SQL = `
  SELECT
    batch.client_batch_id, batch.session_id, batch.sample_count,
    CAST(batch.attempt_count AS TEXT) AS attempt_count_text,
    typeof(batch.attempt_count) AS attempt_count_type,
    batch.state, batch.lease_owner_id, batch.lease_expires_at,
    batch.next_attempt_at, batch.body_json, batch.body_sha256,
    batch.last_error_code,
    (SELECT COUNT(*) FROM telemetry_upload_batch_item AS item
      WHERE item.client_batch_id = batch.client_batch_id) AS item_count,
    (SELECT MIN(position) FROM telemetry_upload_batch_item AS item
      WHERE item.client_batch_id = batch.client_batch_id) AS minimum_position,
    (SELECT MAX(position) FROM telemetry_upload_batch_item AS item
      WHERE item.client_batch_id = batch.client_batch_id) AS maximum_position,
    (SELECT COUNT(*) FROM telemetry_upload_batch_item AS item
      WHERE item.client_batch_id = batch.client_batch_id
        AND typeof(item.position) != 'integer') AS non_integer_position_count,
    (SELECT COUNT(*)
       FROM telemetry_upload_batch_item AS item
       JOIN outbox_delivery AS delivery ON delivery.event_id = item.event_id
      WHERE item.client_batch_id = batch.client_batch_id
        AND delivery.state = 'batched') AS batched_count,
    (SELECT COUNT(*)
       FROM telemetry_upload_batch_item AS item
       JOIN outbox_delivery AS delivery ON delivery.event_id = item.event_id
      WHERE item.client_batch_id = batch.client_batch_id
        AND delivery.state = 'held') AS held_count,
    (SELECT COUNT(*)
       FROM telemetry_upload_batch_item AS item
       JOIN outbox_delivery AS delivery ON delivery.event_id = item.event_id
      WHERE item.client_batch_id = batch.client_batch_id
        AND delivery.delivery_scope = 'telemetry_upload'
        AND delivery.state = 'held'
        AND delivery.attempt_count = 0
        AND delivery.next_attempt_at IS NULL
        AND delivery.acknowledged_at IS NULL
        AND delivery.last_error_code = batch.last_error_code) AS clean_held_count
  FROM telemetry_upload_batch AS batch
  WHERE batch.client_batch_id = ?
`;

function hasCompleteCorrelationBinding(row: LeaseCorrelationRow): boolean {
  return (
    row.item_count === row.sample_count &&
    row.minimum_position === 0 &&
    row.maximum_position === row.sample_count - 1 &&
    row.non_integer_position_count === 0
  );
}

export async function correlateUploadLeaseResultCore(
  database: UploadLeaseReadDatabase,
  result: Exclude<UploadLeaseResult, { kind: 'none' }>,
): Promise<UploadLeaseCorrelation> {
  const clientBatchId =
    result.kind === 'leased' ? result.lease.clientBatchId : result.clientBatchId;
  const row = await database.getFirstAsync<LeaseCorrelationRow>(
    READ_LEASE_CORRELATION_SQL,
    clientBatchId,
  );
  if (!row || !hasCompleteCorrelationBinding(row)) return { kind: 'unverifiable' };

  if (result.kind === 'leased') {
    const lease = result.lease;
    return row.client_batch_id === lease.clientBatchId &&
      row.session_id === lease.sessionId &&
      row.sample_count === lease.sampleCount &&
      row.attempt_count_type === 'integer' &&
      row.attempt_count_text === String(lease.attemptCount) &&
      row.state === 'leased' &&
      row.lease_owner_id === lease.leaseOwnerId &&
      row.lease_expires_at === lease.leaseExpiresAt &&
      row.next_attempt_at === null &&
      row.body_json === lease.body &&
      row.body_sha256 === lease.bodySha256 &&
      row.last_error_code === null &&
      row.batched_count === lease.sampleCount &&
      row.held_count === 0
      ? { kind: 'committed' }
      : { kind: 'unverifiable' };
  }

  return row.client_batch_id === result.clientBatchId &&
    row.state === 'held' &&
    row.lease_owner_id === null &&
    row.lease_expires_at === null &&
    row.next_attempt_at === null &&
    row.last_error_code === result.reason &&
    row.batched_count === 0 &&
    row.held_count === row.sample_count &&
    row.clean_held_count === row.sample_count
    ? { kind: 'committed' }
    : { kind: 'unverifiable' };
}
