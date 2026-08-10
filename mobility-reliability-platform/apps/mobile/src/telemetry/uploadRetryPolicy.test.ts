import { describe, expect, it } from 'vitest';

import { createUploadRetryAt } from './uploadRetryPolicy';

const observedAt = '2026-07-23T08:00:00.000Z';

describe('upload retry policy', () => {
  it.each([
    ['network_failure', '2026-07-23T08:00:02.500Z'],
    ['server_unavailable', '2026-07-23T08:00:02.500Z'],
    ['rate_limited', '2026-07-23T08:00:15.000Z'],
    ['invalid_acknowledgment', '2026-07-23T08:00:30.000Z'],
  ] as const)('applies the bounded base delay for %s', (code, expected) => {
    expect(
      createUploadRetryAt({ code, attemptCount: 1, observedAt, randomUnit: 0 }),
    ).toBe(expected);
  });

  it('uses deterministic half-to-full jitter over exponential backoff', () => {
    expect(
      createUploadRetryAt({
        code: 'network_failure',
        attemptCount: 3,
        observedAt,
        randomUnit: 0.5,
      }),
    ).toBe('2026-07-23T08:00:15.000Z');
  });

  it('caps the retry window before applying jitter', () => {
    expect(
      createUploadRetryAt({
        code: 'invalid_acknowledgment',
        attemptCount: 1_000_000,
        observedAt,
        randomUnit: 0.999,
      }),
    ).toBe('2026-07-23T08:14:59.550Z');
  });

  it.each([
    [{ code: 'network_failure', attemptCount: 0, observedAt, randomUnit: 0 }, 'ATTEMPT'],
    [{ code: 'network_failure', attemptCount: 1, observedAt: 'invalid', randomUnit: 0 }, 'TIME'],
    [{ code: 'network_failure', attemptCount: 1, observedAt, randomUnit: 1 }, 'RANDOM'],
  ] as const)('rejects malformed policy input', (input, errorFragment) => {
    expect(() => createUploadRetryAt(input)).toThrow(errorFragment);
  });
});
