import { isLowercaseSha256 } from './uploadDigest';
import type { UploadLeaseReference } from './uploadLease';
import type {
  TelemetryAcknowledgment,
  UploadDisposition,
} from './syncProtocol';
import { MAXIMUM_UPLOAD_RETRY_DELAY_MS } from './uploadRetryPolicy';

type SqlValue = string | number | null;

const LOCAL_UUID =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const WIRE_UUID =
  /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;

const RETRY_CODES = new Set([
  'network_failure',
  'server_unavailable',
  'rate_limited',
  'invalid_acknowledgment',
]);
const HOLD_CODES = new Set([
  'authorization_rejected',
  'idempotency_conflict',
  'client_batch_conflict',
  'object_conflict',
  'unexpected_conflict',
  'payload_rejected',
  'unexpected_client_error',
]);

export type PersistableUploadDisposition = Exclude<
  UploadDisposition,
  { kind: 'reauthenticate' }
>;

export type UploadDispositionAuthority = Omit<UploadLeaseReference, 'body'>;

export type UploadDispositionCommand = {
  authority: UploadDispositionAuthority;
  disposition: PersistableUploadDisposition;
  observedAt: string;
  retryAt: string | null;
};

export type UploadDispositionTransaction = {
  getFirstAsync<T>(source: string, ...params: SqlValue[]): Promise<T | null>;
  runAsync(source: string, ...params: SqlValue[]): Promise<{ changes: number }>;
};

export type UploadDispositionDatabase = {
  withExclusiveTransactionAsync(
    task: (transaction: UploadDispositionTransaction) => Promise<void>,
  ): Promise<void>;
};

export type UploadDispositionReadDatabase = {
  getFirstAsync<T>(source: string, ...params: SqlValue[]): Promise<T | null>;
};

export type UploadDispositionCorrelation =
  | { kind: 'committed' }
  | { kind: 'not_committed' }
  | { kind: 'unverifiable' };

type DispositionRow = {
  client_batch_id: string;
  session_id: string;
  sample_count: number;
  state: 'pending' | 'leased' | 'acknowledged' | 'held';
  attempt_count_text: string;
  attempt_count_type: string;
  lease_owner_id: string | null;
  lease_expires_at: string | null;
  next_attempt_at: string | null;
  receipt_id: string | null;
  server_batch_id: string | null;
  server_state: 'stored' | 'queued' | 'projected' | null;
  replay: number | null;
  acknowledged_at: string | null;
  last_error_code: string | null;
  body_sha256: string;
  updated_at: string;
  item_count: number;
  minimum_position: number | null;
  maximum_position: number | null;
  non_integer_position_count: number;
  batched_count: number;
  acknowledged_count: number;
  held_count: number;
  clean_batched_count: number;
  clean_acknowledged_count: number;
  clean_held_count: number;
};

const READ_DISPOSITION_ROW_SQL = `
  SELECT
    batch.client_batch_id, batch.session_id, batch.sample_count, batch.state,
    CAST(batch.attempt_count AS TEXT) AS attempt_count_text,
    typeof(batch.attempt_count) AS attempt_count_type,
    batch.lease_owner_id, batch.lease_expires_at, batch.next_attempt_at,
    batch.receipt_id, batch.server_batch_id, batch.server_state, batch.replay,
    batch.acknowledged_at, batch.last_error_code, batch.body_sha256,
    batch.updated_at,
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
        AND delivery.state = 'acknowledged') AS acknowledged_count,
    (SELECT COUNT(*)
       FROM telemetry_upload_batch_item AS item
       JOIN outbox_delivery AS delivery ON delivery.event_id = item.event_id
      WHERE item.client_batch_id = batch.client_batch_id
        AND delivery.state = 'held') AS held_count
    ,(SELECT COUNT(*)
       FROM telemetry_upload_batch_item AS item
       JOIN outbox_delivery AS delivery ON delivery.event_id = item.event_id
      WHERE item.client_batch_id = batch.client_batch_id
        AND delivery.state = 'batched'
        AND delivery.attempt_count = 0
        AND delivery.next_attempt_at IS NULL
        AND delivery.acknowledged_at IS NULL
        AND delivery.last_error_code IS NULL) AS clean_batched_count
    ,(SELECT COUNT(*)
       FROM telemetry_upload_batch_item AS item
       JOIN outbox_delivery AS delivery ON delivery.event_id = item.event_id
      WHERE item.client_batch_id = batch.client_batch_id
        AND delivery.state = 'acknowledged'
        AND delivery.attempt_count = 0
        AND delivery.next_attempt_at IS NULL
        AND delivery.acknowledged_at = batch.acknowledged_at
        AND delivery.last_error_code IS NULL) AS clean_acknowledged_count
    ,(SELECT COUNT(*)
       FROM telemetry_upload_batch_item AS item
       JOIN outbox_delivery AS delivery ON delivery.event_id = item.event_id
      WHERE item.client_batch_id = batch.client_batch_id
        AND delivery.state = 'held'
        AND delivery.attempt_count = 0
        AND delivery.next_attempt_at IS NULL
        AND delivery.acknowledged_at IS NULL
        AND delivery.last_error_code = batch.last_error_code) AS clean_held_count
  FROM telemetry_upload_batch AS batch
  WHERE batch.client_batch_id = ?
`;

