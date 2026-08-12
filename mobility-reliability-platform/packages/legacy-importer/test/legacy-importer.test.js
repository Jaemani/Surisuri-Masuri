import assert from 'node:assert/strict';
import test from 'node:test';

import { buildLegacyRepairEventDryRun, mapLegacyDevice, mapLegacyRepair, mapLegacyUser, normalizeRepairCategories, reconcileImport } from '../src/index.js';

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

test('builds deterministic privacy-minimised repair events without applying writes', () => {
  const sourceDeviceId = 'legacy-device-1';
  const targetDeviceId = 'cd230e0f-a01c-47e5-830d-3a1330e591d6';
  const record = mapLegacyRepair(context, { _id: 'repair-accepted', vehicleId: sourceDeviceId, repairedAt: '2025-01-02T00:00:00Z', repairCategories: ['배터리'] }, new Map([[sourceDeviceId, sourceDeviceId]]));
  const input = { manifestId: '0cb33fa1-07d9-4c6d-9a15-bd6b7b26e7d4', importRunId: '3b5ddcc5-f65f-48e5-a3b3-f8e142fb5d79', tenantId: '09460fa5-dc3c-4c14-9c00-d1956206292c', records: [record], deviceCrosswalk: new Map([[sourceDeviceId, targetDeviceId]]), repairCrosswalk: new Map([['repair-accepted', '0a7299a5-3f18-4f47-b0c8-6f1d9ef5ee60']]), verifiedSourceIds: new Set(['repair-accepted']), recordedAtBySource: new Map([['repair-accepted', '2025-01-03T00:00:00Z']]) };
  const first = buildLegacyRepairEventDryRun(input);
  const second = buildLegacyRepairEventDryRun(input);

  assert.deepEqual(first, second);
  assert.deepEqual(first.result.counts, { accepted: 1, quarantined: 0 });
  assert.deepEqual({ dryRun: first.result.dryRun, writeApplied: first.result.writeApplied, deploymentApplied: first.result.deploymentApplied }, { dryRun: true, writeApplied: false, deploymentApplied: false });
  assert.deepEqual(Object.keys(first.result.generatedEvents[0]).sort(), ['eventHash', 'eventId']);
  assert.deepEqual(first.events[0], {
    schemaVersion: 'device-state-event.v1', eventId: first.result.generatedEvents[0].eventId, eventType: 'repair.recorded', tenantId: input.tenantId, deviceId: targetDeviceId,
    occurredAt: '2025-01-02T00:00:00.000Z', recordedAt: '2025-01-03T00:00:00.000Z', sourceQuality: 'verified', payload: { repairId: '0a7299a5-3f18-4f47-b0c8-6f1d9ef5ee60' },
  });
  assert.equal(JSON.stringify(first.result).includes('repair-accepted'), false);
});

test('quarantines missing crosswalk, invalid date, unknown category, amount, and conflicting duplicate', () => {
  const deviceMap = new Map([['legacy-device-1', 'legacy-device-1']]);
  const records = [
    mapLegacyRepair(context, { _id: 'missing-device', vehicleId: 'unknown', repairedAt: '2025-01-02T00:00:00Z', repairCategories: ['배터리'] }, deviceMap),
    mapLegacyRepair(context, { _id: 'invalid-shape', vehicleId: 'legacy-device-1', repairedAt: 'bad', repairCategories: ['정체불명'], billingPrice: 1000 }, deviceMap),
    mapLegacyRepair(context, { _id: 'duplicate', vehicleId: 'legacy-device-1', repairedAt: '2025-01-02T00:00:00Z', repairCategories: ['배터리'] }, deviceMap),
    mapLegacyRepair(context, { _id: 'duplicate', vehicleId: 'legacy-device-1', repairedAt: '2025-02-02T00:00:00Z', repairCategories: ['브레이크'] }, deviceMap),
  ];
  const sourceIds = records.map((record) => record.sourceId);
  const output = buildLegacyRepairEventDryRun({ manifestId: '0cb33fa1-07d9-4c6d-9a15-bd6b7b26e7d4', importRunId: '3b5ddcc5-f65f-48e5-a3b3-f8e142fb5d79', tenantId: '09460fa5-dc3c-4c14-9c00-d1956206292c', records, deviceCrosswalk: new Map([['legacy-device-1', 'cd230e0f-a01c-47e5-830d-3a1330e591d6']]), repairCrosswalk: new Map(sourceIds.map((id, index) => [id, `0a7299a5-3f18-4f47-b0c8-6f1d9ef5ee6${index}`])), verifiedSourceIds: new Set(sourceIds), recordedAtBySource: new Map(sourceIds.map((id) => [id, '2025-03-01T00:00:00Z'])) });
  assert.deepEqual(output.result.counts, { accepted: 1, quarantined: 3 });
  assert.equal(output.result.quarantineReasonCounts.conflicting_duplicate, 1);
  assert.equal(output.result.quarantineReasonCounts.invalid_date, 1);
  assert.equal(output.result.quarantineReasonCounts.unknown_enum, 1);
  assert.equal(output.result.quarantineReasonCounts.unverified_amount, 1);
  assert.equal(JSON.stringify(output.result).includes('정체불명'), false);
});

test('never upgrades mapper acceptance to verified without explicit evidence and timestamps', () => {
  const sourceDeviceId = 'legacy-device-1';
  const record = mapLegacyRepair(context, { _id: 'repair-unverified', vehicleId: sourceDeviceId, repairedAt: '2025-01-02T00:00:00Z', repairCategories: ['배터리'] }, new Map([[sourceDeviceId, sourceDeviceId]]));
  const output = buildLegacyRepairEventDryRun({ manifestId: '0cb33fa1-07d9-4c6d-9a15-bd6b7b26e7d4', importRunId: '3b5ddcc5-f65f-48e5-a3b3-f8e142fb5d79', tenantId: '09460fa5-dc3c-4c14-9c00-d1956206292c', records: [record], deviceCrosswalk: new Map([[sourceDeviceId, 'cd230e0f-a01c-47e5-830d-3a1330e591d6']]), repairCrosswalk: new Map([['repair-unverified', '0a7299a5-3f18-4f47-b0c8-6f1d9ef5ee60']]), verifiedSourceIds: new Set(), recordedAtBySource: new Map() });
  assert.deepEqual(output.result.counts, { accepted: 0, quarantined: 1 });
  assert.deepEqual(output.result.quarantineReasonCounts, { missing_recorded_at: 1, unverified_source: 1 });
  assert.deepEqual(output.events, []);
});
