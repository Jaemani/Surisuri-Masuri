import { createHash } from 'node:crypto'

const allowedFactTypes = new Set(['component_readiness', 'fallback_policy', 'scope_boundary'])
const allowedClaimTypes = new Set(['readiness_summary', 'fallback_summary', 'boundary_summary'])
const forbiddenKeys = new Set(['personId', 'person_id', 'deviceId', 'device_id', 'tenantId', 'tenant_id', 'firebaseUid', 'firebase_uid', 'actorUid', 'actor_uid', 'latitude', 'longitude', 'coordinates', 'rawPath', 'raw_path', 'objectPath', 'object_path', 'repairMemo', 'repair_memo', 'sourceRef', 'source_ref'])

export class ReportEvidenceError extends Error {
  constructor(code) { super(code); this.code = code }
}

const stable = (value) => Array.isArray(value)
  ? value.map(stable)
  : value && typeof value === 'object'
    ? Object.fromEntries(Object.entries(value).sort(([left], [right]) => left.localeCompare(right)).map(([key, child]) => [key, stable(child)]))
    : value
const canonical = (value) => JSON.stringify(stable(value))
const sha256 = (value) => createHash('sha256').update(value).digest('hex')

function containsForbiddenKey(value) {
  if (Array.isArray(value)) return value.some(containsForbiddenKey)
  if (!value || typeof value !== 'object') return false
  return Object.keys(value).some((key) => forbiddenKeys.has(key)) || Object.values(value).some(containsForbiddenKey)
}

function exactKeys(value, expected, code) {
  if (!value || typeof value !== 'object' || Array.isArray(value) || Object.keys(value).sort().join('|') !== [...expected].sort().join('|')) throw new ReportEvidenceError(code)
}

function factText(fact) {
  if (fact.factType === 'component_readiness') return `${fact.value.component} 보정 가능성은 ${fact.value.status}입니다. validation ${fact.value.validationCount}건, 사건 ${fact.value.validationEventCount}건입니다.`
  if (fact.factType === 'fallback_policy') return '판단 유보 시 고정 점검 일정을 유지하고 담당자가 검토합니다.'
  return '이 결과는 합성 aggregate 평가이며 실제 기기 위험도나 현장 성능을 의미하지 않습니다.'
}

function validCount(value) { return Number.isSafeInteger(value) && value >= 0 && value <= 1_000_000 }
function validateFactValue(fact) {
  if (fact.factType === 'component_readiness') {
    exactKeys(fact.value, ['component', 'status', 'validationCount', 'validationEventCount'], 'FACT_VALUE_KEYS_INVALID')
    if (!['battery', 'brake', 'controller'].includes(fact.value.component) || fact.value.status !== 'not_estimable' || !validCount(fact.value.validationCount) || !validCount(fact.value.validationEventCount) || fact.value.validationEventCount > fact.value.validationCount) throw new ReportEvidenceError('FACT_VALUE_INVALID')
  } else if (fact.factType === 'fallback_policy') {
    exactKeys(fact.value, ['label'], 'FACT_VALUE_KEYS_INVALID')
    if (fact.value.label !== '고정 점검 일정과 담당자 검토') throw new ReportEvidenceError('FACT_VALUE_INVALID')
  } else {
    exactKeys(fact.value, ['evaluationScope', 'fieldPerformance', 'individualActionAllowed'], 'FACT_VALUE_KEYS_INVALID')
    if (fact.value.evaluationScope !== 'synthetic_only' || fact.value.fieldPerformance !== false || fact.value.individualActionAllowed !== false) throw new ReportEvidenceError('FACT_VALUE_INVALID')
  }
}

export function validateFactBundle(bundle) {
  exactKeys(bundle, ['schemaVersion', 'bundleId', 'generatedAt', 'sourceArtifactSha256', 'facts', 'factBundleSha256'], 'FACT_BUNDLE_KEYS_INVALID')
  if (bundle.schemaVersion !== 'report-fact-bundle.v1' || !/^bundle-[a-f0-9]{16}$/.test(bundle.bundleId) || Number.isNaN(Date.parse(bundle.generatedAt)) || !/^[a-f0-9]{64}$/.test(bundle.sourceArtifactSha256) || !/^[a-f0-9]{64}$/.test(bundle.factBundleSha256) || !Array.isArray(bundle.facts) || bundle.facts.length < 1 || bundle.facts.length > 50 || containsForbiddenKey(bundle)) throw new ReportEvidenceError('FACT_BUNDLE_INVALID')
  const seen = new Set()
  for (const fact of bundle.facts) {
    exactKeys(fact, ['factId', 'factType', 'sourceArtifactSha256', 'value'], 'FACT_KEYS_INVALID')
    if (!/^FACT-R11-[A-Z0-9-]{3,40}$/.test(fact.factId) || seen.has(fact.factId) || !allowedFactTypes.has(fact.factType) || fact.sourceArtifactSha256 !== bundle.sourceArtifactSha256) throw new ReportEvidenceError('FACT_INVALID')
    validateFactValue(fact)
    seen.add(fact.factId)
  }
  const payload = { ...bundle }; delete payload.factBundleSha256
  if (sha256(canonical(payload)) !== bundle.factBundleSha256) throw new ReportEvidenceError('FACT_BUNDLE_HASH_MISMATCH')
  return true
}

