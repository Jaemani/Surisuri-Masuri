import { describe, expect, it } from 'vitest';

import { CaptureGuard } from './captureGuard';

describe('CaptureGuard', () => {
  it('rejects a second operation until the first one ends', () => {
    const guard = new CaptureGuard();
    expect(guard.tryBeginOperation()).toBe(true);
    expect(guard.tryBeginOperation()).toBe(false);
    guard.endOperation();
    expect(guard.tryBeginOperation()).toBe(true);
  });

  it('rejects callbacks after capture closes', () => {
    const guard = new CaptureGuard();
    const generation = guard.openCapture();
    expect(guard.acceptsCallback(generation)).toBe(true);
    guard.closeCapture();
    expect(guard.acceptsCallback(generation)).toBe(false);
  });

  it('rejects callbacks from an older watcher generation', () => {
    const guard = new CaptureGuard();
    const oldGeneration = guard.openCapture();
    guard.closeCapture();
    const currentGeneration = guard.openCapture();

    expect(guard.acceptsCallback(oldGeneration)).toBe(false);
    expect(guard.acceptsCallback(currentGeneration)).toBe(true);
  });
});
