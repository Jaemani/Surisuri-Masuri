import { Timestamp, getFirestore, type DocumentData, type Firestore, type QueryDocumentSnapshot } from 'firebase-admin/firestore';
import { DomainCommandError } from './canonical.js';
import type { ConsoleProjectionName, MobileProductSnapshot, ProductProjectionStore } from './projection-types.js';
import type { ActorContext, RepairStatus } from './types.js';

const MAX_PROJECTION_DOCUMENTS = 200;
const MAX_LEDGER_TRANSACTIONS = 200;
const activeRepairStatuses = new Set<RepairStatus>(['requested', 'under_review', 'assigned', 'scheduled', 'in_progress', 'repairer_submitted', 'needs_correction', 'center_verified', 'reopened']);
const terminalRepairStatuses = new Set<RepairStatus>(['completed', 'rejected', 'cancelled']);
const allRepairStatuses = new Set<RepairStatus>([...activeRepairStatuses, ...terminalRepairStatuses]);
const repairerVisibleStatuses = new Set<RepairStatus>(['assigned', 'scheduled', 'in_progress', 'repairer_submitted', 'needs_correction', 'center_verified']);

export class FirestoreProductProjectionStore implements ProductProjectionStore {
  constructor(private readonly db: Firestore = getFirestore()) {}

  async getMobileSnapshot(actor: ActorContext): Promise<MobileProductSnapshot> {
    const tenant = this.db.collection('tenants').doc(actor.tenantId);
    if (actor.roles.some(isOperationalRole) || actor.roles.includes('auditor')) throw new DomainCommandError('ROLE_FORBIDDEN', 'Institution operators and auditors must use their purpose-specific surface.', 403);
    if (actor.roles.includes('repairer') && actor.roles.some((role) => role === 'beneficiary' || role === 'guardian')) throw new DomainCommandError('AMBIGUOUS_MOBILE_AUDIENCE', 'A mobile session must resolve to exactly one product audience.', 403);
    if (actor.roles.includes('repairer')) return this.repairerSnapshot(actor, tenant);
    if (actor.roles.includes('guardian') && !actor.roles.includes('beneficiary') && !actor.roles.some(isOperationalRole)) {
      throw new DomainCommandError('GUARDIAN_TARGET_REQUIRED', 'A guardian view requires an explicitly authorized beneficiary target.', 403);
    }
    if (!actor.personId || !actor.roles.some((role) => role === 'beneficiary' || role === 'guardian' || isOperationalRole(role))) {
      throw new DomainCommandError('PERSON_SCOPE_REQUIRED', 'The mobile product view requires an authorized person scope.', 403);
    }

    const [person, assignments, workOrders, accounts] = await Promise.all([
      tenant.collection('people').doc(actor.personId).get(),
      this.bounded(tenant.collection('deviceAssignments').limit(MAX_PROJECTION_DOCUMENTS + 1).get(), 'device assignments', actor.tenantId),
      this.bounded(tenant.collection('repairWorkOrders').limit(MAX_PROJECTION_DOCUMENTS + 1).get(), 'repair work orders', actor.tenantId),
      this.bounded(tenant.collection('subsidyAccounts').limit(MAX_PROJECTION_DOCUMENTS + 1).get(), 'subsidy accounts', actor.tenantId),
    ]);
    this.assertTenantDoc(person.data(), actor.tenantId, 'PERSON_NOT_FOUND');
    const matchingAssignments = assignments.filter((doc) => doc.data().person_id === actor.personId && activeAt(doc.data()));
    if (matchingAssignments.length === 0) throw new DomainCommandError('DEVICE_ASSIGNMENT_NOT_FOUND', 'No active device assignment is available.', 404);
    if (matchingAssignments.length !== 1) throw new DomainCommandError('AMBIGUOUS_DEVICE_ASSIGNMENT', 'More than one active device assignment is available.', 409);
    const assignment = matchingAssignments[0]!;
    const deviceId = requiredString(assignment.data(), 'device_id', 'CORRUPT_DEVICE_ASSIGNMENT');
    const device = await tenant.collection('devices').doc(deviceId).get();
    this.assertTenantDoc(device.data(), actor.tenantId, 'DEVICE_NOT_FOUND');

    const ownOrders = workOrders.filter((doc) => doc.data().requester_person_id === actor.personId && doc.data().device_id === deviceId).sort(byUpdatedDesc);
    ownOrders.forEach(assertValidRepairDocument);
    const activeOrder = ownOrders.find((doc) => activeRepairStatuses.has(doc.data().status));
    const account = accounts.find((doc) => doc.data().person_id === actor.personId && doc.data().status !== 'closed');
    const displayName = `이용자 ${shortCode(actor.personId)}`;
    const accountData = account?.data();
    const allocated = safeMoney(accountData?.allocated_krw);
    const adjustment = safeSignedMoney(accountData?.adjustment_krw);
    const available = safeMoney(accountData?.available_krw);
    const used = Math.max(allocated + adjustment - available, 0);

    return {
      roleSession: { role: 'user', displayName: `${displayName} 님`, isDemo: false },
      repairRequest: activeOrder ? await this.mobileWorkOrder(tenant, activeOrder) : null,
      device: {
        id: device.id,
        name: deviceName(device.data()),
        registrationNumber: optionalString(device.data(), 'public_code') ?? '등록번호 미확인',
        registeredAt: yearLabel(device.data()?.commissioned_at ?? device.data()?.created_at, '등록일 미확인'),
        status: mobileDeviceStatus(device.data()?.status),
        timeline: this.mobileTimeline(ownOrders, device),
      },
      subsidy: {
        program: optionalString(accountData, 'program_name') ?? '전동보장구 수리 지원금',
        cycle: optionalString(accountData, 'cycle_label') ?? '현재 지원 주기',
        used,
        total: Math.max(allocated + adjustment, 0),
        nextReview: dateLabel(accountData?.next_review_at, '검토일 미정'),
        note: account ? '예약·집행 내역은 복지관 검증 후 반영돼요.' : '연결된 지원금 계정이 없어요.',
      },
    };
  }

