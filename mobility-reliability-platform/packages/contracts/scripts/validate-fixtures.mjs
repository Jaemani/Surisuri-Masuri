import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

import Ajv2020 from 'ajv/dist/2020.js'
import addFormats from 'ajv-formats'

const rootUrl = new URL('../', import.meta.url)

async function readJson(relativePath) {
  const contents = await readFile(new URL(relativePath, rootUrl), 'utf8')
  return JSON.parse(contents)
}

const ajv = new Ajv2020({ allErrors: true, strict: true })
addFormats(ajv)

const validators = new Map()

async function getValidator(schemaPath) {
  const existing = validators.get(schemaPath)
  if (existing) return existing

  const schema = await readJson(schemaPath)
  const validator = ajv.compile(schema)
  validators.set(schemaPath, validator)
  return validator
}

const cases = [
  {
    name: 'valid repair work order v1',
    schema: 'schemas/repair-work-order.v1.schema.json',
    fixture: 'fixtures/repair-work-order.v1.valid.json',
    expected: true,
  },
  {
    name: 'invalid repair work order v1',
    schema: 'schemas/repair-work-order.v1.schema.json',
    fixture: 'fixtures/repair-work-order.v1.invalid.json',
    expected: false,
  },
  {
    name: 'valid subsidy ledger transaction v1',
    schema: 'schemas/subsidy-ledger-transaction.v1.schema.json',
    fixture: 'fixtures/subsidy-ledger-transaction.v1.valid.json',
    expected: true,
  },
  {
    name: 'invalid subsidy ledger transaction v1',
    schema: 'schemas/subsidy-ledger-transaction.v1.schema.json',
    fixture: 'fixtures/subsidy-ledger-transaction.v1.invalid.json',
    expected: false,
  },
  {
    name: 'valid legacy import record v1',
    schema: 'schemas/legacy-import-record.v1.schema.json',
    fixture: 'fixtures/legacy-import-record.v1.valid.json',
    expected: true,
  },
  {
    name: 'invalid legacy import record v1',
    schema: 'schemas/legacy-import-record.v1.schema.json',
    fixture: 'fixtures/legacy-import-record.v1.invalid.json',
    expected: false,
  },
  {
    name: 'valid quality features v1',
    schema: 'schemas/quality-features.v1.schema.json',
    fixture: 'fixtures/quality-features.v1.valid.json',
    expected: true,
  },
  {
    name: 'valid quality features v1 review state',
    schema: 'schemas/quality-features.v1.schema.json',
    fixture: 'fixtures/quality-features.v1.review.valid.json',
    expected: true,
  },
  {
    name: 'invalid quality features v1',
    schema: 'schemas/quality-features.v1.schema.json',
    fixture: 'fixtures/quality-features.v1.invalid.json',
    expected: false,
  },
  {
    name: 'valid quality baseline result v1',
    schema: 'schemas/quality-baseline-result.v1.schema.json',
    fixture: 'fixtures/quality-baseline-result.v1.valid.json',
    expected: true,
  },
  {
    name: 'invalid quality baseline result v1',
    schema: 'schemas/quality-baseline-result.v1.schema.json',
    fixture: 'fixtures/quality-baseline-result.v1.invalid.json',
    expected: false,
  },
  {
    name: 'valid quality label v1',
    schema: 'schemas/quality-label.v1.schema.json',
    fixture: 'fixtures/quality-label.v1.valid.json',
    expected: true,
  },
  {
    name: 'invalid quality label v1',
    schema: 'schemas/quality-label.v1.schema.json',
    fixture: 'fixtures/quality-label.v1.invalid.json',
    expected: false,
  },
  {
    name: 'valid quality label v1 review state',
    schema: 'schemas/quality-label.v1.schema.json',
    fixture: 'fixtures/quality-label.v1.review.valid.json',
    expected: true,
  },
  {
    name: 'valid quality dataset manifest v1',
    schema: 'schemas/quality-dataset-manifest.v1.schema.json',
    fixture: 'fixtures/quality-dataset-manifest.v1.valid.json',
    expected: true,
  },
  {
    name: 'invalid quality dataset manifest v1',
    schema: 'schemas/quality-dataset-manifest.v1.schema.json',
    fixture: 'fixtures/quality-dataset-manifest.v1.invalid.json',
    expected: false,
  },
  {
    name: 'valid quality field holdout v1',
    schema: 'schemas/quality-field-holdout.v1.schema.json',
    fixture: 'fixtures/quality-field-holdout.v1.valid.json',
    expected: true,
  },
  {
    name: 'invalid quality field holdout v1',
    schema: 'schemas/quality-field-holdout.v1.schema.json',
    fixture: 'fixtures/quality-field-holdout.v1.invalid.json',
    expected: false,
  },
  {
    name: 'valid quality field features v1',
    schema: 'schemas/quality-field-features.v1.schema.json',
    fixture: 'fixtures/quality-field-features.v1.valid.json',
    expected: true,
  },
  {
    name: 'invalid quality field features v1',
    schema: 'schemas/quality-field-features.v1.schema.json',
    fixture: 'fixtures/quality-field-features.v1.invalid.json',
    expected: false,
  },
  {
    name: 'valid quality model artifact v1',
    schema: 'schemas/quality-model-artifact.v1.schema.json',
    fixture: 'fixtures/quality-model-artifact.v1.valid.json',
    expected: true,
  },
  {
    name: 'invalid quality model artifact v1',
    schema: 'schemas/quality-model-artifact.v1.schema.json',
    fixture: 'fixtures/quality-model-artifact.v1.invalid.json',
    expected: false,
  },
  {
    name: 'valid quality field evaluation result v1',
    schema: 'schemas/quality-field-evaluation-result.v1.schema.json',
    fixture: 'fixtures/quality-field-evaluation-result.v1.valid.json',
    expected: true,
  },
  {
    name: 'invalid quality field evaluation result v1',
    schema: 'schemas/quality-field-evaluation-result.v1.schema.json',
    fixture: 'fixtures/quality-field-evaluation-result.v1.invalid.json',
    expected: false,
  },
  {
    name: 'valid telemetry batch v2',
    schema: 'schemas/telemetry-batch.v2.schema.json',
    fixture: 'fixtures/telemetry-batch.v2.valid.json',
    expected: true,
  },
  {
    name: 'invalid telemetry batch v2',
    schema: 'schemas/telemetry-batch.v2.schema.json',
    fixture: 'fixtures/telemetry-batch.v2.invalid.json',
    expected: false,
  },
  {
    name: 'valid telemetry batch v1 compatibility fixture',
    schema: 'schemas/telemetry-batch.v1.schema.json',
    fixture: 'fixtures/telemetry-batch.valid.json',
    expected: true,
  },
  {
    name: 'invalid telemetry batch v1 compatibility fixture',
    schema: 'schemas/telemetry-batch.v1.schema.json',
    fixture: 'fixtures/telemetry-batch.invalid.json',
    expected: false,
  },
  {
    name: 'valid domain event',
    schema: 'schemas/domain-event.v1.schema.json',
    fixture: 'fixtures/domain-event.valid.json',
    expected: true,
  },
  {
    name: 'invalid domain event',
    schema: 'schemas/domain-event.v1.schema.json',
    fixture: 'fixtures/domain-event.invalid.json',
    expected: false,
  },
  {
    name: 'valid device state event v1',
    schema: 'schemas/device-state-event.v1.schema.json',
    fixture: 'fixtures/device-state-event.v1.valid.json',
    expected: true,
  },
  {
    name: 'invalid device state event v1 rejects raw coordinates',
    schema: 'schemas/device-state-event.v1.schema.json',
    fixture: 'fixtures/device-state-event.v1.invalid.json',
    expected: false,
  },
  {
    name: 'valid legacy device event dry run v1',
    schema: 'schemas/legacy-device-event-dry-run.v1.schema.json',
    fixture: 'fixtures/legacy-device-event-dry-run.v1.valid.json',
    expected: true,
  },
  {
    name: 'invalid legacy device event dry run v1 rejects writes and source records',
    schema: 'schemas/legacy-device-event-dry-run.v1.schema.json',
    fixture: 'fixtures/legacy-device-event-dry-run.v1.invalid.json',
    expected: false,
  },
  {
    name: 'valid reliability baseline result v1',
    schema: 'schemas/reliability-baseline-result.v1.schema.json',
    fixture: 'fixtures/reliability-baseline-result.v1.valid.json',
    expected: true,
  },
  {
    name: 'invalid reliability baseline result v1 rejects field claims, metrics on abstention, and deployment',
    schema: 'schemas/reliability-baseline-result.v1.schema.json',
    fixture: 'fixtures/reliability-baseline-result.v1.invalid.json',
    expected: false,
  },
  {
    name: 'valid reliability comparison artifact v1',
    schema: 'schemas/reliability-comparison-artifact.v1.schema.json',
    fixture: 'fixtures/reliability-comparison-artifact.v1.valid.json',
    expected: true,
  },
  {
    name: 'invalid reliability comparison artifact v1 rejects field, identity, CTA, and abstention metrics',
    schema: 'schemas/reliability-comparison-artifact.v1.schema.json',
    fixture: 'fixtures/reliability-comparison-artifact.v1.invalid.json',
    expected: false,
  },
  {
    name: 'valid reliability calibration assessment v1',
    schema: 'schemas/reliability-calibration-assessment.v1.schema.json',
    fixture: 'fixtures/reliability-calibration-assessment.v1.valid.json',
    expected: true,
  },
  {
    name: 'invalid reliability calibration assessment v1 rejects field, tuning, inferred linkage, metrics on abstention, and deployment',
    schema: 'schemas/reliability-calibration-assessment.v1.schema.json',
    fixture: 'fixtures/reliability-calibration-assessment.v1.invalid.json',
    expected: false,
  },
]

for (const testCase of cases) {
  const fixture = await readJson(testCase.fixture)
  const validate = await getValidator(testCase.schema)
  const actual = validate(fixture)

  assert.equal(
    actual,
    testCase.expected,
    `${testCase.name}: ${ajv.errorsText(validate.errors, { separator: '\n' })}`
  )

  process.stdout.write(`PASS ${testCase.name}\n`)
}
