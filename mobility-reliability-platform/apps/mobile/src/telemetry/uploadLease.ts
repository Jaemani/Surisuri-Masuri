import { isLowercaseSha256, requireLowercaseSha256 } from './uploadDigest';

type SqlValue = string | number | null;

const MAX_UPLOAD_LEASE_DURATION_MS = 5 * 60 * 1_000;
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
};

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
             AND lease_owner_id = ?
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

    const candidate = await transaction.getFirstAsync<LeaseCandidateRow>(
      `SELECT
         client_batch_id, session_id, sample_count,
         CAST(attempt_count AS TEXT) AS attempt_count_text,
         typeof(attempt_count) AS attempt_count_type,
         state,
         lease_owner_id, lease_expires_at, next_attempt_at,
         body_json, body_sha256
       FROM telemetry_upload_batch
       WHERE state IN ('pending', 'leased')
       ORDER BY created_at ASC, client_batch_id ASC
       LIMIT 1`,
    );
    if (!candidate) return;

    const now = dependencies.now();
    const nowMilliseconds = requireCanonicalUtc(now, 'UPLOAD_LEASE_NOW_INVALID');
    const attemptCount = readAttemptCount(candidate);
    if (attemptCount === null) {
      await holdBatch(transaction, candidate, now, ATTEMPT_METADATA_INVALID_CODE);
      result = {
        kind: 'held',
        clientBatchId: candidate.client_batch_id,
        reason: ATTEMPT_METADATA_INVALID_CODE,
      };
      return;
    }

    if (candidate.state === 'pending' && candidate.next_attempt_at !== null) {
      const retryAt = canonicalUtcMilliseconds(candidate.next_attempt_at);
      if (retryAt === null) {
        await holdBatch(transaction, candidate, now, RETRY_METADATA_INVALID_CODE);
        result = {
          kind: 'held',
          clientBatchId: candidate.client_batch_id,
          reason: RETRY_METADATA_INVALID_CODE,
        };
        return;
      }
      if (retryAt > nowMilliseconds) return;
    }

    if (candidate.state === 'leased') {
      const expiry =
        candidate.lease_expires_at === null
          ? null
          : canonicalUtcMilliseconds(candidate.lease_expires_at);
      if (
        candidate.lease_owner_id === null ||
        !UUID.test(candidate.lease_owner_id) ||
        expiry === null
      ) {
        await holdBatch(transaction, candidate, now, LEASE_METADATA_INVALID_CODE);
        result = {
          kind: 'held',
          clientBatchId: candidate.client_batch_id,
          reason: LEASE_METADATA_INVALID_CODE,
        };
        return;
      }
      if (expiry > nowMilliseconds) return;
    }

    if (!isLowercaseSha256(candidate.body_sha256)) {
      await holdBatch(transaction, candidate, now, DIGEST_MISMATCH_CODE);
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
      await holdBatch(transaction, candidate, now, DIGEST_MISMATCH_CODE);
      result = {
        kind: 'held',
        clientBatchId: candidate.client_batch_id,
        reason: DIGEST_MISMATCH_CODE,
      };
      return;
    }

    const leaseOwnerId = dependencies.createLeaseOwnerId();
    const leaseExpiresAt = dependencies.leaseExpiresAt(now);
    requireLeaseOwner(leaseOwnerId);
    requireLeaseWindow(now, leaseExpiresAt);
    await acquireLease(
      transaction,
      candidate,
      now,
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
