import { describe, expect, it } from 'vitest';

import {
  buildImmutableTelemetryBatch,
  classifyUploadFailure,
  classifyUploadResponse,
  MAX_TELEMETRY_BATCH_SAMPLES,
  SyncProtocolError,
  type TelemetryUploadScope,
  type UploadableLocationSample,
} from './syncProtocol';

const ids = {
  clientBatchId: '10000000-0000-4000-8000-000000000001',
  tenantId: '10000000-0000-4000-8000-000000000002',
  deviceId: '10000000-0000-4000-8000-000000000003',
  tripId: '10000000-0000-4000-8000-000000000004',
  clientSessionId: '10000000-0000-4000-8000-000000000005',
  installationId: '10000000-0000-4000-8000-000000000006',
  consentRevisionId: '10000000-0000-4000-8000-000000000007',
  clientSampleId: '10000000-0000-4000-8000-000000000008',
  receiptId: '10000000-0000-7000-8000-000000000009',
  serverBatchId: '10000000-0000-7000-8000-000000000010',
};

const scope: TelemetryUploadScope = {
  tenantId: ids.tenantId,
  deviceId: ids.deviceId,
  tripId: ids.tripId,
  clientSessionId: ids.clientSessionId,
  installationId: ids.installationId,
  consentRevisionId: ids.consentRevisionId,
};

const sample: UploadableLocationSample = {
  clientSampleId: ids.clientSampleId,
  sequence: 0,
  capturedAt: '2026-07-23T03:00:00.000Z',
  latitude: 37.5665,
  longitude: 126.978,
  horizontalAccuracyM: 8,
  altitudeM: null,
  speedMps: 1.2,
  headingDegrees: 90,
  isMockLocation: false,
};

function build(overrides: Partial<Parameters<typeof buildImmutableTelemetryBatch>[0]> = {}) {
  return buildImmutableTelemetryBatch({
    clientBatchId: ids.clientBatchId,
    sentAt: '2026-07-23T03:01:00.000Z',
    scope,
    samples: [sample],
    ...overrides,
  });
}

describe('buildImmutableTelemetryBatch', () => {
  it('creates the exact v2 wire body without mutating local samples', () => {
    const original = { ...sample };
    const result = build();

    expect(sample).toEqual(original);
    expect(result.sampleCount).toBe(1);
    const parsedBody = JSON.parse(result.body);
    expect(parsedBody).toEqual({
      schemaVersion: 'telemetry-batch.v2',
      clientBatchId: ids.clientBatchId,
      ...scope,
      sentAt: '2026-07-23T03:01:00.000Z',
      samples: [
        {
          ...sample,
          activityHint: 'unknown',
          source: 'phone_gps',
        },
      ],
    });
  });

  it('returns canonical bytes regardless of input key order and extra runtime fields', () => {
    const reorderedAndTainted = {
      longitude: sample.longitude,
      latitude: sample.latitude,
      sequence: sample.sequence,
      clientSampleId: sample.clientSampleId,
      capturedAt: sample.capturedAt,
      horizontalAccuracyM: sample.horizontalAccuracyM,
      altitudeM: sample.altitudeM,
      speedMps: sample.speedMps,
      headingDegrees: sample.headingDegrees,
      isMockLocation: sample.isMockLocation,
      unexpected: 'must-not-reach-wire',
    } as UploadableLocationSample;

    expect(build({ samples: [reorderedAndTainted] }).body).toBe(build().body);
    expect(build({ samples: [reorderedAndTainted] }).body).not.toContain('unexpected');
  });

  it('accepts the batch-size boundary', () => {
    const samples = Array.from({ length: MAX_TELEMETRY_BATCH_SAMPLES }, (_, sequence) => ({
      ...sample,
      clientSampleId: `10000000-0000-4000-8000-${String(sequence).padStart(12, '0')}`,
      sequence,
    }));
    expect(build({ samples }).sampleCount).toBe(MAX_TELEMETRY_BATCH_SAMPLES);
  });

  it('rejects empty, oversized, and out-of-order batches with metadata-only codes', () => {
    expect(() => build({ samples: [] })).toThrowError(
      new SyncProtocolError('sample_count_invalid'),
    );
    expect(() =>
      build({
        samples: Array.from({ length: MAX_TELEMETRY_BATCH_SAMPLES + 1 }, (_, sequence) => ({
          ...sample,
          clientSampleId: `10000000-0000-4000-8000-${String(sequence).padStart(12, '0')}`,
          sequence,
        })),
      }),
    ).toThrowError(new SyncProtocolError('sample_count_invalid'));
    expect(() =>
      build({
        samples: [sample, { ...sample, clientSampleId: ids.clientBatchId, sequence: 0 }],
      }),
    ).toThrowError(new SyncProtocolError('sample_sequence_order_invalid'));
  });

  it('rejects non-finite sensor values before JSON serialization', () => {
    expect(() => build({ samples: [{ ...sample, latitude: Number.NaN }] })).toThrowError(
      new SyncProtocolError('sample_0_latitude_invalid'),
    );
    expect(() => build({ samples: [{ ...sample, speedMps: -1 }] })).toThrowError(
      new SyncProtocolError('sample_0_speed_invalid'),
    );
    expect(() =>
      build({
        samples: [
          { ...sample, activityHint: 'corrupted' as UploadableLocationSample['activityHint'] },
        ],
      }),
    ).toThrowError(new SyncProtocolError('sample_0_activity_hint_invalid'));
  });
});