function canonicalUtcMilliseconds(value: string): number | null {
  const milliseconds = Date.parse(value);
  return Number.isFinite(milliseconds) && new Date(milliseconds).toISOString() === value
    ? milliseconds
    : null;
}

function requireCanonicalUtc(value: string, code: string): number {
  const milliseconds = canonicalUtcMilliseconds(value);
  if (milliseconds === null) throw new Error(code);
  return milliseconds;
}

function requireAuthority(authority: UploadDispositionAuthority): void {
  if (
    typeof authority.clientBatchId !== 'string' ||
    authority.clientBatchId.length === 0 ||
    typeof authority.sessionId !== 'string' ||
    authority.sessionId.length === 0 ||
    !Number.isSafeInteger(authority.sampleCount) ||
    authority.sampleCount < 1 ||
    authority.sampleCount > 500 ||
    !Number.isSafeInteger(authority.attemptCount) ||
    authority.attemptCount < 1 ||
    !LOCAL_UUID.test(authority.leaseOwnerId) ||
    canonicalUtcMilliseconds(authority.leaseExpiresAt) === null ||
    !isLowercaseSha256(authority.bodySha256)
  ) {
    throw new Error('UPLOAD_DISPOSITION_AUTHORITY_INVALID');
  }
}

function requireAcknowledgment(
  acknowledgment: TelemetryAcknowledgment,
  authority: UploadDispositionAuthority,
): void {
  if (
    !WIRE_UUID.test(acknowledgment.receiptId) ||
    !WIRE_UUID.test(acknowledgment.batchId) ||
    acknowledgment.clientBatchId !== authority.clientBatchId ||
    acknowledgment.sampleCount !== authority.sampleCount ||
    (acknowledgment.state !== 'stored' &&
      acknowledgment.state !== 'queued' &&
      acknowledgment.state !== 'projected') ||
    typeof acknowledgment.replay !== 'boolean'
  ) {
    throw new Error('UPLOAD_ACKNOWLEDGMENT_INVALID');
  }
}

export function createUploadDispositionCommand(input: {
  lease: UploadLeaseReference;
  disposition: UploadDisposition;
  observedAt: string;
  retryAt?: string;
}): UploadDispositionCommand {
  const { body: _body, ...authority } = input.lease;
  requireAuthority(authority);
  const observedMilliseconds = requireCanonicalUtc(
    input.observedAt,
    'UPLOAD_DISPOSITION_TIME_INVALID',
  );

  if (input.disposition.kind === 'reauthenticate') {
    throw new Error('UPLOAD_REAUTHENTICATION_POLICY_UNDEFINED');
  }
  if (input.disposition.kind === 'acknowledged') {
    if (input.retryAt !== undefined) {
      throw new Error('UPLOAD_DISPOSITION_RETRY_TIME_UNEXPECTED');
    }
    requireAcknowledgment(input.disposition.acknowledgment, authority);
    return {
      authority,
      disposition: input.disposition,
      observedAt: input.observedAt,
      retryAt: null,
    };
  }
  if (input.disposition.kind === 'hold') {
    if (!HOLD_CODES.has(input.disposition.code) || input.retryAt !== undefined) {
      throw new Error('UPLOAD_HOLD_DISPOSITION_INVALID');
    }
    return {
      authority,
      disposition: input.disposition,
      observedAt: input.observedAt,
      retryAt: null,
    };
  }
  if (
    input.disposition.kind !== 'retry' ||
    !RETRY_CODES.has(input.disposition.code) ||
    input.retryAt === undefined
  ) {
    throw new Error('UPLOAD_RETRY_DISPOSITION_INVALID');
  }
  const retryMilliseconds = requireCanonicalUtc(
    input.retryAt,
    'UPLOAD_RETRY_TIME_INVALID',
  );
  if (
    retryMilliseconds <= observedMilliseconds ||
    retryMilliseconds - observedMilliseconds > MAXIMUM_UPLOAD_RETRY_DELAY_MS
  ) {
    throw new Error('UPLOAD_RETRY_TIME_INVALID');
  }
  return {
    authority,
    disposition: input.disposition,
    observedAt: input.observedAt,
    retryAt: input.retryAt,
  };
}

