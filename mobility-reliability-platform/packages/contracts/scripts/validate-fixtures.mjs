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