  async getConsoleProjection(actor: ActorContext, projection: ConsoleProjectionName): Promise<unknown> {
    if (!actor.roles.some(isOperationalRole)) throw new DomainCommandError('ROLE_FORBIDDEN', 'Only institution operators may read the operations projection.', 403);
    const tenant = this.db.collection('tenants').doc(actor.tenantId);
    if (projection === 'services') return [{ name: '수리 상태 projection', detail: '서버 인증·권한 기반 목적 제한 조회', value: '연결됨', status: '정상', tone: 'success' }, { name: '원본 위치 보호', detail: '운영 DTO에 좌표·경로 미포함', value: '보호 중', status: '정상', tone: 'success' }];

    const needsPeople = ['dashboard', 'users', 'devices', 'repairs', 'ledger', 'inspections'].includes(projection);
    const needsAssignments = ['users', 'devices'].includes(projection);
    const needsDevices = ['dashboard', 'users', 'devices', 'repairs', 'inspections'].includes(projection);
    const needsRepairs = ['dashboard', 'repairs', 'ledger', 'partners'].includes(projection);
    const [people, assignments, devices, repairs] = await Promise.all([
      needsPeople ? this.collection(tenant, 'people') : [],
      needsAssignments ? this.collection(tenant, 'deviceAssignments') : [],
      needsDevices ? this.collection(tenant, 'devices') : [],
      needsRepairs ? this.collection(tenant, 'repairWorkOrders') : [],
    ]);
    repairs.forEach(assertValidRepairDocument);
    const names = new Map(people.map((doc) => [doc.id, `이용자 ${optionalString(doc.data(), 'public_code') ?? shortCode(doc.id)}`]));
    const deviceById = new Map(devices.map((doc) => [doc.id, doc]));
    const activeAssignments = assignments.filter((doc) => activeAt(doc.data()));
    assertUniqueActiveAssignments(activeAssignments);
    const activeAssignmentByPerson = new Map(activeAssignments.map((doc) => [String(doc.data().person_id), doc]));
    const partnerById = new Map<string, string>();

    if (projection === 'users') return people.map((person, index) => {
      const assignment = activeAssignmentByPerson.get(person.id);
      const device = assignment ? deviceById.get(String(assignment.data().device_id)) : undefined;
      return { name: names.get(person.id) ?? `이용자 ${shortCode(person.id)}`, code: optionalString(person.data(), 'public_code') ?? shortCode(person.id), relation: '본인', device: device ? optionalString(device.data(), 'public_code') ?? shortCode(device.id) : '미배정', status: person.data().status === 'active' ? '정상' : '확인 필요', last: dateLabel(person.data().updated_at, '기록 없음'), color: ['peach', 'blue', 'lilac', 'mint', 'yellow'][index % 5] };
    });
    if (projection === 'devices') return devices.map((device) => {
      const assignment = activeAssignments.find((doc) => doc.data().device_id === device.id);
      const status = consoleDeviceStatus(device.data()?.status);
      return { id: optionalString(device.data(), 'public_code') ?? shortCode(device.id), user: assignment ? names.get(String(assignment.data().person_id)) ?? `이용자 ${shortCode(String(assignment.data().person_id))}` : '미배정', model: deviceName(device.data()), health: status.health, battery: optionalString(device.data(), 'battery_label') ?? '—', mileage: distanceLabel(device.data()?.odometer_m), inspection: dateLabel(device.data()?.next_inspection_at, '미정'), state: status.state };
    });
    if (projection === 'repairs') {
      const partners = await this.collection(tenant, 'repairStations');
      for (const partner of partners) partnerById.set(partner.id, publicPartnerName(partner));
      return repairs.filter((repair) => !['rejected', 'cancelled'].includes(String(repair.data().status))).sort(byUpdatedDesc).map((repair) => this.consoleRepair(repair, names, deviceById, partnerById));
    }
    if (projection === 'ledger') return this.consoleLedger(actor.tenantId, await this.collection(tenant, 'subsidyAccounts'), names);
    if (projection === 'inspections') return (await this.collection(tenant, 'inspections')).map((inspection) => ({ user: names.get(String(inspection.data().person_id)) ?? '이용자', device: publicDevice(deviceById.get(String(inspection.data().device_id))), due: dateLabel(inspection.data().scheduled_at ?? inspection.data().started_at, '미정'), reason: inspectionReason(inspection.data()?.reason_code), score: inspectionDecision(inspection.data()?.decision_code), confidence: inspectionConfidence(inspection.data()?.confidence_band) }));
    if (projection === 'partners') return (await this.collection(tenant, 'repairStations')).map((partner) => ({ name: publicPartnerName(partner), contact: '복지관 등록 연락망', active: `${repairs.filter((repair) => repair.data().repair_station_id === partner.id && activeRepairStatuses.has(repair.data().status)).length}건`, completed: `${safeCount(partner.data()?.completed_count)}건`, sla: optionalString(partner.data(), 'sla_label') ?? '측정 전', tone: partner.data().status === 'active' ? 'success' : 'warning' }));
    if (projection === 'reports') return (await this.collection(tenant, 'reportRuns')).map((report) => ({ title: reportTitle(report.data()?.report_type), type: reportTypeLabel(report.data()?.report_type), date: dateLabel(report.data().completed_at ?? report.data().created_at, '작성 중'), state: reportState(report.data()?.status), facts: String(safeCount(report.data()?.fact_count)) }));
    const [inspections, accounts] = await Promise.all([this.collection(tenant, 'inspections'), this.collection(tenant, 'subsidyAccounts')]);
    return this.dashboard(repairs, inspections, accounts, devices, names);
  }