function requireCommand(command: UploadDispositionCommand): void {
  requireAuthority(command.authority);
  const observedMilliseconds = requireCanonicalUtc(
    command.observedAt,
    'UPLOAD_DISPOSITION_TIME_INVALID',
  );
  if (command.disposition.kind === 'acknowledged') {
    requireAcknowledgment(command.disposition.acknowledgment, command.authority);
    if (command.retryAt !== null) {
      throw new Error('UPLOAD_DISPOSITION_RETRY_TIME_UNEXPECTED');
    }
    return;
  }
  if (command.disposition.kind === 'hold') {
    if (!HOLD_CODES.has(command.disposition.code) || command.retryAt !== null) {
      throw new Error('UPLOAD_HOLD_DISPOSITION_INVALID');
    }
    return;
  }
  if (
    command.disposition.kind !== 'retry' ||
    !RETRY_CODES.has(command.disposition.code) ||
    command.retryAt === null
  ) {
    throw new Error('UPLOAD_RETRY_DISPOSITION_INVALID');
  }
  const retryMilliseconds = requireCanonicalUtc(
    command.retryAt,
    'UPLOAD_RETRY_TIME_INVALID',
  );
  if (
    retryMilliseconds <= observedMilliseconds ||
    retryMilliseconds - observedMilliseconds > MAXIMUM_UPLOAD_RETRY_DELAY_MS
  ) {
    throw new Error('UPLOAD_RETRY_TIME_INVALID');
  }
}

function hasCompleteBinding(row: DispositionRow): boolean {
  return (
    row.item_count === row.sample_count &&
    row.minimum_position === 0 &&
    row.maximum_position === row.sample_count - 1 &&
    row.non_integer_position_count === 0
  );
}

function matchesAuthority(
  row: DispositionRow,
  authority: UploadDispositionAuthority,
): boolean {
  return (
    row.client_batch_id === authority.clientBatchId &&
    row.session_id === authority.sessionId &&
    row.sample_count === authority.sampleCount &&
    row.attempt_count_type === 'integer' &&
    row.attempt_count_text === String(authority.attemptCount) &&
    row.body_sha256 === authority.bodySha256
  );
}

function matchesLeasedPrestate(
  row: DispositionRow,
  authority: UploadDispositionAuthority,
): boolean {
  return (
    matchesAuthority(row, authority) &&
    hasCompleteBinding(row) &&
    row.state === 'leased' &&
    row.lease_owner_id === authority.leaseOwnerId &&
    row.lease_expires_at === authority.leaseExpiresAt &&
    row.next_attempt_at === null &&
    row.receipt_id === null &&
    row.server_batch_id === null &&
    row.server_state === null &&
    row.replay === null &&
    row.acknowledged_at === null &&
    row.last_error_code === null &&
    row.batched_count === authority.sampleCount &&
    row.clean_batched_count === authority.sampleCount &&
    row.acknowledged_count === 0 &&
    row.held_count === 0
  );
}

function matchesApplicableLeasedPrestate(
  row: DispositionRow,
  command: UploadDispositionCommand,
): boolean {
  const persistedUpdatedAt = canonicalUtcMilliseconds(row.updated_at);
  const observedAt = canonicalUtcMilliseconds(command.observedAt);
  return (
    matchesLeasedPrestate(row, command.authority) &&
    persistedUpdatedAt !== null &&
    observedAt !== null &&
    observedAt >= persistedUpdatedAt
  );
}

