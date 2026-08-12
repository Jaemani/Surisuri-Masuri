import { describe, expect, it } from 'vitest';
import { normalizeDeviceCode, parseDeviceQrValue } from './deviceCode';

describe('device public code parser', () => {
  it('normalizes manual codes and accepts the bounded device deep link', () => {
    expect(normalizeDeviceCode(' mr-2208 ')).toBe('MR-2208');
    expect(parseDeviceQrValue('surisuri://device/mr-2208')).toBe('MR-2208');
    expect(parseDeviceQrValue('MOB_1042')).toBe('MOB_1042');
  });

  it('rejects URLs, scripts, missing codes, and overlong payloads', () => {
    expect(parseDeviceQrValue('https://example.com/device/MR-2208')).toBeNull();
    expect(parseDeviceQrValue('javascript:alert(1)')).toBeNull();
    expect(parseDeviceQrValue('surisuri://device/')).toBeNull();
    expect(parseDeviceQrValue('X'.repeat(65))).toBeNull();
  });
});