  private async repairerSnapshot(actor: ActorContext, tenant: FirebaseFirestore.DocumentReference): Promise<MobileProductSnapshot> {
    const [jobs, tenantDoc] = await Promise.all([
      this.bounded(tenant.collection('repairWorkOrders').where('repairer_firebase_uid', '==', actor.uid).limit(MAX_PROJECTION_DOCUMENTS + 1).get(), 'assigned repair work orders', actor.tenantId),
      tenant.get(),
    ]);
    this.assertTenantDoc(tenantDoc.data(), actor.tenantId, 'TENANT_NOT_FOUND');
    jobs.forEach(assertValidRepairDocument);
    const assigned = jobs.filter((doc) => repairerVisibleStatuses.has(doc.data().status));
    const referencedPeople = [...new Set(assigned.map((job) => requiredString(job.data(), 'requester_person_id', 'CORRUPT_REPAIR_DOCUMENT')))];
    const referencedDevices = [...new Set(assigned.map((job) => requiredString(job.data(), 'device_id', 'CORRUPT_REPAIR_DOCUMENT')))];
    const [people, devices] = await Promise.all([
      Promise.all(referencedPeople.map((personId) => tenant.collection('people').doc(personId).get())),
      Promise.all(referencedDevices.map((deviceId) => tenant.collection('devices').doc(deviceId).get())),
    ]);
    for (const person of people) this.assertTenantDoc(person.data(), actor.tenantId, 'PERSON_NOT_FOUND');
    for (const device of devices) this.assertTenantDoc(device.data(), actor.tenantId, 'DEVICE_NOT_FOUND');
    const names = new Map(people.map((doc) => [doc.id, `이용자 ${optionalString(doc.data(), 'public_code') ?? shortCode(doc.id)}`]));
    const deviceById = new Map(devices.map((doc) => [doc.id, doc]));
    return {
      roleSession: { role: 'repairer', displayName: displayNameOf(tenantDoc.data(), '수리 파트너'), isDemo: false },
      repairJobs: assigned.sort(byUpdatedDesc).map((job) => {
        const status = job.data().status as 'assigned' | 'scheduled' | 'in_progress' | 'repairer_submitted' | 'needs_correction' | 'center_verified';
        const device = deviceById.get(String(job.data().device_id));
        return {
          id: job.id,
          revision: requiredPositiveInteger(job.data(), 'revision', 'CORRUPT_REPAIR_DOCUMENT'),
          status,
          customerLabel: names.get(String(job.data().requester_person_id)) ?? '이용자',
          device: { publicCode: publicDevice(device), model: deviceName(device?.data()) },
          issue: safeOperationalIssue(job.data()),
          scheduledAt: isoLabel(job.data().scheduled_at),
          scheduleLabel: dateTimeLabel(job.data().scheduled_at, '일정 협의 필요'),
          priority: isToday(job.data().scheduled_at) ? 'today' : 'scheduled',
          billedAmountKrw: nullableMoney(job.data().billed_amount_krw),
          submittedAt: isoLabel(job.data().submitted_at),
          allowedActions: repairerAllowedActions(status),
        };
      }),
    };
  }

