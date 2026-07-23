import { readFile } from 'node:fs/promises';
import { afterAll, afterEach, beforeAll, describe, test } from 'vitest';
import {
  assertFails,
  assertSucceeds,
  initializeTestEnvironment
} from '@firebase/rules-unit-testing';
import {
  collection,
  doc,
  getDoc,
  getDocs,
  query,
  setDoc,
  Timestamp,
  where
} from 'firebase/firestore';

const projectId = 'demo-mobility-reliability';
const consentStateDocumentId = 'a'.repeat(64);
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
    personId = 'person-a',
    validFrom = Timestamp.fromMillis(Date.now() - 60_000),
    validTo,
    tenantStatus = 'active',
    tenantDocumentTenantId = tenantId
  } = {}
) {
  await seedDocument(`tenants/${tenantId}`, {
    tenant_id: tenantDocumentTenantId,
    display_name: tenantId,
    status: tenantStatus
  });

  const membership = {
    tenant_id: tenantId,
    firebase_uid: firebaseUid,
    person_id: personId,
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
  test('an active member may get its tenant, own membership, and part catalog only through explicit reads', async () => {
    await seedMembership('tenant-a', 'member-a', {
      roles: ['beneficiary', 'guardian']
    });
    await seedDocument('tenants/tenant-a/partCatalog/part-1', {
      tenant_id: 'tenant-a',
      part_id: 'part-1'
    });

    const db = testEnvironment.authenticatedContext('member-a').firestore();

    await assertSucceeds(getDoc(doc(db, 'tenants/tenant-a')));
    await assertSucceeds(
      getDoc(doc(db, 'tenants/tenant-a/memberships/member-a'))
    );
    await assertSucceeds(
      getDoc(doc(db, 'tenants/tenant-a/partCatalog/part-1'))
    );
    await assertFails(getDocs(collection(db, 'tenants')));
    await assertFails(
      getDocs(collection(db, 'tenants/tenant-a/memberships'))
    );
  });

  test('a revoked membership is denied', async () => {
    await seedMembership('tenant-a', 'member-a', { status: 'revoked' });
    await seedDocument('tenants/tenant-a/partCatalog/part-1');

    const db = testEnvironment.authenticatedContext('member-a').firestore();
    await assertFails(
      getDoc(doc(db, 'tenants/tenant-a/partCatalog/part-1'))
    );
  });

  test('an expired membership is denied', async () => {
    await seedMembership('tenant-a', 'member-a', {
      validTo: Timestamp.fromMillis(Date.now() - 1_000)
    });
    await seedDocument('tenants/tenant-a/partCatalog/part-1');

    const db = testEnvironment.authenticatedContext('member-a').firestore();
    await assertFails(
      getDoc(doc(db, 'tenants/tenant-a/partCatalog/part-1'))
    );
  });

  test('a membership that is not yet valid is denied', async () => {
    await seedMembership('tenant-a', 'member-a', {
      validFrom: Timestamp.fromMillis(Date.now() + 60_000)
    });
    await seedDocument('tenants/tenant-a/partCatalog/part-1');

    const db = testEnvironment.authenticatedContext('member-a').firestore();
    await assertFails(
      getDoc(doc(db, 'tenants/tenant-a/partCatalog/part-1'))
    );
  });

  test('a scalar role cannot replace the canonical roles array', async () => {
    await seedMembership('tenant-a', 'member-a', { roles: 'beneficiary' });
    await seedDocument('tenants/tenant-a/partCatalog/part-1');

    const db = testEnvironment.authenticatedContext('member-a').firestore();
    await assertFails(
      getDoc(doc(db, 'tenants/tenant-a/partCatalog/part-1'))
    );
  });

  test('an unknown role cannot activate a membership', async () => {
    await seedMembership('tenant-a', 'member-a', { roles: ['owner'] });
    await seedDocument('tenants/tenant-a/partCatalog/part-1');

    const db = testEnvironment.authenticatedContext('member-a').firestore();
    await assertFails(
      getDoc(doc(db, 'tenants/tenant-a/partCatalog/part-1'))
    );
  });

  test('a suspended tenant invalidates an otherwise active membership', async () => {
    await seedMembership('tenant-a', 'member-a', {
      tenantStatus: 'suspended'
    });
    await seedDocument('tenants/tenant-a/partCatalog/part-1');

    const db = testEnvironment.authenticatedContext('member-a').firestore();
    await assertFails(
      getDoc(doc(db, 'tenants/tenant-a/partCatalog/part-1'))
    );
  });

  test('a tenant document with a mismatched tenant_id is denied', async () => {
    await seedMembership('tenant-a', 'member-a', {
      tenantDocumentTenantId: 'tenant-b'
    });
    await seedDocument('tenants/tenant-a/partCatalog/part-1');

    const db = testEnvironment.authenticatedContext('member-a').firestore();
    await assertFails(
      getDoc(doc(db, 'tenants/tenant-a/partCatalog/part-1'))
    );
  });

  test('a member may not cross the tenant boundary', async () => {
    await seedMembership('tenant-a', 'member-a');
    await seedDocument('tenants/tenant-b', {
      tenant_id: 'tenant-b',
      status: 'active'
    });
    await seedDocument('tenants/tenant-b/partCatalog/part-1', {
      tenant_id: 'tenant-b',
      part_id: 'part-1'
    });

    const db = testEnvironment.authenticatedContext('member-a').firestore();
    await assertFails(
      getDoc(doc(db, 'tenants/tenant-b/partCatalog/part-1'))
    );
  });

  test('unauthenticated access is denied', async () => {
    await seedDocument('tenants/tenant-a', {
      tenant_id: 'tenant-a',
      status: 'active'
    });
    await seedDocument('tenants/tenant-a/partCatalog/part-1');
    const db = testEnvironment.unauthenticatedContext().firestore();

    await assertFails(
      getDoc(doc(db, 'tenants/tenant-a/partCatalog/part-1'))
    );
  });
});

