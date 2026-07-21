import { readFile } from 'node:fs/promises';
import { afterAll, afterEach, beforeAll, describe, test } from 'vitest';
import {
  assertFails,
  assertSucceeds,
  initializeTestEnvironment
} from '@firebase/rules-unit-testing';
import { doc, getDoc, setDoc, Timestamp } from 'firebase/firestore';

const projectId = 'demo-mobility-reliability';
const rulesPath = new URL('../../../firestore.rules', import.meta.url);
const storageRulesPath = new URL('../../../storage.rules', import.meta.url);

let testEnvironment;

async function seedDocument(path, data = {}) {
  await testEnvironment.withSecurityRulesDisabled(async (context) => {
    await setDoc(doc(context.firestore(), path), data);
  });
}

async function seedMembership(
  tenantId,
  firebaseUid,
  {
    roles = ['beneficiary'],
    status = 'active',
    validFrom = Timestamp.fromMillis(Date.now() - 60_000),
    validTo
  } = {}
) {
  const membership = {
    tenant_id: tenantId,
    firebase_uid: firebaseUid,
    roles,
    status,
    valid_from: validFrom,
    policy_version: 1,
    created_at: Timestamp.fromMillis(Date.now() - 60_000),
    updated_at: Timestamp.fromMillis(Date.now() - 60_000)
  };

  if (validTo !== undefined) {
    membership.valid_to = validTo;
  }

  await seedDocument(
    `tenants/${tenantId}/memberships/${firebaseUid}`,
    membership
  );
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

describe('canonical membership authorization', () => {
  test('an active member with a roles array may read its tenant and domain documents', async () => {
    await seedMembership('tenant-a', 'member-a', {
      roles: ['beneficiary', 'guardian']
    });
    await seedDocument('tenants/tenant-a', {
      tenant_id: 'tenant-a',
      display_name: 'Tenant A'
    });
    await seedDocument('tenants/tenant-a/devices/device-1', {
      tenant_id: 'tenant-a',
      device_id: 'device-1'
    });

    const db = testEnvironment.authenticatedContext('member-a').firestore();

    await assertSucceeds(getDoc(doc(db, 'tenants/tenant-a')));
    await assertSucceeds(
      getDoc(doc(db, 'tenants/tenant-a/memberships/member-a'))
    );
    await assertSucceeds(
      getDoc(doc(db, 'tenants/tenant-a/devices/device-1'))
    );
  });

  test('a revoked membership is denied', async () => {
    await seedMembership('tenant-a', 'member-a', { status: 'revoked' });
    await seedDocument('tenants/tenant-a/devices/device-1');

    const db = testEnvironment.authenticatedContext('member-a').firestore();
    await assertFails(getDoc(doc(db, 'tenants/tenant-a/devices/device-1')));
  });

  test('an expired membership is denied', async () => {
    await seedMembership('tenant-a', 'member-a', {
      validTo: Timestamp.fromMillis(Date.now() - 1_000)
    });
    await seedDocument('tenants/tenant-a/devices/device-1');

    const db = testEnvironment.authenticatedContext('member-a').firestore();
    await assertFails(getDoc(doc(db, 'tenants/tenant-a/devices/device-1')));
  });

  test('a membership that is not yet valid is denied', async () => {
    await seedMembership('tenant-a', 'member-a', {
      validFrom: Timestamp.fromMillis(Date.now() + 60_000)
    });
    await seedDocument('tenants/tenant-a/devices/device-1');

    const db = testEnvironment.authenticatedContext('member-a').firestore();
    await assertFails(getDoc(doc(db, 'tenants/tenant-a/devices/device-1')));
  });

  test('a scalar role cannot replace the canonical roles array', async () => {
    await seedMembership('tenant-a', 'member-a', { roles: 'beneficiary' });
    await seedDocument('tenants/tenant-a/devices/device-1');

    const db = testEnvironment.authenticatedContext('member-a').firestore();
    await assertFails(getDoc(doc(db, 'tenants/tenant-a/devices/device-1')));
  });

  test('an unknown role cannot activate a membership', async () => {
    await seedMembership('tenant-a', 'member-a', { roles: ['owner'] });
    await seedDocument('tenants/tenant-a/devices/device-1');

    const db = testEnvironment.authenticatedContext('member-a').firestore();
    await assertFails(getDoc(doc(db, 'tenants/tenant-a/devices/device-1')));
  });

  test('a member may not cross the tenant boundary', async () => {
    await seedMembership('tenant-a', 'member-a');
    await seedDocument('tenants/tenant-b/devices/device-1', {
      tenant_id: 'tenant-b',
      device_id: 'device-1'
    });

    const db = testEnvironment.authenticatedContext('member-a').firestore();
    await assertFails(getDoc(doc(db, 'tenants/tenant-b/devices/device-1')));
  });

  test('unauthenticated access is denied', async () => {
    await seedDocument('tenants/tenant-a/devices/device-1');
    const db = testEnvironment.unauthenticatedContext().firestore();

    await assertFails(getDoc(doc(db, 'tenants/tenant-a/devices/device-1')));
  });
});

describe('server-owned mutation boundary', () => {
  test('active members cannot directly write protected domain paths', async () => {
    await seedMembership('tenant-a', 'member-a', { roles: ['tenant_admin'] });
    const db = testEnvironment.authenticatedContext('member-a').firestore();
    const protectedPaths = [
      'tenants/tenant-a',
      'tenants/tenant-a/memberships/new-member',
      'tenants/tenant-a/appInstallations/installation-1',
      'tenants/tenant-a/devices/device-1',
      'tenants/tenant-a/deviceAssignments/assignment-1',
      'tenants/tenant-a/consentRevisions/consent-1',
      'tenants/tenant-a/trips/trip-1',
      'tenants/tenant-a/ingestReceipts/batch-1',
      'tenants/tenant-a/ingestIdempotency/key-1',
      'tenants/tenant-a/ingestClientBatches/key-1',
      'tenants/tenant-a/repairs/repair-1',
      'tenants/tenant-a/repairs/repair-1/items/item-1',
      'tenants/tenant-a/inspections/inspection-1',
      'tenants/tenant-a/inspections/inspection-1/observations/observation-1'
    ];

    for (const path of protectedPaths) {
      await assertFails(setDoc(doc(db, path), {
        tenant_id: 'tenant-a',
        created_by: 'member-a'
      }));
    }
  });

  test('trip summaries are readable but client writes are denied', async () => {
    await seedMembership('tenant-a', 'member-a');
    await seedDocument('tenants/tenant-a/trips/trip-1', {
      tenant_id: 'tenant-a',
      trip_id: 'trip-1',
      raw_sample_count: 0
    });
    const db = testEnvironment.authenticatedContext('member-a').firestore();

    await assertSucceeds(getDoc(doc(db, 'tenants/tenant-a/trips/trip-1')));
    await assertFails(setDoc(doc(db, 'tenants/tenant-a/trips/trip-2'), {
      tenant_id: 'tenant-a',
      trip_id: 'trip-2'
    }));
  });

  test('ingest receipts and idempotency records deny client reads and writes', async () => {
    await seedMembership('tenant-a', 'member-a');
    await seedDocument('tenants/tenant-a/ingestReceipts/batch-1', {
      tenant_id: 'tenant-a',
      body_hash: 'sha256:receipt'
    });
    await seedDocument('tenants/tenant-a/ingestIdempotency/key-1', {
      tenant_id: 'tenant-a',
      body_hash: 'sha256:idempotency'
    });
    await seedDocument('tenants/tenant-a/ingestClientBatches/key-1', {
      tenant_id: 'tenant-a',
      client_batch_id: 'client-batch-1'
    });
    const db = testEnvironment.authenticatedContext('member-a').firestore();

    await assertFails(
      getDoc(doc(db, 'tenants/tenant-a/ingestReceipts/batch-1'))
    );
    await assertFails(
      getDoc(doc(db, 'tenants/tenant-a/ingestIdempotency/key-1'))
    );
    await assertFails(
      getDoc(doc(db, 'tenants/tenant-a/ingestClientBatches/key-1'))
    );
    await assertFails(
      setDoc(doc(db, 'tenants/tenant-a/ingestReceipts/batch-2'), {
        tenant_id: 'tenant-a'
      })
    );
    await assertFails(
      setDoc(doc(db, 'tenants/tenant-a/ingestIdempotency/key-2'), {
        tenant_id: 'tenant-a'
      })
    );
  });
});

describe('default deny', () => {
  test('unknown tenant and global paths deny reads and writes', async () => {
    await seedMembership('tenant-a', 'member-a');
    await seedDocument('tenants/tenant-a/unknownDomain/document-1', {
      tenant_id: 'tenant-a'
    });
    await seedDocument('unknownGlobal/document-1');
    const db = testEnvironment.authenticatedContext('member-a').firestore();

    await assertFails(
      getDoc(doc(db, 'tenants/tenant-a/unknownDomain/document-1'))
    );
    await assertFails(
      setDoc(doc(db, 'tenants/tenant-a/unknownDomain/document-2'), {
        tenant_id: 'tenant-a'
      })
    );
    await assertFails(getDoc(doc(db, 'unknownGlobal/document-1')));
    await assertFails(setDoc(doc(db, 'unknownGlobal/document-2'), {}));
  });
});
