import { readFile } from 'node:fs/promises';
import { afterAll, afterEach, beforeAll, describe, test } from 'vitest';
import {
  assertFails,
  assertSucceeds,
  initializeTestEnvironment
} from '@firebase/rules-unit-testing';
import {
  doc,
  getDoc,
  serverTimestamp,
  setDoc
} from 'firebase/firestore';

const projectId = 'demo-mobility-reliability';
const rulesPath = new URL('../../../firestore.rules', import.meta.url);
const storageRulesPath = new URL('../../../storage.rules', import.meta.url);

let testEnvironment;

async function seedMember(tenantId, uid, role = 'beneficiary') {
  await testEnvironment.withSecurityRulesDisabled(async (context) => {
    await setDoc(
      doc(context.firestore(), `tenants/${tenantId}/members/${uid}`),
      { status: 'active', role }
    );
  });
}

async function seedDevice(tenantId, deviceId) {
  await testEnvironment.withSecurityRulesDisabled(async (context) => {
    await setDoc(doc(context.firestore(), `tenants/${tenantId}/devices/${deviceId}`), {
      tenantId,
      publicCode: `${tenantId}-${deviceId}`,
      status: 'active'
    });
  });
}

function validRepair(tenantId, uid) {
  return {
    tenantId,
    deviceId: 'device-1',
    occurredAt: new Date('2026-07-21T00:00:00.000Z'),
    repairKind: 'breakdown',
    status: 'completed',
    currency: 'KRW',
    sourceQuality: 'verified',
    createdBy: uid,
    createdAt: serverTimestamp(),
    updatedAt: serverTimestamp()
  };
}

beforeAll(async () => {
  testEnvironment = await initializeTestEnvironment({
    projectId,
    firestore: {
      rules: await readFile(rulesPath, 'utf8')
    },
    storage: {
      rules: await readFile(storageRulesPath, 'utf8')
    }
  });
});

afterEach(async () => {
  await testEnvironment.clearFirestore();
});

afterAll(async () => {
  await testEnvironment.cleanup();
});

describe('tenant isolation and server-owned data', () => {
  test('unauthenticated access is denied', async () => {
    const db = testEnvironment.unauthenticatedContext().firestore();
    await assertFails(getDoc(doc(db, 'tenants/tenant-a/devices/device-1')));
  });

  test('an active member may read its tenant device', async () => {
    await seedMember('tenant-a', 'member-a');
    await seedDevice('tenant-a', 'device-1');

    const db = testEnvironment.authenticatedContext('member-a').firestore();
    await assertSucceeds(getDoc(doc(db, 'tenants/tenant-a/devices/device-1')));
  });

  test('a member may not read another tenant', async () => {
    await seedMember('tenant-a', 'member-a');
    await seedDevice('tenant-b', 'device-1');

    const db = testEnvironment.authenticatedContext('member-a').firestore();
    await assertFails(getDoc(doc(db, 'tenants/tenant-b/devices/device-1')));
  });

  test('a client may not create an ingest receipt', async () => {
    await seedMember('tenant-a', 'member-a');
    const db = testEnvironment.authenticatedContext('member-a').firestore();

    await assertFails(setDoc(doc(db, 'tenants/tenant-a/ingestReceipts/batch-1'), {
      tenantId: 'tenant-a',
      status: 'accepted'
    }));
  });
});

describe('repair writes', () => {
  test('an authorized repairer may create a valid repair', async () => {
    await seedMember('tenant-a', 'repairer-a', 'repairer');
    const db = testEnvironment.authenticatedContext('repairer-a').firestore();

    await assertSucceeds(setDoc(
      doc(db, 'tenants/tenant-a/repairs/repair-1'),
      validRepair('tenant-a', 'repairer-a')
    ));
  });

  test('tenantId spoofing is denied', async () => {
    await seedMember('tenant-a', 'repairer-a', 'repairer');
    const db = testEnvironment.authenticatedContext('repairer-a').firestore();

    await assertFails(setDoc(
      doc(db, 'tenants/tenant-a/repairs/repair-1'),
      validRepair('tenant-b', 'repairer-a')
    ));
  });
});
