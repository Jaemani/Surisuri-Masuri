import assert from 'node:assert/strict';
import test from 'node:test';

import { mapLegacyDevice, mapLegacyRepair, mapLegacyUser, normalizeRepairCategories, reconcileImport } from '../src/index.js';

const context = { runId: 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', sourceSystem: 'mongo_legacy' };

test('normalizes known repair category aliases without inventing unknown categories', () => {
  assert.deepEqual(normalizeRepairCategories(['타이어 | 튜브', '타이어', '정체불명']), {
    values: ['tire', 'tube'], unknown: ['정체불명'],
  });
});

test('maps a complete user while preserving legacy SMS consent as non-authoritative', () => {
  const result = mapLegacyUser(context, {
    _id: 'user-1', name: '홍길동', phoneNumber: '01000000000', recipientType: '수급자', smsConsent: true,
  });
  assert.equal(result.disposition, 'accepted');
  assert.equal(result.normalized.recipientType, 'public_assistance_recipient');
  assert.equal(result.normalized.smsConsentLegacy, true);
});

test('quarantines a device whose owner cannot be resolved', () => {
  const result = mapLegacyDevice(context, { _id: 'device-1', vehicleId: 'QR-1', userId: 'missing-user' }, new Map());
  assert.equal(result.disposition, 'quarantined');
  assert.deepEqual(result.quarantineReasonCodes, ['missing_reference']);
});

test('legacy billing price never creates a subsidy execution', () => {
  const result = mapLegacyRepair(context, {
    _id: 'repair-1', vehicleId: 'legacy-device-1', repairedAt: '2025-01-02T00:00:00Z',
    repairCategories: ['배터리'], billingPrice: 120000,
  }, new Map([['legacy-device-1', 'new-device-1']]));
  assert.equal(result.disposition, 'quarantined');
  assert.deepEqual(result.quarantineReasonCodes, ['unverified_amount']);
  assert.deepEqual(result.normalized.costObservation, {
    billedAmountKrw: 120000,
    verificationStatus: 'unverified_legacy',
    subsidyTransactionCreated: false,
  });
});

test('invalid dates and unknown categories remain reviewable rather than receiving defaults', () => {
  const result = mapLegacyRepair(context, {
    _id: 'repair-2', vehicleId: 'legacy-device-1', repairedAt: 'not-a-date', repairCategories: ['알 수 없음'],
  }, new Map([['legacy-device-1', 'new-device-1']]));
  assert.deepEqual(result.quarantineReasonCodes, ['invalid_date', 'unknown_enum']);
  assert.equal(result.normalized.repairedAt, null);
  assert.deepEqual(result.normalized.unknownCategoryLabels, ['알 수 없음']);
});

test('reconciliation counts every disposition and produces a stable manifest hash', () => {
  const records = [
    mapLegacyUser(context, { _id: 'u1', name: 'A', phoneNumber: '1', recipientType: 'general' }),
    mapLegacyDevice(context, { _id: 'd1', vehicleId: 'Q1', userId: 'missing' }, new Map()),
  ];
  const first = reconcileImport(records);
  const second = reconcileImport(records);
  assert.deepEqual(first.counts, { total: 2, accepted: 1, quarantined: 1, rejected: 0 });
  assert.equal(first.manifestHash, second.manifestHash);
});
