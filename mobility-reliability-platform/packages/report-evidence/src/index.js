import { createHash } from 'node:crypto'

const allowedFactTypes = new Set(['component_readiness', 'fallback_policy', 'scope_boundary'])
const allowedClaimTypes = new Set(['readiness_summary', 'fallback_summary', 'boundary_summary'])
const requiredFactProfile = new Map([
  ['FACT-R11-BATTERY-READINESS', ['component_readiness', 'battery', 'readiness_summary', 'CLAIM-R11-01-COMPONENT-READINESS']],
  ['FACT-R11-BRAKE-READINESS', ['component_readiness', 'brake', 'readiness_summary', 'CLAIM-R11-02-COMPONENT-READINESS']],
  ['FACT-R11-CONTROLLER-READINESS', ['component_readiness', 'controller', 'readiness_summary', 'CLAIM-R11-03-COMPONENT-READINESS']],
  ['FACT-R11-FALLBACK-POLICY', ['fallback_policy', null, 'fallback_summary', 'CLAIM-R11-04-FALLBACK-POLICY']],
  ['FACT-R11-SYNTHETIC-BOUNDARY', ['scope_boundary', null, 'boundary_summary', 'CLAIM-R11-05-SCOPE-BOUNDARY']],
])
const forbiddenKeys = new Set(['personId', 'person_id', 'deviceId', 'device_id', 'tenantId', 'tenant_id', 'firebaseUid', 'firebase_uid', 'actorUid', 'actor_uid', 'latitude', 'longitude', 'coordinates', 'rawPath', 'raw_path', 'objectPath', 'object_path', 'repairMemo', 'repair_memo', 'sourceRef', 'source_ref'])

export class ReportEvidenceError extends Error {
  constructor(code) { super(code); this.code = code }
}

const reportRunTransitions = {
  pending: new Set(['validated', 'failed']),
  validated: new Set(['completed', 'fallback', 'failed']),
  completed: new Set(),
  fallback: new Set(),
  failed: new Set(),
}

const stable = (value) => Array.isArray(value)
  ? value.map(stable)
  : value && typeof value === 'object'
    ? Object.fromEntries(Object.entries(value).sort(([left], [right]) => left < right ? -1 : left > right ? 1 : 0).map(([key, child]) => [key, stable(child)]))
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
function validTimestamp(value) { return typeof value === 'string' && /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(value) && !Number.isNaN(Date.parse(value)) }

