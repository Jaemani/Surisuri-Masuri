import test from 'node:test'
import assert from 'node:assert/strict'
import assessment from '../../contracts/fixtures/reliability-calibration-assessment.v1.valid.json' with { type: 'json' }
import consoleSnapshot from '../../../apps/console/src/data/r12GroundedReport.json' with { type: 'json' }
import { ReportEvidenceError, buildSyntheticCalibrationReport, buildSyntheticFallbackRun, classifyCandidateClaims, createReportRun, transitionReportRun, validateFactBundle, validateGroundedReport } from '../src/index.js'

test('builds deterministic aggregate facts and a fully grounded fallback report', () => {
  const first = buildSyntheticCalibrationReport(assessment)
  const second = buildSyntheticCalibrationReport(assessment)
  assert.deepEqual(first, second)
  assert.equal(first.bundle.facts.length, 5)
  assert.equal(first.report.claims.length, 5)
  assert.ok(first.report.claims.every((claim) => claim.status === 'grounded' && claim.evidenceFactIds.length === 1))
  assert.equal(first.report.receipt.fallbackUsed, true)
})

test('console snapshot exactly matches the validated builder output', () => {
  assert.deepEqual(consoleSnapshot, buildSyntheticCalibrationReport(assessment))
})

test('rejects hallucinated text even when it references a real fact ID', () => {
  const { bundle, report } = buildSyntheticCalibrationReport(assessment)
  const tampered = structuredClone(report)
  tampered.claims[0].text = '배터리 고장 확률은 95%입니다.'
  assert.throws(() => validateGroundedReport(tampered, bundle), (error) => error instanceof ReportEvidenceError && error.code === 'CLAIM_EVIDENCE_MISMATCH')
})

test('rejects missing, duplicate, and foreign facts', () => {
  const { bundle, report } = buildSyntheticCalibrationReport(assessment)
  const missing = structuredClone(report)
  missing.claims[0].evidenceFactIds = ['FACT-R11-NOT-FOUND']
  assert.throws(() => validateGroundedReport(missing, bundle), /CLAIM_INVALID|CLAIM_EVIDENCE_MISMATCH/)
  const duplicate = structuredClone(bundle)
  duplicate.facts.push(structuredClone(duplicate.facts[0]))
  assert.throws(() => validateFactBundle(duplicate), /FACT_BUNDLE_INVALID|FACT_INVALID/)
})

test('rejects identity and raw-location fields anywhere in fact or report payloads', () => {
  const { bundle, report } = buildSyntheticCalibrationReport(assessment)
  const leaked = structuredClone(bundle)
  leaked.facts[0].value.deviceId = 'MOB-SECRET'
  assert.throws(() => validateFactBundle(leaked), /FACT_BUNDLE_INVALID/)
  const leakedReport = structuredClone(report)
  leakedReport.receipt.latitude = 37.5
  assert.throws(() => validateGroundedReport(leakedReport, bundle), /REPORT_KEYS_INVALID|REPORT_INVALID/)
  const snakeLeak = structuredClone(bundle)
  snakeLeak.facts[0].value.device_id = 'SECRET'
  assert.throws(() => validateFactBundle(snakeLeak), /FACT_BUNDLE_INVALID/)
})

test('binds nested facts and the entire fact bundle into both hashes', () => {
  const { bundle, report } = buildSyntheticCalibrationReport(assessment)
  const changedFact = structuredClone(bundle)
  changedFact.facts[0].value.validationCount += 1
  assert.throws(() => validateFactBundle(changedFact), /FACT_BUNDLE_HASH_MISMATCH/)
  const changedReceipt = structuredClone(report)
  changedReceipt.receipt.factBundleSha256 = '0'.repeat(64)
  assert.throws(() => validateGroundedReport(changedReceipt, bundle), /RECEIPT_INVALID/)
})

test('rejects forged assessment scope and malformed typed facts', () => {
  const forged = structuredClone(assessment)
  forged.evaluationScope = 'field'
  assert.throws(() => buildSyntheticCalibrationReport(forged), /ASSESSMENT_INVALID/)
  const { bundle } = buildSyntheticCalibrationReport(assessment)
  const malformed = structuredClone(bundle)
  malformed.facts[0].value.validationEventCount = malformed.facts[0].value.validationCount + 1
  assert.throws(() => validateFactBundle(malformed), /FACT_VALUE_INVALID/)
})

test('recomputes the complete source assessment hash before deriving facts', () => {
  const changed = structuredClone(assessment)
  changed.components[0].validationCount += 1
  assert.throws(() => buildSyntheticCalibrationReport(changed), (error) => error instanceof ReportEvidenceError && error.code === 'ASSESSMENT_HASH_MISMATCH')
})

test('rejects a rehashed assessment that weakens the accepted source contract', () => {
  const forged = structuredClone(assessment)
  forged.assessmentPolicy.testUsedForTuning = true
  assert.throws(() => buildSyntheticCalibrationReport(forged), (error) => error instanceof ReportEvidenceError && error.code === 'ASSESSMENT_POLICY_INVALID')
})

