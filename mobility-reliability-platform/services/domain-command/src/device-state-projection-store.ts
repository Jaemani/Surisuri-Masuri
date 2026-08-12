import { Timestamp, getFirestore, type DocumentData, type Firestore, type QueryDocumentSnapshot } from 'firebase-admin/firestore';
import { bodyHash, deterministicId, DomainCommandError, safeId } from './canonical.js';
import { DEVICE_STATE_PROJECTOR_VERSION, projectDeviceCurrentState, type DeviceCurrentState, type NormalizedDeviceEvent } from './device-state-projector.js';

const MAX_SOURCE_EVENTS = 10_000;
const PROJECTOR_NAME = 'device-current-state';

export type DeviceStateReplayRequest = { tenantId: string; deviceId: string; replayRunId: string; asOf?: string };
export type DeviceStateReplayResult = { status: 'prepared' | 'promoted' | 'replayed'; shadowId: string; revision: number; state: DeviceCurrentState; inputHash: string };

type ShadowBinding = {
  tenant_id: string;
  device_id: string;
  replay_run_id: string;
  projector_name: typeof PROJECTOR_NAME;
  projector_version: typeof DEVICE_STATE_PROJECTOR_VERSION;
  input_hash: string;
  output_checksum: string;
  state_hash: string;
  event_count: number;
  expected_current_revision: number;
  as_of: string | null;
  state: DeviceCurrentState;
};

export class FirestoreDeviceStateProjectionStore {
  constructor(private readonly db: Firestore = getFirestore()) {}

  async rebuild(request: DeviceStateReplayRequest): Promise<DeviceStateReplayResult> {
    const prepared = await this.prepare(request);
    if (prepared.status === 'replayed') return prepared;
    return this.promote(request, prepared.shadowId);
  }

  async prepare(request: DeviceStateReplayRequest): Promise<DeviceStateReplayResult> {
    const scope = this.scope(request);
    const deviceRef = this.deviceRef(scope.tenantId, scope.deviceId);
    const shadowId = deterministicId('device_state', scope.replayRunId);
    const shadowRef = deviceRef.collection('stateVersions').doc(shadowId);
    const currentRef = deviceRef.collection('state').doc('current');
    const [device, current, source] = await Promise.all([deviceRef.get(), currentRef.get(), this.sourceEvents(scope.tenantId, scope.deviceId)]);
    if (!device.exists || device.data()?.tenant_id !== scope.tenantId || device.data()?.device_id !== scope.deviceId) throw new DomainCommandError('DEVICE_NOT_FOUND', 'Device is outside the replay scope.', 404);
    const events = source.map((document) => this.decodeEvent(document.data()));
    const state = projectDeviceCurrentState({ tenantId: scope.tenantId, deviceId: scope.deviceId, events, ...(scope.asOf ? { asOf: scope.asOf } : {}) });
    const inputHash = bodyHash(events);
    const expectedCurrentRevision = current.exists ? this.positiveOrZero(current.data()?.revision, 'CORRUPT_CURRENT_STATE') : 0;
    const binding: ShadowBinding = {
      tenant_id: scope.tenantId,
      device_id: scope.deviceId,
      replay_run_id: scope.replayRunId,
      projector_name: PROJECTOR_NAME,
      projector_version: DEVICE_STATE_PROJECTOR_VERSION,
      input_hash: inputHash,
      output_checksum: state.canonicalChecksum,
      state_hash: bodyHash(state),
      event_count: state.checkpoint.eventCount,
      expected_current_revision: expectedCurrentRevision,
      as_of: state.checkpoint.asOf,
      state,
    };
    const existing = await shadowRef.get();
    if (existing.exists) {
      const originalExpectedRevision = this.positiveOrZero(existing.data()?.expected_current_revision, 'CORRUPT_SHADOW');
      this.assertBinding(existing.data(), { ...binding, expected_current_revision: originalExpectedRevision }, 'SHADOW_BINDING_CONFLICT');
      return { status: 'prepared', shadowId, revision: expectedCurrentRevision, state, inputHash };
    }
    try {
      await shadowRef.create({ ...binding, created_at: Timestamp.now() });
    } catch (error) {
      const raced = await shadowRef.get();
      if (!raced.exists) throw error;
      this.assertBinding(raced.data(), binding, 'SHADOW_BINDING_CONFLICT');
    }
    return { status: 'prepared', shadowId, revision: expectedCurrentRevision, state, inputHash };
  }