  private async mobileWorkOrder(tenant: FirebaseFirestore.DocumentReference, workOrder: QueryDocumentSnapshot) {
    const stationId = optionalString(workOrder.data(), 'repair_station_id');
    const station = stationId ? await tenant.collection('repairStations').doc(stationId).get() : undefined;
    if (station?.exists) this.assertTenantDoc(station.data(), tenant.id, 'REPAIR_STATION_NOT_FOUND');
    return { id: workOrder.id, title: requiredString(workOrder.data(), 'issue_summary', 'CORRUPT_REPAIR_DOCUMENT'), createdAt: dateLabel(workOrder.data().created_at, '접수일 미확인'), status: mobileRepairStatus(workOrder.data().status), repairer: station?.exists ? optionalString(station.data(), 'display_name') ?? '배정된 수리센터' : '복지관에서 수리센터를 확인 중', visitAt: dateLabel(workOrder.data().scheduled_at, '일정 협의 필요') };
  }

  private mobileTimeline(orders: QueryDocumentSnapshot[], device: FirebaseFirestore.DocumentSnapshot) {
    const repairItems = orders.slice(0, 4).map((order) => ({ id: `repair-${order.id}`, date: dateLabel(order.data().updated_at ?? order.data().created_at, '날짜 미확인'), title: timelineTitle(order.data().status), detail: requiredString(order.data(), 'issue_summary', 'CORRUPT_REPAIR_DOCUMENT'), tone: order.data().status === 'completed' ? 'teal' as const : 'orange' as const }));
    return [...repairItems, { id: `device-${device.id}`, date: dateLabel(device.data()?.created_at, '등록일 미확인'), title: '내 기기를 등록했어요', detail: deviceName(device.data()), tone: 'blue' as const }].slice(0, 5);
  }

