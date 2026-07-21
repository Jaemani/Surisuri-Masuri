# Firebase security rules

This package tests the local, deny-by-default Firebase security boundary. It is
not connected to a real Firebase project and must be run with the demo project
ID `demo-mobility-reliability`.

## Boundary

- An active document at `/tenants/{tenantId}/memberships/{uid}` establishes tenant
  membership. Roles are read from that document, not trusted from client data.
- Active tenant members may read tenant-owned product data.
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