async function acknowledge(
  transaction: UploadDispositionTransaction,
  command: UploadDispositionCommand & {
    disposition: Extract<PersistableUploadDisposition, { kind: 'acknowledged' }>;
  },
): Promise<void> {
  const acknowledgment = command.disposition.acknowledgment;
  const parent = await transaction.runAsync(
    `UPDATE telemetry_upload_batch
     SET state = 'acknowledged', lease_owner_id = NULL, lease_expires_at = NULL,
         next_attempt_at = NULL, receipt_id = ?, server_batch_id = ?, server_state = ?,
         replay = ?, acknowledged_at = ?, last_error_code = NULL, updated_at = ?
     WHERE client_batch_id = ? AND session_id = ? AND state = 'leased'
       AND typeof(attempt_count) = 'integer' AND attempt_count = ?
       AND lease_owner_id = ? AND lease_expires_at = ?
       AND sample_count = ? AND body_sha256 = ?`,
    acknowledgment.receiptId,
    acknowledgment.batchId,
    acknowledgment.state,
    Number(acknowledgment.replay),
    command.observedAt,
    command.observedAt,
    command.authority.clientBatchId,
    command.authority.sessionId,
    command.authority.attemptCount,
    command.authority.leaseOwnerId,
    command.authority.leaseExpiresAt,
    command.authority.sampleCount,
    command.authority.bodySha256,
  );
  if (parent.changes !== 1) throw new Error('UPLOAD_DISPOSITION_AUTHORITY_CONFLICT');

  const children = await transaction.runAsync(
    `UPDATE outbox_delivery
     SET state = 'acknowledged', next_attempt_at = NULL,
         acknowledged_at = ?, last_error_code = NULL
     WHERE state = 'batched'
       AND event_id IN (
         SELECT event_id FROM telemetry_upload_batch_item
         WHERE client_batch_id = ? AND session_id = ?
       )`,
    command.observedAt,
    command.authority.clientBatchId,
    command.authority.sessionId,
  );
  if (children.changes !== command.authority.sampleCount) {
    throw new Error('UPLOAD_ACKNOWLEDGMENT_BINDING_INCOMPLETE');
  }
}

async function hold(
  transaction: UploadDispositionTransaction,
  command: UploadDispositionCommand & {
    disposition: Extract<PersistableUploadDisposition, { kind: 'hold' }>;
  },
): Promise<void> {
  const parent = await transaction.runAsync(
    `UPDATE telemetry_upload_batch
     SET state = 'held', lease_owner_id = NULL, lease_expires_at = NULL,
         next_attempt_at = NULL, last_error_code = ?, updated_at = ?
     WHERE client_batch_id = ? AND session_id = ? AND state = 'leased'
       AND typeof(attempt_count) = 'integer' AND attempt_count = ?
       AND lease_owner_id = ? AND lease_expires_at = ?
       AND sample_count = ? AND body_sha256 = ?`,
    command.disposition.code,
    command.observedAt,
    command.authority.clientBatchId,
    command.authority.sessionId,
    command.authority.attemptCount,
    command.authority.leaseOwnerId,
    command.authority.leaseExpiresAt,
    command.authority.sampleCount,
    command.authority.bodySha256,
  );
  if (parent.changes !== 1) throw new Error('UPLOAD_DISPOSITION_AUTHORITY_CONFLICT');

  const children = await transaction.runAsync(
    `UPDATE outbox_delivery
     SET state = 'held', next_attempt_at = NULL,
         acknowledged_at = NULL, last_error_code = ?
     WHERE state = 'batched'
       AND event_id IN (
         SELECT event_id FROM telemetry_upload_batch_item
         WHERE client_batch_id = ? AND session_id = ?
       )`,
    command.disposition.code,
    command.authority.clientBatchId,
    command.authority.sessionId,
  );
  if (children.changes !== command.authority.sampleCount) {
    throw new Error('UPLOAD_HOLD_BINDING_INCOMPLETE');
  }
}

async function retry(
  transaction: UploadDispositionTransaction,
  command: UploadDispositionCommand & {
    disposition: Extract<PersistableUploadDisposition, { kind: 'retry' }>;
    retryAt: string;
  },
): Promise<void> {
  const parent = await transaction.runAsync(
    `UPDATE telemetry_upload_batch
     SET state = 'pending', lease_owner_id = NULL, lease_expires_at = NULL,
         next_attempt_at = ?, last_error_code = ?, updated_at = ?
     WHERE client_batch_id = ? AND session_id = ? AND state = 'leased'
       AND typeof(attempt_count) = 'integer' AND attempt_count = ?
       AND lease_owner_id = ? AND lease_expires_at = ?
       AND sample_count = ? AND body_sha256 = ?`,
    command.retryAt,
    command.disposition.code,
    command.observedAt,
    command.authority.clientBatchId,
    command.authority.sessionId,
    command.authority.attemptCount,
    command.authority.leaseOwnerId,
    command.authority.leaseExpiresAt,
    command.authority.sampleCount,
    command.authority.bodySha256,
  );
  if (parent.changes !== 1) throw new Error('UPLOAD_DISPOSITION_AUTHORITY_CONFLICT');
}