function validateSourceAssessment(assessment) {
  exactKeys(assessment, ['schemaVersion', 'assessmentId', 'generatedAt', 'evaluationScope', 'sourceKind', 'evaluatorVersion', 'policyVersion', 'lineage', 'assessmentPolicy', 'factBoundary', 'components', 'limitations', 'deploymentAuthorized', 'deploymentDecision', 'assessmentSha256'], 'ASSESSMENT_KEYS_INVALID')
  if (assessment.schemaVersion !== 'reliability-calibration-assessment.v1' || !/^[0-9a-f-]{36}$/.test(assessment.assessmentId) || !validTimestamp(assessment.generatedAt) || assessment.evaluationScope !== 'synthetic_only' || assessment.sourceKind !== 'synthetic' || assessment.evaluatorVersion !== 'r11-calibration-estimability.v1' || assessment.policyVersion !== 'r11-calibration-abstention-policy.v1' || assessment.deploymentAuthorized !== false || assessment.deploymentDecision !== 'defer' || !/^[a-f0-9]{64}$/.test(assessment.assessmentSha256) || containsForbiddenKey(assessment)) throw new ReportEvidenceError('ASSESSMENT_INVALID')
  exactKeys(assessment.lineage, ['datasetSha256', 'baselineResultSha256'], 'ASSESSMENT_LINEAGE_INVALID')
  if (!/^[a-f0-9]{64}$/.test(assessment.lineage.datasetSha256) || !/^[a-f0-9]{64}$/.test(assessment.lineage.baselineResultSha256)) throw new ReportEvidenceError('ASSESSMENT_LINEAGE_INVALID')
  exactKeys(assessment.assessmentPolicy, ['method', 'horizonDays', 'riskThreshold', 'minimumSamples', 'minimumEvents', 'minimumDistinctScores', 'validationPurpose', 'testPurpose', 'testUsedForTuning'], 'ASSESSMENT_POLICY_INVALID')
  if (assessment.assessmentPolicy.method !== 'kaplan_meier' || assessment.assessmentPolicy.horizonDays !== 30 || assessment.assessmentPolicy.riskThreshold !== 0.5 || assessment.assessmentPolicy.minimumSamples !== 30 || assessment.assessmentPolicy.minimumEvents !== 10 || assessment.assessmentPolicy.minimumDistinctScores !== 3 || assessment.assessmentPolicy.validationPurpose !== 'calibration_and_abstention_assessment' || assessment.assessmentPolicy.testPurpose !== 'untouched_final_measurement' || assessment.assessmentPolicy.testUsedForTuning !== false) throw new ReportEvidenceError('ASSESSMENT_POLICY_INVALID')
  exactKeys(assessment.factBoundary, ['riskResetSourceQuality', 'explicitRiskResetFactCount', 'componentLinkInferenceAllowed', 'rawRepairTextIncluded', 'identityIncluded'], 'ASSESSMENT_BOUNDARY_INVALID')
  if (assessment.factBoundary.riskResetSourceQuality !== 'verified_synthetic' || !validCount(assessment.factBoundary.explicitRiskResetFactCount) || assessment.factBoundary.componentLinkInferenceAllowed !== false || assessment.factBoundary.rawRepairTextIncluded !== false || assessment.factBoundary.identityIncluded !== false) throw new ReportEvidenceError('ASSESSMENT_BOUNDARY_INVALID')
  const expectedLimitations = ['aggregate_only', 'no_field_calibration', 'no_individual_action', 'not_for_safety_critical_failure_prediction', 'synthetic_data_only']
  if (!Array.isArray(assessment.limitations) || [...assessment.limitations].sort().join('|') !== expectedLimitations.join('|')) throw new ReportEvidenceError('ASSESSMENT_LIMITATIONS_INVALID')
  if (!Array.isArray(assessment.components) || assessment.components.length !== 3) throw new ReportEvidenceError('ASSESSMENT_COMPONENTS_INVALID')
  const components = new Set()
  for (const component of assessment.components) {
    exactKeys(component, ['component', 'validationCount', 'validationEventCount', 'testCount', 'testEventCount', 'distinctValidationScoreCount', 'fallback', 'calibrationStatus', 'abstention', 'notEstimableReason'], 'ASSESSMENT_COMPONENT_INVALID')
    if (!['battery', 'brake', 'controller'].includes(component.component) || components.has(component.component) || component.calibrationStatus !== 'not_estimable' || component.abstention !== true || !['reliability_train_insufficient', 'calibration_sample_insufficient', 'calibration_event_insufficient', 'score_variation_insufficient'].includes(component.notEstimableReason) || component.fallback !== 'fixed_interval_and_human_review' || !validCount(component.validationCount) || !validCount(component.validationEventCount) || !validCount(component.testCount) || !validCount(component.testEventCount) || !validCount(component.distinctValidationScoreCount) || component.validationEventCount > component.validationCount || component.testEventCount > component.testCount) throw new ReportEvidenceError('ASSESSMENT_COMPONENT_INVALID')
    components.add(component.component)
  }
  const payload = { ...assessment }; delete payload.assessmentSha256
  if (sha256(canonical(payload)) !== assessment.assessmentSha256) throw new ReportEvidenceError('ASSESSMENT_HASH_MISMATCH')
}
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
  if (bundle.schemaVersion !== 'report-fact-bundle.v1' || bundle.bundleId !== `bundle-${bundle.sourceArtifactSha256?.slice(0, 16)}` || !validTimestamp(bundle.generatedAt) || !/^[a-f0-9]{64}$/.test(bundle.sourceArtifactSha256) || !/^[a-f0-9]{64}$/.test(bundle.factBundleSha256) || !Array.isArray(bundle.facts) || bundle.facts.length !== requiredFactProfile.size || containsForbiddenKey(bundle)) throw new ReportEvidenceError('FACT_BUNDLE_INVALID')
  const seen = new Set()
  for (const fact of bundle.facts) {
    exactKeys(fact, ['factId', 'factType', 'sourceArtifactSha256', 'value'], 'FACT_KEYS_INVALID')
    const profile = requiredFactProfile.get(fact.factId)
    if (!profile || seen.has(fact.factId) || !allowedFactTypes.has(fact.factType) || fact.factType !== profile[0] || (profile[1] !== null && fact.value?.component !== profile[1]) || fact.sourceArtifactSha256 !== bundle.sourceArtifactSha256) throw new ReportEvidenceError('FACT_INVALID')
    validateFactValue(fact)
    seen.add(fact.factId)
  }
  if (seen.size !== requiredFactProfile.size) throw new ReportEvidenceError('FACT_PROFILE_INCOMPLETE')
  const payload = { ...bundle }; delete payload.factBundleSha256
  if (sha256(canonical(payload)) !== bundle.factBundleSha256) throw new ReportEvidenceError('FACT_BUNDLE_HASH_MISMATCH')
  return true
}

