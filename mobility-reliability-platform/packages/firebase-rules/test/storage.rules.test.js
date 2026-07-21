import { readFile } from 'node:fs/promises';
import { afterAll, beforeAll, describe, test } from 'vitest';
import {
  assertFails,
  initializeTestEnvironment
} from '@firebase/rules-unit-testing';
import { getBytes, ref, uploadString } from 'firebase/storage';

const projectId = 'demo-mobility-reliability';
const rulesPath = new URL('../../../storage.rules', import.meta.url);

let testEnvironment;

beforeAll(async () => {
  testEnvironment = await initializeTestEnvironment({
    projectId,
    storage: {
      rules: await readFile(rulesPath, 'utf8')
    }
  });
});

afterAll(async () => {
  await testEnvironment.cleanup();
});

describe('raw telemetry storage', () => {
  test('an authenticated client may not upload raw telemetry', async () => {
    const storage = testEnvironment.authenticatedContext('member-a').storage();
    await assertFails(uploadString(
      ref(storage, 'raw/tenant-a/session-1/batch-1.json'),
      '{}',
      'raw'
    ));
  });

  test('an unauthenticated client may not read raw telemetry', async () => {
    const storage = testEnvironment.unauthenticatedContext().storage();
    await assertFails(getBytes(ref(storage, 'raw/tenant-a/session-1/batch-1.json')));
  });
});