describe('least-privilege client read boundary', () => {
  test('a beneficiary may get and query only records related to its own person_id', async () => {
    await seedMembership('tenant-a', 'member-a', {
      roles: ['beneficiary'],
      personId: 'person-a'
    });

    const personScopedDocuments = [
      ['people/person-a', { person_id: 'person-a' }],
      ['people/person-b', { person_id: 'person-b' }],
      [
        'personRelationships/relationship-from',
        { from_person_id: 'person-a', to_person_id: 'person-b' }
      ],
      [
        'personRelationships/relationship-to',
        { from_person_id: 'person-b', to_person_id: 'person-a' }
      ],
      [
        'personRelationships/relationship-other',
        { from_person_id: 'person-b', to_person_id: 'person-c' }
      ],
      ['deviceAssignments/assignment-own', { person_id: 'person-a' }],
      ['deviceAssignments/assignment-other', { person_id: 'person-b' }],
      ['trips/trip-own', { person_id: 'person-a' }],
      ['trips/trip-other', { person_id: 'person-b' }],
      ['consentRevisions/consent-own', { person_id: 'person-a' }],
      ['consentRevisions/consent-other', { person_id: 'person-b' }],
      ['alerts/alert-own', { person_id: 'person-a' }],
      ['alerts/alert-other', { person_id: 'person-b' }]
    ];
    for (const [path, data] of personScopedDocuments) {
      await seedDocument(`tenants/tenant-a/${path}`, {
        tenant_id: 'tenant-a',
        ...data
      });
    }

    const db = testEnvironment.authenticatedContext('member-a').firestore();
    const allowedPaths = [
      'people/person-a',
      'personRelationships/relationship-from',
      'personRelationships/relationship-to',
      'deviceAssignments/assignment-own',
      'trips/trip-own',
      'consentRevisions/consent-own',
      'alerts/alert-own'
    ];
    const deniedPaths = [
      'people/person-b',
      'personRelationships/relationship-other',
      'deviceAssignments/assignment-other',
      'trips/trip-other',
      'consentRevisions/consent-other',
      'alerts/alert-other'
    ];

    for (const path of allowedPaths) {
      await assertSucceeds(getDoc(doc(db, `tenants/tenant-a/${path}`)));
    }
    for (const path of deniedPaths) {
      await assertFails(getDoc(doc(db, `tenants/tenant-a/${path}`)));
    }

    await assertSucceeds(
      getDocs(
        query(
          collection(db, 'tenants/tenant-a/trips'),
          where('person_id', '==', 'person-a'),
          where('tenant_id', '==', 'tenant-a')
        )
      )
    );
    await assertFails(
      getDocs(
        query(
          collection(db, 'tenants/tenant-a/trips'),
          where('person_id', '==', 'person-b'),
          where('tenant_id', '==', 'tenant-a')
        )
      )
    );
    await assertFails(getDocs(collection(db, 'tenants/tenant-a/trips')));
    await assertFails(
      getDocs(collection(db, 'tenants/tenant-a/people'))
    );
  });

  test('a beneficiary cannot directly read device or operational projections', async () => {
    await seedMembership('tenant-a', 'member-a', {
      roles: ['beneficiary']
    });
    const operationalPaths = [
      'devices/device-1',
      'devices/device-1/state/current',
      'componentInstallations/component-1',
      'repairs/repair-1',
      'repairs/repair-1/items/item-1',
      'inspections/inspection-1',
      'inspections/inspection-1/observations/observation-1',
      'modelPredictions/prediction-1',
      'evidenceFacts/fact-1',
      'reportRuns/report-1',
      'reportRuns/report-1/claims/claim-1'
    ];
    for (const path of operationalPaths) {
      await seedDocument(`tenants/tenant-a/${path}`, {
        tenant_id: 'tenant-a'
      });
    }

    const db = testEnvironment.authenticatedContext('member-a').firestore();
    for (const path of operationalPaths) {
      await assertFails(getDoc(doc(db, `tenants/tenant-a/${path}`)));
    }
  });

  test('path and stored tenant or person identifiers must agree', async () => {
    await seedMembership('tenant-a', 'member-a', {
      roles: ['beneficiary'],
      personId: 'person-a'
    });
    await seedMembership('tenant-a', 'case-worker', {
      roles: ['case_worker'],
      personId: 'worker-person'
    });
    await seedDocument('tenants/tenant-a/trips/trip-wrong-tenant', {
      tenant_id: 'tenant-b',
      person_id: 'person-a'
    });
    await seedDocument('tenants/tenant-a/people/person-a', {
      tenant_id: 'tenant-a',
      person_id: 'person-b'
    });
    await seedDocument('tenants/tenant-a/devices/device-wrong-tenant', {
      tenant_id: 'tenant-b'
    });

    const beneficiaryDb = testEnvironment
      .authenticatedContext('member-a')
      .firestore();
    await assertFails(
      getDoc(
        doc(beneficiaryDb, 'tenants/tenant-a/trips/trip-wrong-tenant')
      )
    );
    await assertFails(
      getDoc(doc(beneficiaryDb, 'tenants/tenant-a/people/person-a'))
    );

    const staffDb = testEnvironment
      .authenticatedContext('case-worker')
      .firestore();
    await assertFails(
      getDoc(
        doc(staffDb, 'tenants/tenant-a/devices/device-wrong-tenant')
      )
    );
  });

  test('a case worker may read tenant-wide person and operational projections', async () => {
    await seedMembership('tenant-a', 'case-worker', {
      roles: ['case_worker'],
      personId: 'worker-person'
    });
    const readablePaths = [
      ['people/person-b', { person_id: 'person-b' }],
      ['trips/trip-b', { person_id: 'person-b' }],
      ['devices/device-1', {}],
      ['devices/device-1/state/current', {}],
      ['componentInstallations/component-1', {}],
      ['repairs/repair-1', {}],
      ['repairs/repair-1/items/item-1', {}],
      ['inspections/inspection-1', {}],
      ['inspections/inspection-1/observations/observation-1', {}],
      ['alerts/alert-b', { person_id: 'person-b' }],
      ['alerts/alert-b/deliveries/delivery-1', {}],
      ['modelPredictions/prediction-1', {}],
      ['evidenceFacts/fact-1', {}],
      ['reportRuns/report-1', {}],
      ['reportRuns/report-1/claims/claim-1', {}]
    ];
    for (const [path, data] of readablePaths) {
      await seedDocument(`tenants/tenant-a/${path}`, {
        tenant_id: 'tenant-a',
        ...data
      });
    }

    const db = testEnvironment.authenticatedContext('case-worker').firestore();
    for (const [path] of readablePaths) {
      await assertSucceeds(getDoc(doc(db, `tenants/tenant-a/${path}`)));
    }
    await assertSucceeds(
      getDocs(
        query(
          collection(db, 'tenants/tenant-a/devices'),
          where('tenant_id', '==', 'tenant-a')
        )
      )
    );
    await assertSucceeds(
      getDocs(
        query(
          collection(db, 'tenants/tenant-a/trips'),
          where('tenant_id', '==', 'tenant-a')
        )
      )
    );
  });

  test.each(['guardian', 'repairer', 'auditor'])(
    '%s may read own person records but not other-person or operational records',
    async (role) => {
      const uid = `${role}-member`;
      const personId = `${role}-person`;
      await seedMembership('tenant-a', uid, {
        roles: [role],
        personId
      });
      await seedDocument('tenants/tenant-a/trips/trip-own', {
        tenant_id: 'tenant-a',
        person_id: personId
      });
      await seedDocument('tenants/tenant-a/trips/trip-other', {
        tenant_id: 'tenant-a',
        person_id: 'person-other'
      });
      await seedDocument('tenants/tenant-a/devices/device-1', {
        tenant_id: 'tenant-a'
      });

      const db = testEnvironment.authenticatedContext(uid).firestore();
      await assertSucceeds(
        getDoc(doc(db, 'tenants/tenant-a/trips/trip-own'))
      );
      await assertFails(
        getDoc(doc(db, 'tenants/tenant-a/trips/trip-other'))
      );
      await assertFails(
        getDoc(doc(db, 'tenants/tenant-a/devices/device-1'))
      );
    }
  );
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
      `tenants/tenant-a/consentStates/${consentStateDocumentId}`,
      'tenants/tenant-a/trips/trip-1',
      'tenants/tenant-a/ingestReceipts/batch-1',
      'tenants/tenant-a/ingestCleanupTargets/cleanup-1',
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
      person_id: 'person-a',
      raw_sample_count: 0
    });
    const db = testEnvironment.authenticatedContext('member-a').firestore();

    await assertSucceeds(getDoc(doc(db, 'tenants/tenant-a/trips/trip-1')));
    await assertFails(setDoc(doc(db, 'tenants/tenant-a/trips/trip-2'), {
      tenant_id: 'tenant-a',
      trip_id: 'trip-2'
    }));
  });

  test('current consent states deny direct client reads', async () => {
    await seedMembership('tenant-a', 'member-a');
    await seedDocument(
      `tenants/tenant-a/consentStates/${consentStateDocumentId}`,
      {
        tenant_id: 'tenant-a',
        person_id: 'person-1',
        purpose_code: 'precise_location',
        status: 'granted'
      }
    );
    const db = testEnvironment.authenticatedContext('member-a').firestore();

    await assertFails(
      getDoc(
        doc(
          db,
          `tenants/tenant-a/consentStates/${consentStateDocumentId}`
        )
      )
    );
  });

  test('ingest internals deny client reads, lists, and writes', async () => {
    await seedMembership('tenant-a', 'member-a');
    await seedDocument('tenants/tenant-a/ingestReceipts/batch-1', {
      tenant_id: 'tenant-a',
      body_hash: 'sha256:receipt'
    });
    await seedDocument(
      'tenants/tenant-a/ingestReceipts/batch-1/recoveryAttempts/attempt-1',
      {
        tenant_id: 'tenant-a',
        receipt_id: 'batch-1',
        attempt_id: 'attempt-1'
      }
    );
    await seedDocument(
      'tenants/tenant-a/ingestReceipts/batch-1/purgeLinks/link-1',
      {
        tenant_id: 'tenant-a',
        receipt_id: 'batch-1',
        link_id: 'link-1'
      }
    );
    await seedDocument('tenants/tenant-a/ingestIdempotency/key-1', {
      tenant_id: 'tenant-a',
      body_hash: 'sha256:idempotency'
    });
    await seedDocument('tenants/tenant-a/ingestCleanupTargets/cleanup-1', {
      tenant_id: 'tenant-a',
      receipt_id: 'batch-1',
      cleanup_id: 'cleanup-1'
    });
    await seedDocument('tenants/tenant-a/ingestIntegrityFindings/finding-1', {
      tenant_id: 'tenant-a',
      receipt_id: 'batch-1',
      finding_id: 'finding-1'
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
      getDoc(
        doc(
          db,
          'tenants/tenant-a/ingestReceipts/batch-1/recoveryAttempts/attempt-1'
        )
      )
    );
    await assertFails(
      getDoc(
        doc(
          db,
          'tenants/tenant-a/ingestReceipts/batch-1/purgeLinks/link-1'
        )
      )
    );
    await assertFails(
      getDocs(
        collection(
          db,
          'tenants/tenant-a/ingestReceipts/batch-1/purgeLinks'
        )
      )
    );
    await assertFails(
      getDoc(doc(db, 'tenants/tenant-a/ingestIdempotency/key-1'))
    );
    await assertFails(
      getDoc(doc(db, 'tenants/tenant-a/ingestCleanupTargets/cleanup-1'))
    );
    await assertFails(
      getDoc(doc(db, 'tenants/tenant-a/ingestIntegrityFindings/finding-1'))
    );
    await assertFails(
      getDocs(collection(db, 'tenants/tenant-a/ingestIntegrityFindings'))
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
      setDoc(
        doc(
          db,
          'tenants/tenant-a/ingestReceipts/batch-1/recoveryAttempts/attempt-1'
        ),
        {
          tenant_id: 'tenant-a',
          receipt_id: 'batch-1',
          attempt_id: 'attempt-1'
        }
      )
    );
    await assertFails(
      setDoc(
        doc(
          db,
          'tenants/tenant-a/ingestReceipts/batch-1/purgeLinks/link-2'
        ),
        {
          tenant_id: 'tenant-a',
          receipt_id: 'batch-1',
          link_id: 'link-2'
        }
      )
    );
    await assertFails(
      setDoc(doc(db, 'tenants/tenant-a/ingestIdempotency/key-2'), {
        tenant_id: 'tenant-a'
      })
    );
    await assertFails(
      setDoc(doc(db, 'tenants/tenant-a/ingestCleanupTargets/cleanup-2'), {
        tenant_id: 'tenant-a',
        receipt_id: 'batch-1'
      })
    );
    await assertFails(
      setDoc(doc(db, 'tenants/tenant-a/ingestIntegrityFindings/finding-2'), {
        tenant_id: 'tenant-a',
        receipt_id: 'batch-1'
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
