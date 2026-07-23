export const TELEMETRY_BATCH_SCHEMA_VERSION = 'telemetry-batch.v2' as const;
export const MAX_TELEMETRY_BATCH_SAMPLES = 500;

export type ActivityHint =
  | 'unknown'
  | 'stationary'
  | 'walking'
  | 'wheeled'
  | 'motor_vehicle';

export type TelemetryUploadScope = {
  tenantId: string;
  deviceId: string;
  tripId: string;
  clientSessionId: string;
  installationId: string;
  consentRevisionId: string;
};

export type UploadableLocationSample = {
  clientSampleId: string;
  sequence: number;
  capturedAt: string;
  latitude: number;
  longitude: number;
  horizontalAccuracyM: number | null;
  altitudeM?: number | null;
  speedMps?: number | null;
  headingDegrees?: number | null;
  activityHint?: ActivityHint;
  isMockLocation?: boolean | null;
};

export type TelemetryBatchV2 = TelemetryUploadScope & {
  schemaVersion: typeof TELEMETRY_BATCH_SCHEMA_VERSION;
  clientBatchId: string;
  sentAt: string;
  samples: Array<{
    clientSampleId: string;
    sequence: number;
    capturedAt: string;
    latitude: number;
    longitude: number;
    horizontalAccuracyM: number | null;
    altitudeM: number | null;
    speedMps: number | null;
    headingDegrees: number | null;
    activityHint: ActivityHint;
    isMockLocation: boolean | null;
    source: 'phone_gps';
  }>;
};

export type ImmutableTelemetryBatch = {
  clientBatchId: string;
  sampleCount: number;
  body: string;
};

export type TelemetryAcknowledgment = {
  receiptId: string;
  batchId: string;
  clientBatchId: string;
  state: 'stored' | 'queued' | 'projected';
  sampleCount: number;
  replay: boolean;
};

export type UploadDisposition =
  | { kind: 'acknowledged'; acknowledgment: TelemetryAcknowledgment }
  | { kind: 'reauthenticate'; code: 'unauthenticated' }
  | {
      kind: 'retry';
      code: 'network_failure' | 'server_unavailable' | 'rate_limited' | 'invalid_acknowledgment';
    }
  | {
      kind: 'hold';
      code:
        | 'authorization_rejected'
        | 'idempotency_conflict'
        | 'client_batch_conflict'
        | 'object_conflict'
        | 'unexpected_conflict'
        | 'payload_rejected'
        | 'unexpected_client_error';
    };

export class SyncProtocolError extends Error {
  constructor(readonly code: string) {
    super(code);
    this.name = 'SyncProtocolError';
  }
}

const UUID_PATTERN =
  /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;

function isUuid(value: unknown): value is string {
  return typeof value === 'string' && UUID_PATTERN.test(value);
}

function isCanonicalUtcDateTime(value: unknown): value is string {
  if (typeof value !== 'string') return false;
  const timestamp = Date.parse(value);
  return Number.isFinite(timestamp) && new Date(timestamp).toISOString() === value;
}

function requireUuid(value: string, field: string): void {
  if (!isUuid(value)) throw new SyncProtocolError(`${field}_invalid`);
}

function requireFiniteInRange(
  value: number,
  minimum: number,
  maximum: number,
  field: string,
): void {
  if (!Number.isFinite(value) || value < minimum || value > maximum) {
    throw new SyncProtocolError(`${field}_invalid`);
  }
}

function validateSample(sample: UploadableLocationSample, index: number): void {
  const prefix = `sample_${index}`;
  requireUuid(sample.clientSampleId, `${prefix}_id`);
  if (!Number.isSafeInteger(sample.sequence) || sample.sequence < 0) {
    throw new SyncProtocolError(`${prefix}_sequence_invalid`);
  }
  if (!isCanonicalUtcDateTime(sample.capturedAt)) {
    throw new SyncProtocolError(`${prefix}_captured_at_invalid`);
  }
  requireFiniteInRange(sample.latitude, -90, 90, `${prefix}_latitude`);
  requireFiniteInRange(sample.longitude, -180, 180, `${prefix}_longitude`);
  if (
    sample.horizontalAccuracyM !== null &&
    (!Number.isFinite(sample.horizontalAccuracyM) || sample.horizontalAccuracyM < 0)
  ) {
    throw new SyncProtocolError(`${prefix}_accuracy_invalid`);
  }
  if (
    sample.altitudeM !== undefined &&
    sample.altitudeM !== null &&
    !Number.isFinite(sample.altitudeM)
  ) {
    throw new SyncProtocolError(`${prefix}_altitude_invalid`);
  }
  if (
    sample.speedMps !== undefined &&
    sample.speedMps !== null &&
    (!Number.isFinite(sample.speedMps) || sample.speedMps < 0)
  ) {
    throw new SyncProtocolError(`${prefix}_speed_invalid`);
  }
  if (
    sample.headingDegrees !== undefined &&
    sample.headingDegrees !== null &&
    (!Number.isFinite(sample.headingDegrees) ||
      sample.headingDegrees < 0 ||
      sample.headingDegrees >= 360)
  ) {
    throw new SyncProtocolError(`${prefix}_heading_invalid`);
  }
  if (
    sample.activityHint !== undefined &&
    sample.activityHint !== 'unknown' &&
    sample.activityHint !== 'stationary' &&
    sample.activityHint !== 'walking' &&
    sample.activityHint !== 'wheeled' &&
    sample.activityHint !== 'motor_vehicle'
  ) {
    throw new SyncProtocolError(`${prefix}_activity_hint_invalid`);
  }
  if (
    sample.isMockLocation !== undefined &&
    sample.isMockLocation !== null &&
    typeof sample.isMockLocation !== 'boolean'
  ) {
    throw new SyncProtocolError(`${prefix}_mock_location_invalid`);
  }
}