export async function applyUploadDispositionCore(
  database: UploadDispositionDatabase,
  command: UploadDispositionCommand,
): Promise<void> {
  requireCommand(command);
  await database.withExclusiveTransactionAsync(async (transaction) => {
    const foreignKeys = await transaction.getFirstAsync<{ foreign_keys: number }>(
      'PRAGMA foreign_keys',
    );
    if (foreignKeys?.foreign_keys !== 1) {
      throw new Error('UPLOAD_DATABASE_FOREIGN_KEYS_DISABLED');
    }

    const row = await transaction.getFirstAsync<DispositionRow>(
      READ_DISPOSITION_ROW_SQL,
      command.authority.clientBatchId,
    );
    if (!row || !matchesApplicableLeasedPrestate(row, command)) {
      throw new Error('UPLOAD_DISPOSITION_AUTHORITY_CONFLICT');
    }

    if (command.disposition.kind === 'acknowledged') {
      await acknowledge(transaction, {
        ...command,
        disposition: command.disposition,
      });
      return;
    }
    if (command.disposition.kind === 'hold') {
      await hold(transaction, { ...command, disposition: command.disposition });
      return;
    }
    if (command.retryAt === null) {
      throw new Error('UPLOAD_RETRY_TIME_INVALID');
    }
    await retry(transaction, {
      ...command,
      disposition: command.disposition,
      retryAt: command.retryAt,
    });
  });
}

function matchesCommitted(
  row: DispositionRow,
  command: UploadDispositionCommand,
): boolean {
  if (!matchesAuthority(row, command.authority) || !hasCompleteBinding(row)) return false;

  if (command.disposition.kind === 'acknowledged') {
    const acknowledgment = command.disposition.acknowledgment;
    return (
      row.state === 'acknowledged' &&
      row.lease_owner_id === null &&
      row.lease_expires_at === null &&
      row.next_attempt_at === null &&
      row.receipt_id === acknowledgment.receiptId &&
      row.server_batch_id === acknowledgment.batchId &&
      row.server_state === acknowledgment.state &&
      row.replay === Number(acknowledgment.replay) &&
      row.acknowledged_at === command.observedAt &&
      row.updated_at === command.observedAt &&
      row.last_error_code === null &&
      row.batched_count === 0 &&
      row.acknowledged_count === command.authority.sampleCount &&
      row.clean_acknowledged_count === command.authority.sampleCount &&
      row.held_count === 0
    );
  }
  if (command.disposition.kind === 'hold') {
    return (
      row.state === 'held' &&
      row.lease_owner_id === null &&
      row.lease_expires_at === null &&
      row.next_attempt_at === null &&
      row.receipt_id === null &&
      row.server_batch_id === null &&
      row.server_state === null &&
      row.replay === null &&
      row.acknowledged_at === null &&
      row.last_error_code === command.disposition.code &&
      row.updated_at === command.observedAt &&
      row.batched_count === 0 &&
      row.acknowledged_count === 0 &&
      row.held_count === command.authority.sampleCount &&
      row.clean_held_count === command.authority.sampleCount
    );
  }
  return (
    row.state === 'pending' &&
    row.lease_owner_id === null &&
    row.lease_expires_at === null &&
    row.next_attempt_at === command.retryAt &&
    row.receipt_id === null &&
    row.server_batch_id === null &&
    row.server_state === null &&
    row.replay === null &&
    row.acknowledged_at === null &&
    row.last_error_code === command.disposition.code &&
    row.updated_at === command.observedAt &&
    row.batched_count === command.authority.sampleCount &&
    row.clean_batched_count === command.authority.sampleCount &&
    row.acknowledged_count === 0 &&
    row.held_count === 0
  );
}

export async function correlateUploadDispositionCore(
  database: UploadDispositionReadDatabase,
  command: UploadDispositionCommand,
): Promise<UploadDispositionCorrelation> {
  requireCommand(command);
  const row = await database.getFirstAsync<DispositionRow>(
    READ_DISPOSITION_ROW_SQL,
    command.authority.clientBatchId,
  );
  if (!row) return { kind: 'unverifiable' };
  if (matchesCommitted(row, command)) return { kind: 'committed' };
  if (matchesApplicableLeasedPrestate(row, command)) return { kind: 'not_committed' };
  return { kind: 'unverifiable' };
}
