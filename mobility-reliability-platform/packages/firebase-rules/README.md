# Firebase security rules

This package tests the local, deny-by-default Firebase security boundary. It is
not connected to a real Firebase project and must be run with the demo project
ID `demo-mobility-reliability`.

## Boundary

- A tenant must have a matching `tenant_id` and `status: active`, and an active
  document at `/tenants/{tenantId}/memberships/{uid}` establishes membership.
  Roles and the optional person relationship are read from that document, not
  trusted from client data.
- Members may get their own membership but cannot list memberships. Tenant
  documents may be fetched by active members but cannot be listed.
- `case_worker` and `tenant_admin` are the only operational staff roles. They
  may read tenant operational projections. `beneficiary`, `guardian`,
  `repairer`, and `auditor` do not gain operational access from their role.
- Non-operational members may read only records associated with their own
  membership `person_id` in people, relationships, assignments, trips,
  consent revisions, and alerts. The part catalog remains readable by every
  active member.
- Tenant-owned top-level documents must repeat the path tenant in `tenant_id`.
  People are get-only. Other allowed collection queries must constrain
  `tenant_id`, and person-scoped queries must also constrain `person_id`;
  unfiltered tenant-wide queries fail.
- Stage 1 clients cannot directly create, update, or delete domain documents,
  including repairs and inspections. Mutations go through authenticated backend
  commands.
- Tenant and membership documents, app installations, current consent states,
  ingest receipts, device-state projections, reports, and devices are written
  by trusted server code only. Firebase Admin SDK calls bypass Firestore client
  rules.
- Cloud Storage denies every client read and write. Raw telemetry is written by
  the Cloud Run service account through trusted server credentials; bucket IAM
  remains a deployment responsibility.

Unknown paths, document deletion, unexpected fields, and cross-tenant access
are denied.

## Local verification

From the repository root:

```sh
pnpm check:firebase
```

The script starts only local Firestore and Storage emulators, runs the rules
suite, and then shuts the emulators down. It does not authenticate, deploy, or
contact a production Firebase project.
