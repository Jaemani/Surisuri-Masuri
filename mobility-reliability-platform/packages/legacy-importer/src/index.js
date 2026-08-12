import { createHash } from 'node:crypto';

const recipientAliases = Object.freeze({
  general: 'general',
  일반: 'general',
  recipient: 'public_assistance_recipient',
  수급: 'public_assistance_recipient',
  수급자: 'public_assistance_recipient',
  lowIncome: 'near_poor',
  차상위: 'near_poor',
});

const categoryAliases = Object.freeze({
  타이어: ['tire'],
  튜브: ['tube'],
  '타이어 | 튜브': ['tire', 'tube'],
  '타이어&튜브': ['tire', 'tube'],
  배터리: ['battery'],
  브레이크: ['brake'],
  제동장치: ['brake'],
  모터: ['motor'],
});

export function canonicalHash(value) {
  return createHash('sha256').update(JSON.stringify(sortObject(value))).digest('hex');
}

function sortObject(value) {
  if (Array.isArray(value)) return value.map(sortObject);
  if (!value || typeof value !== 'object') return value;
  return Object.fromEntries(Object.entries(value).sort(([a], [b]) => a.localeCompare(b)).map(([key, item]) => [key, sortObject(item)]));
}

function presentString(value) {
  return typeof value === 'string' && value.trim().length > 0 ? value.trim() : null;
}

function normalizeDate(value) {
  if (value == null || value === '') return null;
  const date = value instanceof Date ? value : new Date(value);
  return Number.isNaN(date.getTime()) ? null : date.toISOString();
}

export function normalizeRecipientType(value) {
  return recipientAliases[value] ?? null;
}

export function normalizeRepairCategories(values) {
  if (!Array.isArray(values)) return { values: [], unknown: [] };
  const normalized = new Set();
  const unknown = [];
  for (const raw of values) {
    const value = presentString(raw);
    if (!value) continue;
    const mapped = categoryAliases[value];
    if (!mapped) unknown.push(value);
    else mapped.forEach((item) => normalized.add(item));
  }
  return { values: [...normalized].sort(), unknown: [...new Set(unknown)].sort() };
}

function baseRecord({ runId, sourceSystem, sourceCollection, sourceId, raw, targetEntityType, mappingVersion }) {
  return {
    schemaVersion: 'legacy-import-record.v1',
    runId,
    sourceSystem,
    sourceCollection,
    sourceId,
    sourceSnapshotHash: canonicalHash(raw),
    targetEntityType,
    targetId: null,
    mappingVersion,
    reviewedBy: null,
    reviewedAt: null,
  };
}

function disposition(record, normalized, reasons) {
  const uniqueReasons = [...new Set(reasons)].sort();
  if (uniqueReasons.length > 0) {
    return { ...record, disposition: 'quarantined', quarantineReasonCodes: uniqueReasons, normalizedHash: normalized ? canonicalHash(normalized) : null, normalized };
  }
  return { ...record, disposition: 'accepted', quarantineReasonCodes: [], normalizedHash: canonicalHash(normalized), normalized };
}

export function mapLegacyUser(context, raw) {
  const reasons = [];
  const sourceId = presentString(raw._id ?? raw.id);
  const name = presentString(raw.name);
  const phoneNumber = presentString(raw.phoneNumber);
  const recipientType = normalizeRecipientType(raw.recipientType);
  if (!sourceId) reasons.push('ambiguous_id');
  if (!name || !phoneNumber) reasons.push('pii_review_required');
  if (raw.recipientType != null && !recipientType) reasons.push('unknown_enum');

  const normalized = {
    legacySourceId: sourceId,
    firebaseUid: presentString(raw.firebaseUid),
    displayCode: sourceId ? `LEGACY-${sourceId.slice(-6).toUpperCase()}` : null,
    recipientType,
    supportedDistrict: presentString(raw.supportedDistrict),
    pii: { name, phoneNumber },
    smsConsentLegacy: typeof raw.smsConsent === 'boolean' ? raw.smsConsent : null,
    createdAt: normalizeDate(raw.createdAt),
  };
  return disposition(baseRecord({ ...context, sourceCollection: 'users', sourceId: sourceId ?? 'missing', raw, targetEntityType: 'person', mappingVersion: 'user-map.v1' }), normalized, reasons);
}