export function validateGroundedReport(report, bundle) {
  validateFactBundle(bundle)
  exactKeys(report, ['schemaVersion', 'reportId', 'audience', 'generatedAt', 'factBundleId', 'claims', 'receipt', 'reportSha256'], 'REPORT_KEYS_INVALID')
  if (report.schemaVersion !== 'grounded-operations-report.v1' || report.reportId !== `report-${bundle.sourceArtifactSha256.slice(0, 16)}` || report.audience !== 'institution_operator' || report.factBundleId !== bundle.bundleId || report.generatedAt !== bundle.generatedAt || !Array.isArray(report.claims) || report.claims.length !== requiredFactProfile.size || !/^[a-f0-9]{64}$/.test(report.reportSha256) || containsForbiddenKey(report)) throw new ReportEvidenceError('REPORT_INVALID')
  const factById = new Map(bundle.facts.map((fact) => [fact.factId, fact]))
  const claimIds = new Set()
  for (const claim of report.claims) {
    exactKeys(claim, ['claimId', 'claimType', 'status', 'text', 'evidenceFactIds'], 'CLAIM_KEYS_INVALID')
    const profile = requiredFactProfile.get(claim.evidenceFactIds?.[0])
    if (!profile || claim.claimId !== profile[3] || claimIds.has(claim.claimId) || !allowedClaimTypes.has(claim.claimType) || claim.claimType !== profile[2] || claim.status !== 'grounded' || !Array.isArray(claim.evidenceFactIds) || claim.evidenceFactIds.length !== 1) throw new ReportEvidenceError('CLAIM_INVALID')
    claimIds.add(claim.claimId)
    const fact = factById.get(claim.evidenceFactIds[0])
    if (!fact || claim.text !== factText(fact)) throw new ReportEvidenceError('CLAIM_EVIDENCE_MISMATCH')
  }
  if (claimIds.size !== requiredFactProfile.size) throw new ReportEvidenceError('CLAIM_PROFILE_INCOMPLETE')
  exactKeys(report.receipt, ['writer', 'validator', 'fallbackUsed', 'sourceArtifactSha256', 'factBundleSha256'], 'RECEIPT_KEYS_INVALID')
  if (report.receipt.writer !== 'deterministic_template.v1' || report.receipt.validator !== 'claim-evidence-validator.v1' || report.receipt.fallbackUsed !== true || report.receipt.sourceArtifactSha256 !== bundle.sourceArtifactSha256 || report.receipt.factBundleSha256 !== bundle.factBundleSha256) throw new ReportEvidenceError('RECEIPT_INVALID')
  const payload = { ...report }; delete payload.reportSha256
  if (sha256(canonical(payload)) !== report.reportSha256) throw new ReportEvidenceError('REPORT_HASH_MISMATCH')
  return true
}

