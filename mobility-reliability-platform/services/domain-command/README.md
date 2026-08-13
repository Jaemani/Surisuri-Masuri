# Domain Command service

Firebase-first command boundary for repair operations. Clients never write repair, event, or subsidy documents directly.

Exports three Firebase Functions v2 HTTP endpoints:

- `createRepairRequest` — creates a `requested` work order.
- `transitionRepairRequest` — performs an optimistic-concurrency transition using `expectedRevision`.
- `appendSubsidyTransaction` — appends an immutable reservation/execution/release/allocation ledger entry.

Every request requires:

- `Authorization: Bearer <Firebase ID token>`
- `X-Firebase-AppCheck: <App Check token>`
- `Idempotency-Key: <stable command key>`

The request may carry a tenant candidate as `tenantId`, but the service derives the effective tenant and role from the server-side membership document. It rejects inactive tenants, memberships outside `valid_from`/`valid_to`, mismatched Firebase UIDs, unknown roles, and disallowed App Check app IDs. The candidate is never trusted as authorization.

Firestore storage follows the accepted product model:

- `tenants/{tenantId}/repairWorkOrders/{workOrderId}` with append-only `statusHistory`
- `tenants/{tenantId}/subsidyAccounts/{accountId}` with immutable `transactions`
- `tenants/{tenantId}/domainEvents/{eventId}`
- server-only `commandIdempotency` receipts

Wire commands are camelCase; Firestore documents are encoded explicitly as snake_case. Beneficiary requests require an active device assignment, guardian requests require an active person relationship, repairers may transition only their assigned work, and subsidy transactions must match one person, policy, account, and publicly funded work order.

`InMemoryCommandStore` and the pure `DomainCommandKernel` provide deterministic unit tests. `pnpm test:emulator` verifies the real Firestore adapter, including canonical paths, replay/conflict, optimistic concurrency, assignment enforcement, and the person-scoped subsidy ledger.

Subsidy `execution` is accepted only after a work order reaches `center_verified` or `completed`; repairer submission alone is not authorization. The operator repair projection exposes a validated, purpose-limited subsidy command context only when tenant, beneficiary, account, and policy linkage resolve without ambiguity. Its `executed` state is derived from an immutable execution transaction, not from the repair status. Ledger rows preserve their unique transaction ID and typed transaction kind.

This repository does not claim production deployment. Firebase project registration, App Check rollout, institution policy configuration, cross-organization repair-station grants, and field pilot evidence remain deployment gates.
