import test from 'node:test'
import assert from 'node:assert/strict'
import assessment from '../../contracts/fixtures/reliability-calibration-assessment.v1.valid.json' with { type: 'json' }
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
})