  private consoleRepair(repair: QueryDocumentSnapshot, names: Map<string, string>, devices: Map<string, QueryDocumentSnapshot>, partners: Map<string, string>) {
    const status = repair.data().status as RepairStatus;
    return { id: repair.id, user: names.get(String(repair.data().requester_person_id)) ?? `이용자 ${shortCode(String(repair.data().requester_person_id))}`, device: publicDevice(devices.get(String(repair.data().device_id))), issue: safeOperationalIssue(repair.data()), request: dateLabel(repair.data().created_at, '날짜 미확인'), partner: partners.get(String(repair.data().repair_station_id)) ?? '미배정', amount: moneyLabel(repair.data().billed_amount_krw ?? repair.data().requested_amount_krw), stage: consoleStage(status), priority: repair.data().priority === 'urgent_review' ? '높음' : repair.data().priority === 'routine' ? '낮음' : '보통', revision: requiredPositiveInteger(repair.data(), 'revision', 'CORRUPT_REPAIR_DOCUMENT') };
  }

  private async consoleLedger(tenantId: string, accounts: QueryDocumentSnapshot[], names: Map<string, string>) {
    const rows: Array<{ date: string; id: string; user: string; item: string; amount: string; state: '예약' | '집행 완료' | '예약 취소'; actor: string; occurred: number }> = [];
    for (const account of accounts) {
      const remaining = MAX_LEDGER_TRANSACTIONS - rows.length;
      const transactions = await this.bounded(account.ref.collection('transactions').limit(remaining + 1).get(), 'subsidy transactions', tenantId, remaining);
      transactions.forEach(assertValidLedgerTransaction);
      rows.push(...transactions.map((transaction) => ({ date: dateLabel(transaction.data().occurred_at, '날짜 미확인'), id: optionalString(transaction.data(), 'work_order_id') ?? transaction.id, user: names.get(String(account.data().person_id)) ?? `이용자 ${shortCode(String(account.data().person_id))}`, item: transactionLabel(transaction.data().transaction_type), amount: moneyLabel(transaction.data().amount_krw), state: ledgerState(transaction.data().transaction_type), actor: '기관 담당자', occurred: timestampMillis(transaction.data().occurred_at) })));
    }
    return rows.sort((a, b) => b.occurred - a.occurred).slice(0, 100).map(({ occurred: _occurred, ...row }) => row);
  }

  private dashboard(repairs: QueryDocumentSnapshot[], inspections: QueryDocumentSnapshot[], accounts: QueryDocumentSnapshot[], devices: QueryDocumentSnapshot[], names: Map<string, string>) {
    const pending = repairs.filter((repair) => activeRepairStatuses.has(repair.data().status));
    const executed = accounts.reduce((sum, account) => sum + safeMoney(account.data().executed_krw), 0);
    const first = pending.sort(byUpdatedDesc)[0];
    return { metrics: [{ label: '처리할 수리 요청', value: String(pending.length), suffix: '건', trend: '현재 기관 queue', tone: 'orange', icon: 'repair' }, { label: '예방점검 예정', value: String(inspections.length), suffix: '건', trend: '등록된 점검 일정', tone: 'purple', icon: 'check' }, { label: '누적 지원금 집행', value: moneyLabel(executed), suffix: '', trend: '기관 계정 합계', tone: 'green', icon: 'money' }, { label: '등록 기기', value: String(devices.length), suffix: '대', trend: '운영 projection 기준', tone: 'blue', icon: 'device' }], attention: first ? [{ icon: 'repair', color: 'orange', title: '확인할 수리 요청', description: `${names.get(String(first.data().requester_person_id)) ?? '이용자'} · ${safeOperationalIssue(first.data())}`, time: dateLabel(first.data().updated_at, '최근'), action: '확인하기', destination: 'repairs' }] : [], weeklyBars: ['월', '화', '수', '목', '금', '토', '일'].map((day, index) => ({ day, value: index === 3 ? pending.length : 0, ...(index === 3 ? { active: true } : {}) })), weeklyChange: '운영 데이터 기준' };
  }

