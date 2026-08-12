import {
  demoDevice,
  demoRepairJobs,
  demoRepairRequest,
  demoRepairerRoleSession,
  demoSubsidy,
  demoUserRoleSession,
} from './data';
import type {
  CreateRepairRequestInput,
  ProductRole,
  ProductSnapshot,
  RepairWorkOrder,
  RoleSession,
} from './types';

export type ProductRepositoryErrorCode = 'NOT_CONFIGURED';

export class ProductRepositoryError extends Error {
  readonly code: ProductRepositoryErrorCode;

  constructor(code: ProductRepositoryErrorCode, message: string) {
    super(message);
    this.name = 'ProductRepositoryError';
    this.code = code;
  }
}

export interface ProductRepository {
  getSnapshot(): Promise<ProductSnapshot>;
  createRepairRequest(input: CreateRepairRequestInput): Promise<RepairWorkOrder>;
  setRole(role: ProductRole): Promise<RoleSession>;
}

function copySnapshot(snapshot: ProductSnapshot): ProductSnapshot {
  return {
    ...snapshot,
    roleSession: { ...snapshot.roleSession },
    repairRequest: snapshot.repairRequest ? { ...snapshot.repairRequest } : null,
    device: { ...snapshot.device, timeline: snapshot.device.timeline.map((item) => ({ ...item })) },
    subsidy: { ...snapshot.subsidy },
    repairJobs: snapshot.repairJobs.map((job) => ({ ...job })),
  };
}

export const demoProductSnapshot: ProductSnapshot = {
  roleSession: demoUserRoleSession,
  repairRequest: demoRepairRequest,
  device: demoDevice,
  subsidy: demoSubsidy,
  repairJobs: demoRepairJobs,
};

/**
 * Deterministic local adapter used by the product prototype.
 *
 * It is intentionally in-memory: commands exercise the same async boundary
 * as a network repository without pretending that demo data is persisted.
 */
export class DemoProductRepository implements ProductRepository {
  private snapshot: ProductSnapshot;

  constructor(seed: ProductSnapshot = demoProductSnapshot) {
    this.snapshot = copySnapshot(seed);
  }

  async getSnapshot(): Promise<ProductSnapshot> {
    return copySnapshot(this.snapshot);
  }

  async createRepairRequest(input: CreateRepairRequestInput): Promise<RepairWorkOrder> {
    const request: RepairWorkOrder = {
      id: 'demo-request-new',
      title: input.title,
      createdAt: '2026년 8월 13일',
      status: 'received',
      repairer: '가까운 수리센터를 찾는 중',
      visitAt: '센터 배정 후 안내해 드려요',
    };
    this.snapshot = { ...this.snapshot, repairRequest: request };
    return { ...request };
  }

  async setRole(role: ProductRole): Promise<RoleSession> {
    const roleSession = role === 'repairer' ? demoRepairerRoleSession : demoUserRoleSession;
    this.snapshot = { ...this.snapshot, roleSession: { ...roleSession } };
    return { ...roleSession };
  }
}

/**
 * Production adapter boundary. The mobile client must call domain-command
 * endpoints rather than writing Firestore documents directly.
 *
 * Required backend contracts before enabling this adapter:
 * - `GET /v1/mobile/product-snapshot` — authorized role-scoped device,
 *   repair-work-order, subsidy, and repair-job projection.
 * - `POST /v1/mobile/repair-work-orders` — creates a repair request after
 *   validating principal, device assignment, consent, and tenant membership.
 * - `POST /v1/mobile/role-session` — switches an already-authorized demo or
 *   production role; the server returns the resulting role session.
 *
 * No direct Firestore writes belong in this repository. Authentication,
 * App Check, tenant authorization, and event creation stay server-side.
 */
export class FirebaseProductRepository implements ProductRepository {
  private async notConfigured<T>(): Promise<T> {
    throw new ProductRepositoryError(
      'NOT_CONFIGURED',
      'Firebase product repository is not configured; use a domain-command API adapter.',
    );
  }

  getSnapshot(): Promise<ProductSnapshot> {
    return this.notConfigured<ProductSnapshot>();
  }

  createRepairRequest(_input: CreateRepairRequestInput): Promise<RepairWorkOrder> {
    return this.notConfigured<RepairWorkOrder>();
  }

  setRole(_role: ProductRole): Promise<RoleSession> {
    return this.notConfigured<RoleSession>();
  }
}

export type ProductRepositorySource = 'demo' | 'firebase';

export function createProductRepository(source: ProductRepositorySource = 'demo'): ProductRepository {
  return source === 'firebase' ? new FirebaseProductRepository() : new DemoProductRepository();
}