export function buildSyntheticCalibrationReport(assessment) {
  validateSourceAssessment(assessment)
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

export function createReportRun({ reportRunId, sourceArtifactSha256, factBundleSha256, createdAt }) {
  if (!/^report-run-[a-f0-9]{16}$/.test(reportRunId) || !/^[a-f0-9]{64}$/.test(sourceArtifactSha256) || !/^[a-f0-9]{64}$/.test(factBundleSha256) || !validTimestamp(createdAt)) throw new ReportEvidenceError('REPORT_RUN_INPUT_INVALID')
  return { schemaVersion: 'report-run-lifecycle.v1', reportRunId, sourceArtifactSha256, factBundleSha256, status: 'pending', reviewStatus: 'not_ready', publicationStatus: 'unpublished', fallbackUsed: false, failureClass: null, artifactSha256: null, createdAt, completedAt: null, revision: 1 }
}

export function transitionReportRun(run, targetStatus, options = {}) {
  exactKeys(run, ['schemaVersion', 'reportRunId', 'sourceArtifactSha256', 'factBundleSha256', 'status', 'reviewStatus', 'publicationStatus', 'fallbackUsed', 'failureClass', 'artifactSha256', 'createdAt', 'completedAt', 'revision'], 'REPORT_RUN_KEYS_INVALID')
  if (run.schemaVersion !== 'report-run-lifecycle.v1' || !reportRunTransitions[run.status]?.has(targetStatus) || run.publicationStatus !== 'unpublished' || !Number.isSafeInteger(run.revision) || run.revision < 1) throw new ReportEvidenceError('REPORT_RUN_TRANSITION_INVALID')
  if (targetStatus === 'validated') return { ...run, status: 'validated', revision: run.revision + 1 }
  if (targetStatus === 'failed') {
    if (!['source_invalid', 'claim_validation_failed', 'artifact_sealing_failed'].includes(options.failureClass)) throw new ReportEvidenceError('REPORT_RUN_FAILURE_CLASS_REQUIRED')
    return { ...run, status: 'failed', failureClass: options.failureClass, revision: run.revision + 1 }
  }
  if (!/^[a-f0-9]{64}$/.test(options.artifactSha256) || !validTimestamp(options.completedAt)) throw new ReportEvidenceError('REPORT_RUN_TERMINAL_INPUT_INVALID')
  if (targetStatus === 'fallback' && options.fallbackUsed !== true) throw new ReportEvidenceError('REPORT_RUN_FALLBACK_REQUIRED')
  if (targetStatus === 'completed' && options.fallbackUsed !== false) throw new ReportEvidenceError('REPORT_RUN_PRIMARY_REQUIRED')
  return { ...run, status: targetStatus, reviewStatus: 'pending', fallbackUsed: options.fallbackUsed, artifactSha256: options.artifactSha256, completedAt: options.completedAt, revision: run.revision + 1 }
}

export function buildSyntheticFallbackRun(bundle, report) {
  validateGroundedReport(report, bundle)
  const pending = createReportRun({ reportRunId: `report-run-${bundle.sourceArtifactSha256.slice(0, 16)}`, sourceArtifactSha256: bundle.sourceArtifactSha256, factBundleSha256: bundle.factBundleSha256, createdAt: report.generatedAt })
  const validated = transitionReportRun(pending, 'validated')
  return transitionReportRun(validated, 'fallback', { fallbackUsed: true, artifactSha256: report.reportSha256, completedAt: report.generatedAt })
}