  async promote(request: DeviceStateReplayRequest, shadowId: string): Promise<DeviceStateReplayResult> {
    const scope = this.scope(request);
    if (shadowId !== deterministicId('device_state', scope.replayRunId)) throw new DomainCommandError('SHADOW_BINDING_CONFLICT', 'Shadow identity does not match the replay run.', 409);
    const deviceRef = this.deviceRef(scope.tenantId, scope.deviceId);
    const shadowRef = deviceRef.collection('stateVersions').doc(shadowId);
    const currentRef = deviceRef.collection('state').doc('current');
    const checkpointRef = this.tenantRef(scope.tenantId).collection('projectionCheckpoints').doc(`${PROJECTOR_NAME}--${scope.deviceId}`);
    return this.db.runTransaction(async (tx) => {
      const [device, shadow, current, checkpoint, source] = await Promise.all([
        tx.get(deviceRef), tx.get(shadowRef), tx.get(currentRef), tx.get(checkpointRef), tx.get(this.sourceQuery(scope.tenantId, scope.deviceId)),
      ]);
      if (!device.exists || device.data()?.tenant_id !== scope.tenantId || device.data()?.device_id !== scope.deviceId) throw new DomainCommandError('DEVICE_NOT_FOUND', 'Device is outside the replay scope.', 404);
      if (!shadow.exists) throw new DomainCommandError('SHADOW_NOT_FOUND', 'Prepared state was not found.', 404);
      const events = source.docs.map((document) => this.decodeEvent(document.data()));
      const state = projectDeviceCurrentState({ tenantId: scope.tenantId, deviceId: scope.deviceId, events, ...(scope.asOf ? { asOf: scope.asOf } : {}) });
      const inputHash = bodyHash(events);
      const expectedRevision = current.exists ? this.positiveOrZero(current.data()?.revision, 'CORRUPT_CURRENT_STATE') : 0;
      const binding: ShadowBinding = {
        tenant_id: scope.tenantId, device_id: scope.deviceId, replay_run_id: scope.replayRunId,
        projector_name: PROJECTOR_NAME, projector_version: DEVICE_STATE_PROJECTOR_VERSION,
        input_hash: inputHash, output_checksum: state.canonicalChecksum, state_hash: bodyHash(state),
        event_count: state.checkpoint.eventCount,
        expected_current_revision: this.positiveOrZero(shadow.data()?.expected_current_revision, 'CORRUPT_SHADOW'),
        as_of: state.checkpoint.asOf, state,
      };
      this.assertBinding(shadow.data(), binding, 'CORRUPT_SHADOW');
      if (current.exists && this.isPromoted(current.data(), binding)) {
        if (!checkpoint.exists || !this.isPromoted(checkpoint.data(), binding) || checkpoint.data()?.revision !== current.data()?.revision) throw new DomainCommandError('CHECKPOINT_DRIFT', 'Current state and checkpoint disagree.', 500);
        return { status: 'replayed', shadowId, revision: expectedRevision, state, inputHash };
      }
      if (expectedRevision !== binding.expected_current_revision) throw new DomainCommandError('STALE_CURRENT_REVISION', 'Current state changed after shadow preparation.', 409);
      if (checkpoint.exists && checkpoint.data()?.revision !== expectedRevision) throw new DomainCommandError('CHECKPOINT_DRIFT', 'Checkpoint does not match current state revision.', 500);
      const revision = expectedRevision + 1;
      const promoted = { ...binding, revision, shadow_id: shadowId, promoted_at: Timestamp.now() };
      tx.set(currentRef, promoted);
      tx.set(checkpointRef, { ...promoted, state: null });
      return { status: 'promoted', shadowId, revision, state, inputHash };
    });
  }

  private isPromoted(data: DocumentData | undefined, binding: ShadowBinding): boolean {
    return Boolean(data && data.tenant_id === binding.tenant_id && data.device_id === binding.device_id && data.replay_run_id === binding.replay_run_id && data.projector_version === binding.projector_version && data.input_hash === binding.input_hash && data.output_checksum === binding.output_checksum);
  }