export function buildImmutableTelemetryBatch(input: {
  clientBatchId: string;
  sentAt: string;
  scope: TelemetryUploadScope;
  samples: readonly UploadableLocationSample[];
}): ImmutableTelemetryBatch {
  requireUuid(input.clientBatchId, 'client_batch_id');
  requireUuid(input.scope.tenantId, 'tenant_id');
  requireUuid(input.scope.deviceId, 'device_id');
  requireUuid(input.scope.tripId, 'trip_id');
  requireUuid(input.scope.clientSessionId, 'client_session_id');
  requireUuid(input.scope.installationId, 'installation_id');
  requireUuid(input.scope.consentRevisionId, 'consent_revision_id');
  if (!isCanonicalUtcDateTime(input.sentAt)) {
    throw new SyncProtocolError('sent_at_invalid');
  }
  if (input.samples.length < 1 || input.samples.length > MAX_TELEMETRY_BATCH_SAMPLES) {
    throw new SyncProtocolError('sample_count_invalid');
  }

  input.samples.forEach(validateSample);
  for (let index = 1; index < input.samples.length; index += 1) {
    if (input.samples[index].sequence <= input.samples[index - 1].sequence) {
      throw new SyncProtocolError('sample_sequence_order_invalid');
    }
  }

  const batch: TelemetryBatchV2 = {
    schemaVersion: TELEMETRY_BATCH_SCHEMA_VERSION,
    clientBatchId: input.clientBatchId,
    tenantId: input.scope.tenantId,
    deviceId: input.scope.deviceId,
    tripId: input.scope.tripId,
    clientSessionId: input.scope.clientSessionId,
    installationId: input.scope.installationId,
    consentRevisionId: input.scope.consentRevisionId,
    sentAt: input.sentAt,
    samples: input.samples.map((sample) => ({
      clientSampleId: sample.clientSampleId,
      sequence: sample.sequence,
      capturedAt: sample.capturedAt,
      latitude: sample.latitude,
      longitude: sample.longitude,
      horizontalAccuracyM: sample.horizontalAccuracyM,
      altitudeM: sample.altitudeM ?? null,
      speedMps: sample.speedMps ?? null,
      headingDegrees: sample.headingDegrees ?? null,
      activityHint: sample.activityHint ?? 'unknown',
      isMockLocation: sample.isMockLocation ?? null,
      source: 'phone_gps',
    })),
  };
  const body = JSON.stringify(batch);

  return {
    clientBatchId: input.clientBatchId,
    sampleCount: batch.samples.length,
    body,
  };
}

function readSafeErrorCode(payload: unknown): string | null {
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) return null;
  const error = (payload as Record<string, unknown>).error;
  if (!error || typeof error !== 'object' || Array.isArray(error)) return null;
  const code = (error as Record<string, unknown>).code;
  return typeof code === 'string' ? code : null;
}

function readAcknowledgment(
  payload: unknown,
  expected: { clientBatchId: string; sampleCount: number },
): TelemetryAcknowledgment | null {
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) return null;
  const candidate = payload as Record<string, unknown>;
  if (
    !isUuid(candidate.receiptId) ||
    !isUuid(candidate.batchId) ||
    candidate.clientBatchId !== expected.clientBatchId ||
    candidate.sampleCount !== expected.sampleCount ||
    (candidate.state !== 'stored' &&
      candidate.state !== 'queued' &&
      candidate.state !== 'projected') ||
    typeof candidate.replay !== 'boolean'
  ) {
    return null;
  }

  return {
    receiptId: candidate.receiptId,
    batchId: candidate.batchId,
    clientBatchId: candidate.clientBatchId,
    state: candidate.state,
    sampleCount: candidate.sampleCount,
    replay: candidate.replay,
  };
}

export function classifyUploadResponse(
  status: number,
  payload: unknown,
  expected: { clientBatchId: string; sampleCount: number },
): UploadDisposition {
  if (status === 200 || status === 202) {
    const acknowledgment = readAcknowledgment(payload, expected);
    return acknowledgment
      ? { kind: 'acknowledged', acknowledgment }
      : { kind: 'retry', code: 'invalid_acknowledgment' };
  }
  if (status === 401) return { kind: 'reauthenticate', code: 'unauthenticated' };
  if (status === 403) return { kind: 'hold', code: 'authorization_rejected' };
  if (status === 409) {
    const errorCode = readSafeErrorCode(payload);
    if (errorCode === 'idempotency_conflict') {
      return { kind: 'hold', code: 'idempotency_conflict' };
    }
    if (errorCode === 'client_batch_conflict') {
      return { kind: 'hold', code: 'client_batch_conflict' };
    }
    if (errorCode === 'object_conflict') {
      return { kind: 'hold', code: 'object_conflict' };
    }
    return { kind: 'hold', code: 'unexpected_conflict' };
  }
  if (status === 400 || status === 413 || status === 415 || status === 422) {
    return { kind: 'hold', code: 'payload_rejected' };
  }
  if (status === 408 || status >= 500) {
    return { kind: 'retry', code: 'server_unavailable' };
  }
  if (status === 429) return { kind: 'retry', code: 'rate_limited' };
  if (status >= 400 && status < 500) {
    return { kind: 'hold', code: 'unexpected_client_error' };
  }
  return { kind: 'retry', code: 'invalid_acknowledgment' };
}

export function classifyUploadFailure(): UploadDisposition {
  return { kind: 'retry', code: 'network_failure' };
}