export function mapLegacyDevice(context, raw, userCrosswalk) {
  const reasons = [];
  const sourceId = presentString(raw._id ?? raw.id);
  const publicCode = presentString(raw.vehicleId);
  const legacyUserId = presentString(raw.userId);
  const personId = legacyUserId ? userCrosswalk.get(legacyUserId) ?? null : null;
  const purchasedAt = normalizeDate(raw.purchasedAt);
  const manufacturedAt = normalizeDate(raw.manufacturedAt ?? raw.registeredAt);
  if (!sourceId || !publicCode) reasons.push('ambiguous_id');
  if (!legacyUserId || !personId) reasons.push('missing_reference');
  if (raw.purchasedAt != null && !purchasedAt) reasons.push('invalid_date');
  if ((raw.manufacturedAt ?? raw.registeredAt) != null && !manufacturedAt) reasons.push('invalid_date');

  const normalized = {
    legacySourceId: sourceId,
    publicCode,
    model: presentString(raw.model),
    vehicleType: presentString(raw.vehicleType),
    purchasedAt,
    manufacturedAt,
    initialAssignment: personId ? { personId, source: 'legacy_current_owner' } : null,
  };
  return disposition(baseRecord({ ...context, sourceCollection: 'vehicles', sourceId: sourceId ?? 'missing', raw, targetEntityType: 'device', mappingVersion: 'device-map.v1' }), normalized, reasons);
}

export function mapLegacyRepair(context, raw, deviceCrosswalk) {
  const reasons = [];
  const sourceId = presentString(raw._id ?? raw.id);
  const legacyDeviceId = presentString(raw.vehicleId);
  const deviceId = legacyDeviceId ? deviceCrosswalk.get(legacyDeviceId) ?? null : null;
  const repairedAt = normalizeDate(raw.repairedAt);
  const categories = normalizeRepairCategories(raw.repairCategories);
  const billingPrice = Number.isFinite(raw.billingPrice) && raw.billingPrice >= 0 ? Math.round(raw.billingPrice) : null;
  if (!sourceId) reasons.push('ambiguous_id');
  if (!legacyDeviceId || !deviceId) reasons.push('missing_reference');
  if (!repairedAt) reasons.push('invalid_date');
  if (categories.unknown.length > 0) reasons.push('unknown_enum');
  if (billingPrice != null) reasons.push('unverified_amount');

  const normalized = {
    legacySourceId: sourceId,
    deviceId,
    repairedAt,
    repairCategoryCodes: categories.values,
    unknownCategoryLabels: categories.unknown,
    repairStationCode: presentString(raw.repairStationCode),
    repairStationLabelSnapshot: presentString(raw.repairStationLabel),
    repairerLabelSnapshot: presentString(raw.repairer),
    batteryVoltage: Number.isFinite(raw.batteryVoltage) && raw.batteryVoltage > 0 ? raw.batteryVoltage : null,
    partsMemo: presentString(raw.etcRepairParts),
    memo: presentString(raw.memo),
    costObservation: billingPrice == null ? null : {
      billedAmountKrw: billingPrice,
      verificationStatus: 'unverified_legacy',
      subsidyTransactionCreated: false,
    },
  };
  return disposition(baseRecord({ ...context, sourceCollection: 'repairs', sourceId: sourceId ?? 'missing', raw, targetEntityType: 'repair_event', mappingVersion: 'repair-map.v1' }), normalized, reasons);
}

export function reconcileImport(records) {
  const counts = { total: records.length, accepted: 0, quarantined: 0, rejected: 0 };
  const byCollection = {};
  const reasons = {};
  for (const record of records) {
    counts[record.disposition] += 1;
    byCollection[record.sourceCollection] ??= { total: 0, accepted: 0, quarantined: 0, rejected: 0 };
    byCollection[record.sourceCollection].total += 1;
    byCollection[record.sourceCollection][record.disposition] += 1;
    for (const reason of record.quarantineReasonCodes ?? []) reasons[reason] = (reasons[reason] ?? 0) + 1;
  }
  return { counts, byCollection, reasons, manifestHash: canonicalHash(records.map(({ normalized, ...record }) => record)) };
}

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