export function validateGroundedReport(report, bundle) {
  validateFactBundle(bundle)
  exactKeys(report, ['schemaVersion', 'reportId', 'audience', 'generatedAt', 'factBundleId', 'claims', 'receipt', 'reportSha256'], 'REPORT_KEYS_INVALID')
  if (report.schemaVersion !== 'grounded-operations-report.v1' || !/^report-[a-f0-9]{16}$/.test(report.reportId) || report.audience !== 'institution_operator' || report.factBundleId !== bundle.bundleId || report.generatedAt !== bundle.generatedAt || !Array.isArray(report.claims) || report.claims.length < 1 || report.claims.length > 50 || !/^[a-f0-9]{64}$/.test(report.reportSha256) || containsForbiddenKey(report)) throw new ReportEvidenceError('REPORT_INVALID')
  const factById = new Map(bundle.facts.map((fact) => [fact.factId, fact]))
  const claimIds = new Set()
  for (const claim of report.claims) {
    exactKeys(claim, ['claimId', 'claimType', 'status', 'text', 'evidenceFactIds'], 'CLAIM_KEYS_INVALID')
    if (!/^CLAIM-R11-[A-Z0-9-]{3,40}$/.test(claim.claimId) || claimIds.has(claim.claimId) || !allowedClaimTypes.has(claim.claimType) || claim.status !== 'grounded' || !Array.isArray(claim.evidenceFactIds) || claim.evidenceFactIds.length !== 1 || new Set(claim.evidenceFactIds).size !== claim.evidenceFactIds.length) throw new ReportEvidenceError('CLAIM_INVALID')
    claimIds.add(claim.claimId)
    const fact = factById.get(claim.evidenceFactIds[0])
    if (!fact || claim.text !== factText(fact)) throw new ReportEvidenceError('CLAIM_EVIDENCE_MISMATCH')
  }
  exactKeys(report.receipt, ['writer', 'validator', 'fallbackUsed', 'sourceArtifactSha256', 'factBundleSha256'], 'RECEIPT_KEYS_INVALID')
  if (report.receipt.writer !== 'deterministic_template.v1' || report.receipt.validator !== 'claim-evidence-validator.v1' || report.receipt.fallbackUsed !== true || report.receipt.sourceArtifactSha256 !== bundle.sourceArtifactSha256 || report.receipt.factBundleSha256 !== bundle.factBundleSha256) throw new ReportEvidenceError('RECEIPT_INVALID')
  const payload = { ...report }; delete payload.reportSha256
  if (sha256(canonical(payload)) !== report.reportSha256) throw new ReportEvidenceError('REPORT_HASH_MISMATCH')
  return true
}

export function buildSyntheticCalibrationReport(assessment) {
  if (assessment?.schemaVersion !== 'reliability-calibration-assessment.v1' || assessment.evaluationScope !== 'synthetic_only' || assessment.sourceKind !== 'synthetic' || assessment.deploymentAuthorized !== false || assessment.deploymentDecision !== 'defer' || !/^[a-f0-9]{64}$/.test(assessment.assessmentSha256) || !Array.isArray(assessment.components) || assessment.components.length !== 3 || assessment.components.some((component) => component.calibrationStatus !== 'not_estimable' || component.abstention !== true)) throw new ReportEvidenceError('ASSESSMENT_INVALID')
  const sourceArtifactSha256 = assessment.assessmentSha256
  const componentFacts = assessment.components.map((component) => ({ factId: `FACT-R11-${component.component.toUpperCase()}-READINESS`, factType: 'component_readiness', sourceArtifactSha256, value: { component: component.component, status: component.calibrationStatus, validationCount: component.validationCount, validationEventCount: component.validationEventCount } }))
  const facts = [...componentFacts, { factId: 'FACT-R11-FALLBACK-POLICY', factType: 'fallback_policy', sourceArtifactSha256, value: { label: '고정 점검 일정과 담당자 검토' } }, { factId: 'FACT-R11-SYNTHETIC-BOUNDARY', factType: 'scope_boundary', sourceArtifactSha256, value: { evaluationScope: 'synthetic_only', fieldPerformance: false, individualActionAllowed: false } }]
  const bundle = { schemaVersion: 'report-fact-bundle.v1', bundleId: `bundle-${sourceArtifactSha256.slice(0, 16)}`, generatedAt: assessment.generatedAt, sourceArtifactSha256, facts, factBundleSha256: '' }
  const bundlePayload = { ...bundle }; delete bundlePayload.factBundleSha256
  bundle.factBundleSha256 = sha256(canonical(bundlePayload))
  validateFactBundle(bundle)
  const claims = facts.map((fact, index) => ({ claimId: `CLAIM-R11-${String(index + 1).padStart(2, '0')}-${fact.factType.toUpperCase().replaceAll('_', '-')}`, claimType: fact.factType === 'component_readiness' ? 'readiness_summary' : fact.factType === 'fallback_policy' ? 'fallback_summary' : 'boundary_summary', status: 'grounded', text: factText(fact), evidenceFactIds: [fact.factId] }))
  const report = { schemaVersion: 'grounded-operations-report.v1', reportId: `report-${sourceArtifactSha256.slice(0, 16)}`, audience: 'institution_operator', generatedAt: assessment.generatedAt, factBundleId: bundle.bundleId, claims, receipt: { writer: 'deterministic_template.v1', validator: 'claim-evidence-validator.v1', fallbackUsed: true, sourceArtifactSha256, factBundleSha256: bundle.factBundleSha256 }, reportSha256: '' }
  const payload = { ...report }; delete payload.reportSha256
  report.reportSha256 = sha256(canonical(payload))
  validateGroundedReport(report, bundle)
  return { bundle, report }
}