test('requires the complete fallback and synthetic boundary fact profile', () => {
  const { bundle } = buildSyntheticCalibrationReport(assessment)
  const incomplete = structuredClone(bundle)
  incomplete.facts = incomplete.facts.filter((fact) => fact.factType !== 'scope_boundary')
  assert.throws(() => validateFactBundle(incomplete), /FACT_BUNDLE_INVALID|FACT_PROFILE_INCOMPLETE/)
})

test('requires every fact to have its exact typed claim', () => {
  const { bundle, report } = buildSyntheticCalibrationReport(assessment)
  const missing = structuredClone(report)
  missing.claims.pop()
  assert.throws(() => validateGroundedReport(missing, bundle), /REPORT_INVALID|CLAIM_PROFILE_INCOMPLETE/)
  const mistyped = structuredClone(report)
  mistyped.claims[0].claimType = 'boundary_summary'
  assert.throws(() => validateGroundedReport(mistyped, bundle), /CLAIM_INVALID/)
})

test('rejects identifier value channels and arbitrary artifact identities', () => {
  const { bundle, report } = buildSyntheticCalibrationReport(assessment)
  const leaked = structuredClone(bundle)
  leaked.facts[0].factId = 'FACT-R11-PHONE-01012345678'
  assert.throws(() => validateFactBundle(leaked), /FACT_INVALID/)
  const substituted = structuredClone(report)
  substituted.reportId = `report-${'0'.repeat(16)}`
  assert.throws(() => validateGroundedReport(substituted, bundle), /REPORT_INVALID/)
})

test('moves a deterministic report through validation into fallback review', () => {
  const { bundle, report } = buildSyntheticCalibrationReport(assessment)
  const run = buildSyntheticFallbackRun(bundle, report)
  assert.equal(run.status, 'fallback')
  assert.equal(run.reviewStatus, 'pending')
  assert.equal(run.publicationStatus, 'unpublished')
  assert.equal(run.fallbackUsed, true)
  assert.equal(run.artifactSha256, report.reportSha256)
})

test('rejects skipped, reversed, and post-terminal report run transitions', () => {
  const pending = createReportRun({ reportRunId: `report-run-${assessment.assessmentSha256.slice(0, 16)}`, sourceArtifactSha256: assessment.assessmentSha256, factBundleSha256: '1'.repeat(64), createdAt: assessment.generatedAt })
  assert.throws(() => transitionReportRun(pending, 'fallback', { fallbackUsed: true, artifactSha256: '2'.repeat(64), completedAt: assessment.generatedAt }), /REPORT_RUN_TRANSITION_INVALID/)
  const validated = transitionReportRun(pending, 'validated')
  const fallback = transitionReportRun(validated, 'fallback', { fallbackUsed: true, artifactSha256: '2'.repeat(64), completedAt: assessment.generatedAt })
  assert.throws(() => transitionReportRun(fallback, 'completed', { fallbackUsed: false, artifactSha256: '3'.repeat(64), completedAt: assessment.generatedAt }), /REPORT_RUN_TRANSITION_INVALID/)
})

test('requires an allowlisted failure class and separates primary from fallback', () => {
  const pending = createReportRun({ reportRunId: `report-run-${assessment.assessmentSha256.slice(0, 16)}`, sourceArtifactSha256: assessment.assessmentSha256, factBundleSha256: '1'.repeat(64), createdAt: assessment.generatedAt })
  assert.throws(() => transitionReportRun(pending, 'failed'), /REPORT_RUN_FAILURE_CLASS_REQUIRED/)
  const failed = transitionReportRun(pending, 'failed', { failureClass: 'source_invalid' })
  assert.equal(failed.reviewStatus, 'not_ready')
  const validated = transitionReportRun(pending, 'validated')
  assert.throws(() => transitionReportRun(validated, 'completed', { fallbackUsed: true, artifactSha256: '2'.repeat(64), completedAt: assessment.generatedAt }), /REPORT_RUN_PRIMARY_REQUIRED/)
})

test('omits unsupported candidate text before final report sealing', () => {
  const { bundle, report } = buildSyntheticCalibrationReport(assessment)
  const candidates = report.claims.map(({ status: _status, ...claim }) => claim)
  candidates[0].text = '배터리 고장 확률은 95%입니다.'
  const result = classifyCandidateClaims(candidates, bundle)
  assert.equal(result.included.length, 4)
  assert.equal(result.omittedCount, 1)
  assert.deepEqual(result.dispositions[0], { candidateIndex: 0, disposition: 'omit', validationCodes: ['claim_text_unsupported'] })
  assert.equal(JSON.stringify(result).includes('95%'), false)
})

test('does not echo sensitive or foreign candidate content into dispositions', () => {
  const { bundle } = buildSyntheticCalibrationReport(assessment)
  const result = classifyCandidateClaims([{ claimId: 'CLAIM-R11-PHONE-01012345678', claimType: 'readiness_summary', text: '민감 자유문', evidenceFactIds: ['FACT-R11-NOT-FOUND'], deviceId: 'SECRET' }], bundle)
  assert.deepEqual(result, { included: [], dispositions: [{ candidateIndex: 0, disposition: 'omit', validationCodes: ['candidate_shape_or_sensitive_key'] }], omittedCount: 1 })
  assert.equal(JSON.stringify(result).includes('SECRET'), false)
})