function deterministicUuid(namespace, value) {
  const hex = createHash('sha256').update(`${namespace}\u001f${value}`).digest('hex');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-5${hex.slice(13, 16)}-a${hex.slice(17, 20)}-${hex.slice(20, 32)}`;
}

/**
 * Converts legacy repair mapping records into privacy-minimised, verified
 * device-state event candidates. This function is intentionally dry-run only:
 * it returns values and reconciliation hashes but performs no database writes.
 */
export function buildLegacyRepairEventDryRun({ manifestId, importRunId, tenantId, records, deviceCrosswalk, repairCrosswalk, verifiedSourceIds, recordedAtBySource }) {
  if (![manifestId, importRunId, tenantId].every((value) => typeof value === 'string' && uuidPattern.test(value))) throw new Error('DRY_RUN_ID_INVALID');
  if (!Array.isArray(records) || !(deviceCrosswalk instanceof Map) || !(repairCrosswalk instanceof Map) || !(verifiedSourceIds instanceof Set) || !(recordedAtBySource instanceof Map)) throw new Error('DRY_RUN_INPUT_INVALID');
  const crosswalkEntries = [...deviceCrosswalk.entries()].map(([source, target]) => ['device', source, target]).concat([...repairCrosswalk.entries()].map(([source, target]) => ['repair', source, target])).sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  const reasons = {};
  const generated = [];
  const seenSource = new Map();
  let quarantined = 0;

  for (const record of records) {
    const sourceId = presentString(record?.sourceId);
    const normalized = record?.normalized;
    const reasonSet = new Set(record?.quarantineReasonCodes ?? []);
    if (!sourceId) reasonSet.add('ambiguous_id');
    if (record?.targetEntityType !== 'repair_event' || !normalized || typeof normalized !== 'object') reasonSet.add('missing_reference');
    const targetDeviceId = normalized?.deviceId ? deviceCrosswalk.get(normalized.deviceId) ?? normalized.deviceId : null;
    const repairId = sourceId ? repairCrosswalk.get(sourceId) ?? null : null;
    const recordedAt = sourceId ? recordedAtBySource.get(sourceId) ?? null : null;
    if (!targetDeviceId || !uuidPattern.test(targetDeviceId)) reasonSet.add('missing_reference');
    if (!repairId || !uuidPattern.test(repairId)) reasonSet.add('missing_reference');
    if (!sourceId || !verifiedSourceIds.has(sourceId)) reasonSet.add('unverified_source');
    if (!normalized?.repairedAt || Number.isNaN(Date.parse(normalized.repairedAt))) reasonSet.add('invalid_date');
    if (!recordedAt || Number.isNaN(Date.parse(recordedAt)) || (normalized?.repairedAt && Date.parse(recordedAt) < Date.parse(normalized.repairedAt))) reasonSet.add('missing_recorded_at');
    if (Array.isArray(normalized?.unknownCategoryLabels) && normalized.unknownCategoryLabels.length) reasonSet.add('unknown_enum');
    if (normalized?.costObservation) reasonSet.add('unverified_amount');
    if (sourceId) {
      const fingerprint = canonicalHash({ deviceId: targetDeviceId, repairedAt: normalized?.repairedAt, categories: normalized?.repairCategoryCodes });
      const previous = seenSource.get(sourceId);
      if (previous && previous !== fingerprint) reasonSet.add('conflicting_duplicate');
      else seenSource.set(sourceId, fingerprint);
    }

    if (reasonSet.size) {
      quarantined += 1;
      for (const reason of reasonSet) reasons[reason] = (reasons[reason] ?? 0) + 1;
      continue;
    }
    const eventId = deterministicUuid('legacy-repair-event.v1', `${manifestId}\u001f${sourceId}`);
    const event = {
      schemaVersion: 'device-state-event.v1', eventId, eventType: 'repair.recorded', tenantId, deviceId: targetDeviceId,
      occurredAt: new Date(normalized.repairedAt).toISOString(), recordedAt: new Date(recordedAt).toISOString(),
      sourceQuality: 'verified', payload: { repairId },
    };
    generated.push({ eventId, eventHash: canonicalHash(event), event });
  }

  generated.sort((left, right) => left.eventId.localeCompare(right.eventId));
  const sourceHash = canonicalHash(records.map((record) => ({ sourceId: record?.sourceId, sourceSnapshotHash: record?.sourceSnapshotHash, normalizedHash: record?.normalizedHash, disposition: record?.disposition })));
  const crosswalkHash = canonicalHash(crosswalkEntries);
  const outputHash = canonicalHash(generated.map(({ event }) => event));
  return {
    result: {
      schemaVersion: 'legacy-device-event-dry-run.v1', manifestId, importRunId, sourceHash, crosswalkHash, outputHash,
      dryRun: true, writeApplied: false, deploymentApplied: false,
      counts: { accepted: generated.length, quarantined }, quarantineReasonCounts: reasons,
      generatedEvents: generated.map(({ eventId, eventHash }) => ({ eventId, eventHash })),
    },
    events: generated.map(({ event }) => event),
  };
}