  private assertBinding(data: DocumentData | undefined, expected: ShadowBinding, code: string): void {
    if (!data || data.tenant_id !== expected.tenant_id || data.device_id !== expected.device_id || data.replay_run_id !== expected.replay_run_id || data.projector_name !== expected.projector_name || data.projector_version !== expected.projector_version || data.input_hash !== expected.input_hash || data.output_checksum !== expected.output_checksum || data.state_hash !== expected.state_hash || data.event_count !== expected.event_count || data.expected_current_revision !== expected.expected_current_revision || data.as_of !== expected.as_of || bodyHash(data.state) !== expected.state_hash) throw new DomainCommandError(code, 'Projection binding is corrupt or conflicts with the replay.', code.includes('CONFLICT') ? 409 : 500);
  }

  private scope(request: DeviceStateReplayRequest) {
    const tenantId = safeId(request.tenantId, 'tenantId');
    const deviceId = safeId(request.deviceId, 'deviceId');
    const replayRunId = safeId(request.replayRunId, 'replayRunId');
    return { tenantId, deviceId, replayRunId, ...(request.asOf ? { asOf: request.asOf } : {}) };
  }

  private async sourceEvents(tenantId: string, deviceId: string): Promise<QueryDocumentSnapshot[]> {
    const snapshot = await this.sourceQuery(tenantId, deviceId).get();
    if (snapshot.size > MAX_SOURCE_EVENTS) throw new DomainCommandError('DEVICE_STATE_EVENT_LIMIT_EXCEEDED', 'Device event replay exceeds the bounded limit.', 409);
    return snapshot.docs;
  }
  private sourceQuery(tenantId: string, deviceId: string) { return this.tenantRef(tenantId).collection('deviceStateEvents').where('device_id', '==', deviceId).limit(MAX_SOURCE_EVENTS + 1); }
  private decodeEvent(data: DocumentData): NormalizedDeviceEvent {
    const occurredAt = this.iso(data.occurred_at, 'CORRUPT_DEVICE_STATE_EVENT');
    const recordedAt = this.iso(data.recorded_at, 'CORRUPT_DEVICE_STATE_EVENT');
    const payload = data.payload;
    if (!payload || typeof payload !== 'object' || Array.isArray(payload)) throw new DomainCommandError('CORRUPT_DEVICE_STATE_EVENT', 'Device state event payload is invalid.', 500);
    const common = { schemaVersion: data.schema_version, eventId: data.event_id, eventType: data.event_type, tenantId: data.tenant_id, deviceId: data.device_id, occurredAt, recordedAt, sourceQuality: data.source_quality };
    if (data.event_type === 'repair.recorded') return { ...common, payload: { repairId: payload.repair_id } } as NormalizedDeviceEvent;
    if (data.event_type === 'part.replaced') return { ...common, payload: { repairId: payload.repair_id, componentId: payload.component_id, category: payload.category, action: payload.action } } as NormalizedDeviceEvent;
    if (data.event_type === 'inspection.completed') return { ...common, payload: { inspectionId: payload.inspection_id, outcome: payload.outcome } } as NormalizedDeviceEvent;
    if (data.event_type === 'trip.summarized') return { ...common, payload: { sessionId: payload.session_id, distanceMeters: payload.distance_meters } } as NormalizedDeviceEvent;
    return { ...common, payload } as NormalizedDeviceEvent;
  }
  private iso(value: unknown, code: string): string { if (value instanceof Timestamp) return value.toDate().toISOString(); if (typeof value === 'string') return value; throw new DomainCommandError(code, 'Event time is invalid.', 500); }
  private positiveOrZero(value: unknown, code: string): number { if (!Number.isSafeInteger(value) || (value as number) < 0) throw new DomainCommandError(code, 'Projection revision is invalid.', 500); return value as number; }
  private tenantRef(tenantId: string) { return this.db.collection('tenants').doc(tenantId); }
  private deviceRef(tenantId: string, deviceId: string) { return this.tenantRef(tenantId).collection('devices').doc(deviceId); }
}