describe('classifyUploadResponse', () => {
  const expected = { clientBatchId: ids.clientBatchId, sampleCount: 1 };
  const acknowledgment = {
    receiptId: ids.receiptId,
    batchId: ids.serverBatchId,
    clientBatchId: ids.clientBatchId,
    state: 'stored',
    sampleCount: 1,
    replay: false,
  };

  it.each([200, 202])('acknowledges a matching receipt for HTTP %s', (status) => {
    expect(classifyUploadResponse(status, acknowledgment, expected)).toEqual({
      kind: 'acknowledged',
      acknowledgment,
    });
  });

  it('retries the same body when a success response cannot be proven', () => {
    expect(
      classifyUploadResponse(202, { ...acknowledgment, clientBatchId: ids.tripId }, expected),
    ).toEqual({ kind: 'retry', code: 'invalid_acknowledgment' });
    expect(
      classifyUploadResponse(202, { ...acknowledgment, sampleCount: 2 }, expected),
    ).toEqual({ kind: 'retry', code: 'invalid_acknowledgment' });
  });

  it.each([
    [401, { kind: 'reauthenticate', code: 'unauthenticated' }],
    [403, { kind: 'hold', code: 'authorization_rejected' }],
    [
      409,
      { kind: 'hold', code: 'unexpected_conflict' },
    ],
    [422, { kind: 'hold', code: 'payload_rejected' }],
    [429, { kind: 'retry', code: 'rate_limited' }],
    [503, { kind: 'retry', code: 'server_unavailable' }],
  ])('classifies HTTP %s without inspecting sensitive error text', (status, disposition) => {
    expect(classifyUploadResponse(status as number, { error: 'ignored' }, expected)).toEqual(
      disposition,
    );
  });

  it.each([
    ['idempotency_conflict', 'idempotency_conflict'],
    ['client_batch_conflict', 'client_batch_conflict'],
    ['object_conflict', 'object_conflict'],
  ])('preserves the bounded server conflict code %s', (serverCode, expectedCode) => {
    expect(
      classifyUploadResponse(
        409,
        { error: { code: serverCode, ignoredDetail: 'never-read' } },
        expected,
      ),
    ).toEqual({ kind: 'hold', code: expectedCode });
  });

  it('classifies transport failures as retryable', () => {
    expect(classifyUploadFailure()).toEqual({ kind: 'retry', code: 'network_failure' });
  });
});
