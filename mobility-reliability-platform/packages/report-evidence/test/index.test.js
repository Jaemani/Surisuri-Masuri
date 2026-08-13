import test from 'node:test'
import assert from 'node:assert/strict'
import assessment from '../../contracts/fixtures/reliability-calibration-assessment.v1.valid.json' with { type: 'json' }
import consoleSnapshot from '../../../apps/console/src/data/r12GroundedReport.json' with { type: 'json' }
import { ReportEvidenceError, buildSyntheticCalibrationReport, validateFactBundle, validateGroundedReport } from '../src/index.js'

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
  assert.throws(() => validateGroundedReport(missing, bundle), /CLAIM_EVIDENCE_MISMATCH/)
  const duplicate = structuredClone(bundle)
  duplicate.facts.push(structuredClone(duplicate.facts[0]))
  assert.throws(() => validateFactBundle(duplicate), /FACT_INVALID/)
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