  private async collection(tenant: FirebaseFirestore.DocumentReference, name: string) { return this.bounded(tenant.collection(name).limit(MAX_PROJECTION_DOCUMENTS + 1).get(), name, tenant.id); }
  private async bounded(query: Promise<FirebaseFirestore.QuerySnapshot>, label: string, tenantId?: string, maximum = MAX_PROJECTION_DOCUMENTS) { const snapshot = await query; if (snapshot.size > maximum) throw new DomainCommandError('PROJECTION_LIMIT_EXCEEDED', `${label} exceeds the bounded projection limit.`, 503); if (tenantId && snapshot.docs.some((doc) => doc.data().tenant_id !== tenantId)) throw new DomainCommandError('CORRUPT_TENANT_SCOPE', `${label} contains an invalid tenant scope.`, 500); return snapshot.docs; }
  private assertTenantDoc(data: DocumentData | undefined, tenantId: string, code: string) { if (!data || data.tenant_id !== tenantId) throw new DomainCommandError(code, 'The requested projection resource is unavailable.', 404); }
}

function isOperationalRole(role: string) { return role === 'case_worker' || role === 'tenant_admin'; }
function activeAt(data: DocumentData | undefined) { const now = Date.now(); const validFrom = timestampMillis(data?.valid_from); const validTo = data?.valid_to === undefined || data?.valid_to === null ? Number.POSITIVE_INFINITY : timestampMillis(data.valid_to); return data?.status === 'active' && validFrom > 0 && validFrom <= now && validTo > now; }
function assertUniqueActiveAssignments(assignments: QueryDocumentSnapshot[]) { const people = new Set<string>(); const devices = new Set<string>(); for (const assignment of assignments) { const person = requiredString(assignment.data(), 'person_id', 'CORRUPT_DEVICE_ASSIGNMENT'); const device = requiredString(assignment.data(), 'device_id', 'CORRUPT_DEVICE_ASSIGNMENT'); if (people.has(person) || devices.has(device)) throw new DomainCommandError('AMBIGUOUS_DEVICE_ASSIGNMENT', 'Active device assignments are not unique.', 409); people.add(person); devices.add(device); } }
function assertValidRepairDocument(repair: QueryDocumentSnapshot) { if (!allRepairStatuses.has(repair.data().status)) throw new DomainCommandError('CORRUPT_REPAIR_DOCUMENT', 'A repair document has an unsupported status.', 500); requiredString(repair.data(), 'requester_person_id', 'CORRUPT_REPAIR_DOCUMENT'); requiredString(repair.data(), 'device_id', 'CORRUPT_REPAIR_DOCUMENT'); requiredPositiveInteger(repair.data(), 'revision', 'CORRUPT_REPAIR_DOCUMENT'); }
function assertValidLedgerTransaction(transaction: QueryDocumentSnapshot) { if (!['allocation', 'reservation', 'execution', 'release', 'reversal', 'adjustment'].includes(String(transaction.data().transaction_type))) throw new DomainCommandError('CORRUPT_SUBSIDY_TRANSACTION', 'A subsidy transaction has an unsupported type.', 500); if (!Number.isSafeInteger(transaction.data().amount_krw) || transaction.data().amount_krw < 0) throw new DomainCommandError('CORRUPT_SUBSIDY_TRANSACTION', 'A subsidy transaction has an invalid amount.', 500); }
function byUpdatedDesc(a: QueryDocumentSnapshot, b: QueryDocumentSnapshot) { return timestampMillis(b.data().updated_at ?? b.data().created_at) - timestampMillis(a.data().updated_at ?? a.data().created_at); }
function optionalString(data: DocumentData | undefined, field: string): string | undefined { const value = data?.[field]; return typeof value === 'string' && value.trim() ? value.trim() : undefined; }
function requiredString(data: DocumentData | undefined, field: string, code: string): string { const value = optionalString(data, field); if (!value) throw new DomainCommandError(code, `Projection field ${field} is invalid.`, 500); return value; }
function requiredPositiveInteger(data: DocumentData | undefined, field: string, code: string): number { const value = data?.[field]; if (!Number.isSafeInteger(value) || value < 1) throw new DomainCommandError(code, `Projection field ${field} is invalid.`, 500); return value; }
function timestampMillis(value: unknown): number { if (value instanceof Timestamp) return value.toMillis(); if (typeof value === 'string') { const parsed = Date.parse(value); return Number.isNaN(parsed) ? 0 : parsed; } return 0; }
function dateLabel(value: unknown, fallback: string) { const millis = timestampMillis(value); return millis ? new Intl.DateTimeFormat('ko-KR', { timeZone: 'Asia/Seoul', year: 'numeric', month: '2-digit', day: '2-digit' }).format(millis) : fallback; }
function dateTimeLabel(value: unknown, fallback: string) { const millis = timestampMillis(value); return millis ? new Intl.DateTimeFormat('ko-KR', { timeZone: 'Asia/Seoul', month: 'long', day: 'numeric', weekday: 'short', hour: 'numeric', minute: '2-digit' }).format(millis) : fallback; }
function isoLabel(value: unknown): string | null { const millis = timestampMillis(value); return millis ? new Date(millis).toISOString() : null; }
function yearLabel(value: unknown, fallback: string) { const millis = timestampMillis(value); return millis ? `${new Date(millis).getUTCFullYear()}년 등록` : fallback; }
function displayNameOf(data: DocumentData | undefined, fallback: string) { return optionalString(data, 'display_name') ?? fallback; }
function deviceName(data: DocumentData | undefined) { const manufacturer = optionalString(data, 'manufacturer'); const model = optionalString(data, 'model_name'); return [manufacturer, model].filter(Boolean).join(' ') || '전동보장구'; }
function mobileDeviceStatus(status: unknown): 'healthy' | 'attention' { if (status === 'active') return 'healthy'; if (status === 'maintenance') return 'attention'; throw new DomainCommandError('CORRUPT_DEVICE_DOCUMENT', 'The assigned device has an unsupported status.', 500); }
function consoleDeviceStatus(status: unknown): { health: string; state: string } { if (status === 'active') return { health: '양호', state: '정상' }; if (status === 'maintenance') return { health: '점검 권장', state: '주의' }; if (status === 'unassigned') return { health: '미배정', state: '대기' }; if (status === 'retired' || status === 'lost') return { health: '운영 제외', state: '확인 필요' }; throw new DomainCommandError('CORRUPT_DEVICE_DOCUMENT', 'A device has an unsupported status.', 500); }
function safeOperationalIssue(data: DocumentData | undefined) { return data?.issue_redaction_status === 'verified' ? optionalString(data, 'issue_summary_redacted') ?? '수리 요청 상세 확인 필요' : optionalString(data, 'issue_category_label') ?? '수리 요청 상세 확인 필요'; }
function publicPartnerName(partner: QueryDocumentSnapshot) { return optionalString(partner.data(), 'display_name') ?? `수리소 ${shortCode(partner.id)}`; }
function inspectionReason(code: unknown) { return ({ routine_cycle: '정기 점검 주기 도래', repair_followup: '수리 후 확인', usage_change: '사용량 변화 확인' } as Record<string, string>)[String(code)] ?? '점검 사유 확인 필요'; }
function inspectionDecision(code: unknown) { return ({ healthy: '양호', review: '검토', inspection_recommended: '점검 권장' } as Record<string, string>)[String(code)] ?? '검토'; }
function inspectionConfidence(band: unknown) { return ({ high: '높음', medium: '보통', low: '낮음', insufficient_data: '데이터 부족' } as Record<string, string>)[String(band)] ?? '데이터 부족'; }
function reportTitle(type: unknown) { return ({ monthly_operations: '기관 월간 운영 리포트', subsidy_audit: '수리 지원금 집행 현황', inspection_priority: '예방점검 우선순위 목록' } as Record<string, string>)[String(type)] ?? '운영 보고서'; }
function reportTypeLabel(type: unknown) { return ({ monthly_operations: '월간 운영', subsidy_audit: '재정·감사', inspection_priority: '점검 운영' } as Record<string, string>)[String(type)] ?? '운영'; }
function publicDevice(device: QueryDocumentSnapshot | undefined) { return device ? optionalString(device.data(), 'public_code') ?? shortCode(device.id) : '기기 미확인'; }
function shortCode(value: string) { return value.slice(-8).toUpperCase(); }
function safeMoney(value: unknown) { return Number.isSafeInteger(value) && (value as number) >= 0 ? value as number : 0; }
function nullableMoney(value: unknown): number | null { return Number.isSafeInteger(value) && (value as number) >= 0 ? value as number : null; }
function safeSignedMoney(value: unknown) { return Number.isSafeInteger(value) ? value as number : 0; }
function safeCount(value: unknown) { return Number.isSafeInteger(value) && (value as number) >= 0 ? value as number : 0; }
function moneyLabel(value: unknown) { return `₩${safeMoney(value).toLocaleString('ko-KR')}`; }
function distanceLabel(value: unknown) { return Number.isFinite(value) && (value as number) >= 0 ? `${Math.round((value as number) / 1000).toLocaleString('ko-KR')} km` : '—'; }
function mobileRepairStatus(status: unknown): 'received' | 'assigned' | 'visit_scheduled' | 'completed' { if (status === 'completed') return 'completed'; if (status === 'scheduled' || status === 'in_progress' || status === 'repairer_submitted' || status === 'center_verified') return 'visit_scheduled'; if (status === 'assigned') return 'assigned'; return 'received'; }
function consoleStage(status: RepairStatus): 'new' | 'assigned' | 'submitted' | 'verified' { if (status === 'repairer_submitted' || status === 'needs_correction') return 'submitted'; if (status === 'center_verified' || status === 'completed') return 'verified'; if (status === 'assigned' || status === 'scheduled' || status === 'in_progress') return 'assigned'; return 'new'; }
function timelineTitle(status: unknown) { if (status === 'completed') return '수리를 완료했어요'; if (status === 'rejected') return '수리 요청을 처리할 수 없어요'; if (status === 'cancelled') return '수리 요청을 취소했어요'; if (status === 'center_verified') return '복지관 검증을 마쳤어요'; if (status === 'assigned' || status === 'scheduled') return '수리센터가 배정됐어요'; return '수리 요청을 접수했어요'; }
function transactionLabel(type: unknown) { return ({ allocation: '지원금 배정', reservation: '수리 지원금 예약', execution: '수리 지원금 집행', release: '예약 해제', reversal: '집행 취소', adjustment: '지원금 조정' } as Record<string, string>)[String(type)] ?? '지원금 변동'; }
function ledgerState(type: unknown): '예약' | '집행 완료' | '예약 취소' { return type === 'execution' ? '집행 완료' : type === 'release' || type === 'reversal' ? '예약 취소' : '예약'; }
function reportState(status: unknown) { return status === 'completed' ? '발행 완료' : status === 'running' ? '검토 중' : '초안'; }
function isToday(value: unknown) { const millis = timestampMillis(value); if (!millis) return false; const now = new Date(); const date = new Date(millis); return now.getUTCFullYear() === date.getUTCFullYear() && now.getUTCMonth() === date.getUTCMonth() && now.getUTCDate() === date.getUTCDate(); }
function repairerAllowedActions(status: RepairStatus): Array<'schedule' | 'start' | 'submit' | 'resume'> { if (status === 'assigned') return ['schedule']; if (status === 'scheduled') return ['start']; if (status === 'in_progress') return ['submit']; if (status === 'needs_correction') return ['resume']; return []; }
