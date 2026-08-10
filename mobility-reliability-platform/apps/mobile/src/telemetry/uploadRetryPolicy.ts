import type { UploadDisposition } from './syncProtocol';

type RetryDisposition = Extract<UploadDisposition, { kind: 'retry' }>;

export const MAXIMUM_UPLOAD_RETRY_DELAY_MS = 15 * 60 * 1_000;

const BASE_DELAY_MS: Record<RetryDisposition['code'], number> = {
  network_failure: 5_000,
  server_unavailable: 5_000,
  rate_limited: 30_000,
  invalid_acknowledgment: 60_000,
};

function canonicalUtcMilliseconds(value: string): number | null {
  const milliseconds = Date.parse(value);
  return Number.isFinite(milliseconds) && new Date(milliseconds).toISOString() === value
    ? milliseconds
    : null;
}

export function createUploadRetryAt(input: {
  code: RetryDisposition['code'];
  attemptCount: number;
  observedAt: string;
  randomUnit: number;
}): string {
  const observedMilliseconds = canonicalUtcMilliseconds(input.observedAt);
  if (observedMilliseconds === null) {
    throw new Error('UPLOAD_RETRY_POLICY_TIME_INVALID');
  }
  if (!Number.isSafeInteger(input.attemptCount) || input.attemptCount < 1) {
    throw new Error('UPLOAD_RETRY_POLICY_ATTEMPT_INVALID');
  }
  if (!Number.isFinite(input.randomUnit) || input.randomUnit < 0 || input.randomUnit >= 1) {
    throw new Error('UPLOAD_RETRY_POLICY_RANDOM_INVALID');
  }

  const baseDelay = BASE_DELAY_MS[input.code];
  if (baseDelay === undefined) {
    throw new Error('UPLOAD_RETRY_POLICY_CODE_INVALID');
  }
  const exponent = Math.min(input.attemptCount - 1, 20);
  const boundedExponentialDelay = Math.min(
    baseDelay * 2 ** exponent,
    MAXIMUM_UPLOAD_RETRY_DELAY_MS,
  );
  const jitteredDelay = Math.max(
    1,
    Math.floor(boundedExponentialDelay * (0.5 + input.randomUnit / 2)),
  );
  const retryMilliseconds = observedMilliseconds + jitteredDelay;
  const retryDate = new Date(retryMilliseconds);
  if (!Number.isFinite(retryDate.getTime())) {
    throw new Error('UPLOAD_RETRY_POLICY_TIME_INVALID');
  }
  return retryDate.toISOString();
}
