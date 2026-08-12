export type WorkflowStage = 'new' | 'assigned' | 'submitted' | 'verified'

export type DashboardIcon = 'repair' | 'check' | 'money' | 'device'
export type DashboardTone = 'orange' | 'purple' | 'green' | 'blue'

export type UserRecord = {
  name: string
  code: string
  relation: string
  device: string
  status: string
  last: string
  color: 'peach' | 'blue' | 'lilac' | 'mint' | 'yellow'
}

export type DeviceRecord = {
  id: string
  user: string
  model: string
  health: string
  battery: string
  mileage: string
  inspection: string
  state: string
  timeline: DeviceTimelineItem[]
}

/** A safe, read-only event for the device detail timeline. */
export type DeviceTimelineItem = {
  id: string
  date: string
  title: string
  detail: string
  tone: 'success' | 'warning' | 'info'
}

export type RepairRecord = {
  id: string
  user: string
  device: string
  issue: string
  request: string
  partner: string
  amount: string
  workItems: Array<{ categoryCode: string; categoryLabel: string; actionCode: string; actionLabel: string; quantity: number; lineAmountKrw: number }>
  stage: WorkflowStage
  priority: '높음' | '보통' | '낮음'
  /** Server projection revision. Required for a production status transition. */
  revision: number
}

export type LedgerEntry = {
  date: string
  id: string
  user: string
  item: string
  amount: string
  state: '예약' | '집행 완료' | '예약 취소'
  actor: string
}

export type InspectionRecord = {
  user: string
  device: string
  due: string
  reason: string
  score: string
  confidence: string
}

export type PartnerRecord = {
  name: string
  contact: string
  active: string
  completed: string
  sla: string
  tone: 'success' | 'warning'
}

export type ReportRecord = {
  title: string
  type: string
  date: string
  state: string
  facts: string
}

export type ServiceStatusRecord = {
  name: string
  detail: string
  value: string
  status: string
  tone: 'success' | 'warning'
}

export type DashboardMetric = {
  label: string
  value: string
  suffix: string
  trend: string
  tone: DashboardTone
  icon: DashboardIcon
}

export type DashboardAttention = {
  icon: 'repair' | 'check' | 'device'
  color: 'orange' | 'purple' | 'blue'
  title: string
  description: string
  time: string
  action: string
  destination: 'repairs' | 'inspections' | 'devices'
}

export type DashboardBar = {
  day: string
  value: number
  active?: boolean
  muted?: boolean
}

export type DashboardData = {
  metrics: DashboardMetric[]
  attention: DashboardAttention[]
  weeklyBars: DashboardBar[]
  weeklyChange: string
}

export type RepairAdvanceCommand = {
  type: 'repair.advance'
  repairId: string
  actorId: string
  /** Optional demo-only context collected by the stage-aware console form. */
  repairStationId?: string
  repairerFirebaseUid?: string
  note?: string
}

export type DomainRepairStatus =
  | 'requested'
  | 'under_review'
  | 'assigned'
  | 'scheduled'
  | 'in_progress'
  | 'repairer_submitted'
  | 'needs_correction'
  | 'center_verified'
  | 'completed'
  | 'reopened'
  | 'rejected'
  | 'cancelled'

/**
 * Explicit domain command used by a real operations screen. `actorId` is
 * deliberately absent: the command API derives it from the ID token.
 */
type RepairTransitionBase = {
  repairRequestId: string
  expectedRevision: number
}

export type RepairTransitionCommand =
  | (RepairTransitionBase & { toStatus: 'under_review' | 'completed' | 'reopened' | 'rejected' | 'cancelled'; note?: string })
  | (RepairTransitionBase & { toStatus: 'assigned'; repairStationId: string; repairerFirebaseUid: string; note?: string })
  | (RepairTransitionBase & { toStatus: 'needs_correction'; note?: string })
  | (RepairTransitionBase & { toStatus: 'center_verified'; subsidyAccountId?: string; subsidyDecisionId: string; note?: string })

export type SubsidyTransactionType = 'allocation' | 'reservation' | 'execution' | 'release' | 'adjustment' | 'reversal'

export type SubsidyCommand = {
  accountId: string
  personId: string
  policyVersionId: string
  transactionType: SubsidyTransactionType
  amountKrw: number
  workOrderId?: string
  reversesTransactionId?: string
  reasonCode: string
  note?: string
}

export type CommandReceipt = {
  commandType: 'create_repair_request' | 'transition_repair_request' | 'append_subsidy_transaction'
  tenantId: string
  resourceId: string
  eventId: string
  revision?: number
  status?: DomainRepairStatus
  transactionId?: string
  idempotent?: boolean
}

export type RepairTransitionResult = {
  repair: RepairRecord
  repairs: RepairRecord[]
  ledger: LedgerEntry[]
  nextStage: WorkflowStage | null
}

/**
 * Product Operations is a read model and domain-command boundary for the console.
 *
 * Expected Domain Command API:
 *   console UI -> authenticated command gateway -> domain worker -> projections
 *
 * Commands should carry an explicit type, target id, actor id, and idempotency
 * key in the production gateway. The repository intentionally does not write
 * Firestore; it either delegates to that command API or, for the demo adapter,
 * applies a deterministic in-memory transition.
 */
export interface ProductOperationsRepository {
  loadDashboard(): Promise<DashboardData>
  listUsers(): Promise<UserRecord[]>
  listDevices(): Promise<DeviceRecord[]>
  listRepairs(): Promise<RepairRecord[]>
  listLedger(): Promise<LedgerEntry[]>
  listInspections(): Promise<InspectionRecord[]>
  listPartners(): Promise<PartnerRecord[]>
  listReports(): Promise<ReportRecord[]>
  listServices(): Promise<ServiceStatusRecord[]>
  advanceRepair(command: RepairAdvanceCommand): Promise<RepairTransitionResult>
  transitionRepair(command: RepairTransitionCommand): Promise<CommandReceipt>
  appendSubsidyTransaction(command: SubsidyCommand): Promise<CommandReceipt>
}

export class NotConfiguredError extends Error {
  readonly code = 'NOT_CONFIGURED'

  constructor(operation: string) {
    super(`NOT_CONFIGURED: Firebase Operations Repository is not configured for ${operation}.`)
    this.name = 'NotConfiguredError'
  }
}

export type OperationsRepositoryErrorCode =
  | 'NOT_CONFIGURED'
  | 'AUTH_REQUIRED'
  | 'APP_CHECK_REQUIRED'
  | 'NETWORK_FAILURE'
  | 'REQUEST_REJECTED'
  | 'INVALID_PROJECTION'
  | 'INVALID_COMMAND_RESULT'
  | 'INVALID_COMMAND'

/** A typed, non-demo failure. Production callers must not silently fall back. */
export class OperationsRepositoryError extends Error {
  constructor(readonly code: OperationsRepositoryErrorCode | string, message: string, readonly status?: number) {
    super(message)
    this.name = 'OperationsRepositoryError'
  }
}

const demoDashboard: DashboardData = {
  metrics: [
    { label: '처리할 수리 요청', value: '7', suffix: '건', trend: '지난주보다 2건 많아요', tone: 'orange', icon: 'repair' },
    { label: '예방점검 예정', value: '12', suffix: '건', trend: '이번 주 안에 확인', tone: 'purple', icon: 'check' },
    { label: '이번 달 지원금', value: '₩2,480,000', suffix: '', trend: '예산의 62% 집행', tone: 'green', icon: 'money' },
    { label: '데이터 동기화 상태', value: '98.7', suffix: '%', trend: '최근 24시간 기준', tone: 'blue', icon: 'device' },
  ],
  attention: [
    { icon: 'repair', color: 'orange', title: '새 수리 요청 1건', description: '박정호님 · 주행 중 좌측 쏠림', time: '8분 전', action: '배정하기', destination: 'repairs' },
    { icon: 'check', color: 'purple', title: '예방점검 예정 3건', description: '이번 주 안에 방문 일정을 잡아주세요', time: '기한 임박', action: '일정 보기', destination: 'inspections' },
    { icon: 'device', color: 'blue', title: '동기화 대기 기기 1대', description: '이경자님 · 마지막 연결 1일 전', time: '확인 필요', action: '상태 보기', destination: 'devices' },
  ],
  weeklyBars: [
    { day: '월', value: 4 },
    { day: '화', value: 7 },
    { day: '수', value: 5 },
    { day: '목', value: 9, active: true },
    { day: '금', value: 3, muted: true },
    { day: '토', value: 2, muted: true },
    { day: '일', value: 1, muted: true },
  ],
  weeklyChange: '+18%',
}

const demoUsers: UserRecord[] = [
  { name: '김서윤', code: 'C-1042', relation: '본인', device: 'MOB-24018', status: '정상', last: '오늘 09:42', color: 'peach' },
  { name: '박정호', code: 'C-1038', relation: '보호자 연결', device: 'MOB-23991', status: '점검 권장', last: '어제 17:20', color: 'blue' },
  { name: '이경자', code: 'C-1031', relation: '본인', device: 'MOB-23874', status: '동기화 대기', last: '08. 11. 15:08', color: 'lilac' },
  { name: '최민수', code: 'C-1019', relation: '보호자 연결', device: 'MOB-23703', status: '정상', last: '오늘 08:11', color: 'mint' },
  { name: '윤옥순', code: 'C-0998', relation: '본인', device: 'MOB-23218', status: '데이터 부족', last: '08. 09. 11:56', color: 'yellow' },
]

const demoDevices: DeviceRecord[] = [
  {
    id: 'MOB-24018', user: '김서윤', model: '나래 EV-2', health: '양호', battery: '78%', mileage: '1,284 km', inspection: '2026. 09. 03', state: '정상',
    timeline: [
      { id: 'MOB-24018-repair-1', date: '2026. 08. 08', title: '타이어 점검을 완료했어요', detail: '바퀴·타이어 점검 기록', tone: 'success' },
      { id: 'MOB-24018-inspection-1', date: '2026. 08. 07', title: '예방점검 일정을 확인했어요', detail: '다음 점검일 2026. 09. 03', tone: 'info' },
      { id: 'MOB-24018-device-1', date: '2024. 04. 05', title: '기기를 등록했어요', detail: '나래 EV-2', tone: 'info' },
    ],
  },
  {
    id: 'MOB-23991', user: '박정호', model: '오르빗 S1', health: '점검 권장', battery: '42%', mileage: '2,810 km', inspection: '2026. 08. 16', state: '주의',
    timeline: [
      { id: 'MOB-23991-alert-1', date: '2026. 08. 13', title: '점검을 권장해요', detail: '조향부 진동 증가가 확인됐어요', tone: 'warning' },
      { id: 'MOB-23991-repair-1', date: '2026. 08. 13', title: '수리 요청을 접수했어요', detail: '주행 중 좌측 쏠림', tone: 'info' },
      { id: 'MOB-23991-device-1', date: '2023. 10. 18', title: '기기를 등록했어요', detail: '오르빗 S1', tone: 'info' },
    ],
  },
  {
    id: 'MOB-23874', user: '이경자', model: '나래 EV-2', health: '데이터 부족', battery: '61%', mileage: '—', inspection: '2026. 08. 14', state: '대기',
    timeline: [
      { id: 'MOB-23874-sync-1', date: '2026. 08. 11', title: '주행 기록 동기화를 기다리고 있어요', detail: '최근 3일의 기록이 필요해요', tone: 'warning' },
      { id: 'MOB-23874-repair-1', date: '2026. 08. 12', title: '수리 요청을 접수했어요', detail: '충전 단자 접촉 불량', tone: 'info' },
      { id: 'MOB-23874-device-1', date: '2023. 08. 22', title: '기기를 등록했어요', detail: '나래 EV-2', tone: 'info' },
    ],
  },
  {
    id: 'MOB-23703', user: '최민수', model: '모빌리티 K3', health: '양호', battery: '88%', mileage: '948 km', inspection: '2026. 09. 22', state: '정상',
    timeline: [
      { id: 'MOB-23703-repair-1', date: '2026. 08. 12', title: '수리 확인을 완료했어요', detail: '등받이 고정 레버를 교체했어요', tone: 'success' },
      { id: 'MOB-23703-inspection-1', date: '2026. 08. 10', title: '정기 점검 일정을 등록했어요', detail: '다음 점검일 2026. 09. 22', tone: 'info' },
      { id: 'MOB-23703-device-1', date: '2024. 01. 15', title: '기기를 등록했어요', detail: '모빌리티 K3', tone: 'info' },
    ],
  },
]

const demoRepairs: RepairRecord[] = [
  { id: 'SR-2026-081', user: '박정호', device: 'MOB-23991', issue: '주행 중 좌측 쏠림', request: '오늘 08:35', partner: '미배정', amount: '₩180,000', workItems: [], stage: 'new', priority: '높음', revision: 1 },
  { id: 'SR-2026-079', user: '이경자', device: 'MOB-23874', issue: '충전 단자 접촉 불량', request: '어제 16:10', partner: '한마음 모빌리티', amount: '₩95,000', workItems: [], stage: 'assigned', priority: '보통', revision: 3 },
  { id: 'SR-2026-074', user: '최민수', device: 'MOB-23703', issue: '등받이 고정 레버 교체', request: '08. 10. 11:22', partner: '케어휠 수리소', amount: '₩72,000', workItems: [{ categoryCode: 'seat_frame', categoryLabel: '시트·프레임', actionCode: 'replace', actionLabel: '교체', quantity: 1, lineAmountKrw: 72000 }], stage: 'submitted', priority: '보통', revision: 4 },
  { id: 'SR-2026-069', user: '김서윤', device: 'MOB-24018', issue: '타이어 마모 점검', request: '08. 08. 14:05', partner: '한마음 모빌리티', amount: '₩58,000', workItems: [{ categoryCode: 'wheel_tire', categoryLabel: '바퀴·타이어', actionCode: 'inspect', actionLabel: '점검', quantity: 1, lineAmountKrw: 58000 }], stage: 'verified', priority: '낮음', revision: 5 },
]

const demoLedger: LedgerEntry[] = [
  { date: '08. 13', id: 'SR-2026-081', user: '박정호', item: '조향부 점검 및 교정', amount: '₩180,000', state: '예약', actor: '김은정 담당자' },
  { date: '08. 12', id: 'SR-2026-074', user: '최민수', item: '등받이 고정 레버', amount: '₩72,000', state: '집행 완료', actor: '케어휠 수리소' },
  { date: '08. 08', id: 'SR-2026-069', user: '김서윤', item: '타이어 마모 점검', amount: '₩58,000', state: '집행 완료', actor: '김은정 담당자' },
  { date: '08. 04', id: 'SR-2026-062', user: '윤옥순', item: '배터리 상태 점검', amount: '₩110,000', state: '예약 취소', actor: '김은정 담당자' },
]

const demoInspections: InspectionRecord[] = [
  { user: '박정호', device: 'MOB-23991', due: '08. 16', reason: '조향부 진동 증가', score: '주의', confidence: '높음' },
  { user: '이경자', device: 'MOB-23874', due: '08. 14', reason: '주행 데이터 3일 부족', score: '데이터 부족', confidence: '—' },
  { user: '윤옥순', device: 'MOB-23218', due: '08. 18', reason: '정기 점검 주기 도래', score: '관찰', confidence: '보통' },
  { user: '최민수', device: 'MOB-23703', due: '09. 22', reason: '정기 점검 주기 도래', score: '양호', confidence: '높음' },
]

const demoPartners: PartnerRecord[] = [
  { name: '한마음 모빌리티', contact: '김도현 · 02-321-8842', active: '3건', completed: '18건', sla: '1.4일', tone: 'success' },
  { name: '케어휠 수리소', contact: '장유진 · 02-884-2011', active: '2건', completed: '12건', sla: '2.1일', tone: 'warning' },
  { name: '서부 보장구 센터', contact: '이재훈 · 02-771-0390', active: '0건', completed: '24건', sla: '1.1일', tone: 'success' },
]

const demoReports: ReportRecord[] = [
  { title: '7월 기관 운영 리포트', type: '월간 운영', date: '2026. 08. 01', state: '발행 완료', facts: '18' },
  { title: '수리 지원금 집행 현황', type: '재정·감사', date: '2026. 08. 08', state: '검토 중', facts: '12' },
  { title: '예방점검 우선순위 목록', type: '점검 운영', date: '2026. 08. 12', state: '초안', facts: '9' },
]

const demoServices: ServiceStatusRecord[] = [
  { name: '모바일 수집', detail: '최근 24시간 수집 성공률', value: '98.7%', status: '정상', tone: 'success' },
  { name: '동기화 큐', detail: '미처리 이벤트 14건 · 최대 12분', value: '양호', status: '정상', tone: 'success' },
  { name: '상태 projection', detail: '마지막 처리 지연 4분', value: '4분', status: '정상', tone: 'success' },
  { name: '보고서 검증', detail: '최근 30일 검증 실패 1건', value: '주의', status: '확인 필요', tone: 'warning' },
]

const clone = <T,>(value: T): T => structuredClone(value)

export class DemoOperationsRepository implements ProductOperationsRepository {
  private repairs = clone(demoRepairs)
  private ledger = clone(demoLedger)

  async loadDashboard() { return clone(demoDashboard) }
  async listUsers() { return clone(demoUsers) }
  async listDevices() { return clone(demoDevices) }
  async listRepairs() { return clone(this.repairs) }
  async listLedger() { return clone(this.ledger) }
  async listInspections() { return clone(demoInspections) }
  async listPartners() { return clone(demoPartners) }
  async listReports() { return clone(demoReports) }
  async listServices() { return clone(demoServices) }

  async advanceRepair(command: RepairAdvanceCommand): Promise<RepairTransitionResult> {
    const repair = this.repairs.find((item) => item.id === command.repairId)
    if (!repair) throw new Error(`REPAIR_NOT_FOUND: ${command.repairId}`)

    const order: WorkflowStage[] = ['new', 'assigned', 'submitted', 'verified']
    const index = order.indexOf(repair.stage)
    const nextStage = index >= 0 && index < order.length - 1 ? order[index + 1] : null
    if (!nextStage) return { repair: clone(repair), repairs: clone(this.repairs), ledger: clone(this.ledger), nextStage: null }

    const demoPartnerLabels: Record<string, string> = {
      'station-hanmaeum': '한마음 모빌리티',
      'station-carewheel': '케어휠 수리소',
      'station-western': '서부 보장구 센터',
    }
    const updated = {
      ...repair,
      stage: nextStage,
      partner: nextStage === 'assigned' ? demoPartnerLabels[command.repairStationId ?? ''] ?? '한마음 모빌리티' : repair.partner,
      revision: repair.revision + 1,
    }
    this.repairs = this.repairs.map((item) => item.id === updated.id ? updated : item)
    return { repair: clone(updated), repairs: clone(this.repairs), ledger: clone(this.ledger), nextStage }
  }

  async transitionRepair(command: RepairTransitionCommand): Promise<CommandReceipt> {
    const repair = this.repairs.find((item) => item.id === command.repairRequestId)
    if (!repair) throw new Error(`REPAIR_NOT_FOUND: ${command.repairRequestId}`)
    if (repair.revision !== command.expectedRevision) throw new OperationsRepositoryError('REQUEST_REJECTED', 'REVISION_CONFLICT: reload before changing this repair.', 409)
    return { commandType: 'transition_repair_request', tenantId: 'demo-tenant', resourceId: repair.id, eventId: `demo-event-${repair.id}-${repair.revision + 1}`, revision: repair.revision + 1, status: command.toStatus }
  }

  async appendSubsidyTransaction(command: SubsidyCommand): Promise<CommandReceipt> {
    return { commandType: 'append_subsidy_transaction', tenantId: 'demo-tenant', resourceId: command.accountId, eventId: `demo-ledger-${command.accountId}`, transactionId: `demo-tx-${command.accountId}` }
  }
}

/** Firebase Auth/App Check values are injected at the app composition root.
 * This module deliberately has no Firebase client, Firestore SDK, or direct
 * write capability. */
export interface OperationsTokenProvider {
  tenantId: string
  getIdToken(): Promise<string>
  getAppCheckToken(): Promise<string>
}

export interface OperationsApiEndpoints {
  projections: {
    dashboard: string
    users: string
    devices: string
    repairs: string
    ledger: string
    inspections: string
    partners: string
    reports: string
    services: string
  }
  transitionRepairRequest: string
  appendSubsidyTransaction: string
}

/**
 * The deployed Functions surface has one read endpoint for the console. The
 * projection name is a query parameter, rather than a path segment. Keeping
 * this construction in one place prevents a client from accidentally calling
 * an endpoint that does not exist in the production Functions bundle.
 */
export type OperationsEndpointOptions = {
  baseUrl: string
  commandBaseUrl?: string
}

const consoleProjectionNames: Array<keyof OperationsApiEndpoints['projections']> = [
  'dashboard', 'users', 'devices', 'repairs', 'ledger', 'inspections', 'partners', 'reports', 'services',
]

export function createOperationsApiEndpoints(options: OperationsEndpointOptions): OperationsApiEndpoints {
  const baseUrl = options.baseUrl.trim().replace(/\/+$/, '')
  const commandBaseUrl = (options.commandBaseUrl ?? options.baseUrl).trim().replace(/\/+$/, '')
  if (!baseUrl) throw new Error('A console operations API base URL is required.')
  if (!commandBaseUrl) throw new Error('A console command API base URL is required.')

  const snapshotUrl = `${baseUrl}/getConsoleOperationsSnapshot`
  const projections = Object.fromEntries(
    consoleProjectionNames.map((projection) => [projection, `${snapshotUrl}?projection=${encodeURIComponent(projection)}`]),
  ) as OperationsApiEndpoints['projections']
  return {
    projections,
    transitionRepairRequest: `${commandBaseUrl}/transitionRepairRequest`,
    appendSubsidyTransaction: `${commandBaseUrl}/appendSubsidyTransaction`,
  }
}

/** Explicitly named alias for app composition roots. */
export const createConsoleOperationsApiEndpoints = createOperationsApiEndpoints

export interface FirebaseOperationsRepositoryDependencies {
  tokens: OperationsTokenProvider
  /** Either provide explicit endpoint URLs or a Functions origin. */
  endpoints?: OperationsApiEndpoints
  baseUrl?: string
  commandBaseUrl?: string
  fetch?: typeof fetch
  /** Injectable for deterministic tests. Production defaults to crypto.randomUUID(). */
  createIdempotencyKey?: () => string
}

type ProjectionParser<T> = (value: unknown) => T

const optional = <T,>(key: string, value: T | undefined): Record<string, T> => value === undefined ? {} : { [key]: value }

const asRecord = (value: unknown, code = 'INVALID_PROJECTION'): Record<string, unknown> => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new OperationsRepositoryError(code, 'The server response does not match the expected projection.')
  return value as Record<string, unknown>
}

const stringAt = (value: Record<string, unknown>, key: string): string => {
  if (typeof value[key] !== 'string') throw new OperationsRepositoryError('INVALID_PROJECTION', `Projection field ${key} must be a string.`)
  return value[key]
}

const numberAt = (value: Record<string, unknown>, key: string): number => {
  if (typeof value[key] !== 'number' || !Number.isFinite(value[key])) throw new OperationsRepositoryError('INVALID_PROJECTION', `Projection field ${key} must be a finite number.`)
  return value[key]
}

const enumAt = <T extends string>(value: Record<string, unknown>, key: string, allowed: readonly T[]): T => {
  const candidate = stringAt(value, key)
  if (!allowed.includes(candidate as T)) throw new OperationsRepositoryError('INVALID_PROJECTION', `Projection field ${key} has an unsupported value.`)
  return candidate as T
}

const arrayOf = <T,>(value: unknown, parse: ProjectionParser<T>): T[] => {
  if (!Array.isArray(value)) throw new OperationsRepositoryError('INVALID_PROJECTION', 'The server response must be an array projection.')
  return value.map(parse)
}

const parseDashboard: ProjectionParser<DashboardData> = (value) => {
  const record = asRecord(value)
  const metrics = arrayOf(record.metrics, (item): DashboardMetric => {
    const itemRecord = asRecord(item)
    return { label: stringAt(itemRecord, 'label'), value: stringAt(itemRecord, 'value'), suffix: stringAt(itemRecord, 'suffix'), trend: stringAt(itemRecord, 'trend'), tone: enumAt(itemRecord, 'tone', ['orange', 'purple', 'green', 'blue']), icon: enumAt(itemRecord, 'icon', ['repair', 'check', 'money', 'device']) }
  })
  const attention = arrayOf(record.attention, (item): DashboardAttention => {
    const itemRecord = asRecord(item)
    return { icon: enumAt(itemRecord, 'icon', ['repair', 'check', 'device']), color: enumAt(itemRecord, 'color', ['orange', 'purple', 'blue']), title: stringAt(itemRecord, 'title'), description: stringAt(itemRecord, 'description'), time: stringAt(itemRecord, 'time'), action: stringAt(itemRecord, 'action'), destination: enumAt(itemRecord, 'destination', ['repairs', 'inspections', 'devices']) }
  })
  const weeklyBars = arrayOf(record.weeklyBars, (item): DashboardBar => {
    const itemRecord = asRecord(item)
    const active = itemRecord.active
    const muted = itemRecord.muted
    if (active !== undefined && typeof active !== 'boolean') throw new OperationsRepositoryError('INVALID_PROJECTION', 'Projection field active must be boolean.')
    if (muted !== undefined && typeof muted !== 'boolean') throw new OperationsRepositoryError('INVALID_PROJECTION', 'Projection field muted must be boolean.')
    return { day: stringAt(itemRecord, 'day'), value: numberAt(itemRecord, 'value'), ...optional('active', active), ...optional('muted', muted) }
  })
  return { metrics, attention, weeklyBars, weeklyChange: stringAt(record, 'weeklyChange') }
}

const parseUsers: ProjectionParser<UserRecord[]> = (value) => arrayOf(value, (item) => {
  const record = asRecord(item)
  return { name: stringAt(record, 'name'), code: stringAt(record, 'code'), relation: stringAt(record, 'relation'), device: stringAt(record, 'device'), status: stringAt(record, 'status'), last: stringAt(record, 'last'), color: enumAt(record, 'color', ['peach', 'blue', 'lilac', 'mint', 'yellow']) }
})

const parseDeviceTimeline: ProjectionParser<DeviceTimelineItem[]> = (value) => arrayOf(value, (item) => {
  const timeline = asRecord(item)
  return {
    id: stringAt(timeline, 'id'),
    date: stringAt(timeline, 'date'),
    title: stringAt(timeline, 'title'),
    detail: stringAt(timeline, 'detail'),
    tone: enumAt(timeline, 'tone', ['success', 'warning', 'info']),
  }
})

const parseDevices: ProjectionParser<DeviceRecord[]> = (value) => arrayOf(value, (item) => {
  const record = asRecord(item)
  return { id: stringAt(record, 'id'), user: stringAt(record, 'user'), model: stringAt(record, 'model'), health: stringAt(record, 'health'), battery: stringAt(record, 'battery'), mileage: stringAt(record, 'mileage'), inspection: stringAt(record, 'inspection'), state: stringAt(record, 'state'), timeline: parseDeviceTimeline(record.timeline) }
})

const parseRepairs: ProjectionParser<RepairRecord[]> = (value) => arrayOf(value, (item) => {
  const record = asRecord(item)
  const workItems = record.workItems === undefined ? [] : arrayOf(record.workItems, (candidate) => { const work = asRecord(candidate); return { categoryCode: stringAt(work, 'categoryCode'), categoryLabel: stringAt(work, 'categoryLabel'), actionCode: stringAt(work, 'actionCode'), actionLabel: stringAt(work, 'actionLabel'), quantity: numberAt(work, 'quantity'), lineAmountKrw: numberAt(work, 'lineAmountKrw') } })
  return { id: stringAt(record, 'id'), user: stringAt(record, 'user'), device: stringAt(record, 'device'), issue: stringAt(record, 'issue'), request: stringAt(record, 'request'), partner: stringAt(record, 'partner'), amount: stringAt(record, 'amount'), workItems, stage: enumAt(record, 'stage', ['new', 'assigned', 'submitted', 'verified']), priority: enumAt(record, 'priority', ['높음', '보통', '낮음']), revision: numberAt(record, 'revision') }
})

const parseLedger: ProjectionParser<LedgerEntry[]> = (value) => arrayOf(value, (item) => {
  const record = asRecord(item)
  return { date: stringAt(record, 'date'), id: stringAt(record, 'id'), user: stringAt(record, 'user'), item: stringAt(record, 'item'), amount: stringAt(record, 'amount'), state: enumAt(record, 'state', ['예약', '집행 완료', '예약 취소']), actor: stringAt(record, 'actor') }
})

const parseInspections: ProjectionParser<InspectionRecord[]> = (value) => arrayOf(value, (item) => {
  const record = asRecord(item)
  return { user: stringAt(record, 'user'), device: stringAt(record, 'device'), due: stringAt(record, 'due'), reason: stringAt(record, 'reason'), score: stringAt(record, 'score'), confidence: stringAt(record, 'confidence') }
})

const parsePartners: ProjectionParser<PartnerRecord[]> = (value) => arrayOf(value, (item) => {
  const record = asRecord(item)
  return { name: stringAt(record, 'name'), contact: stringAt(record, 'contact'), active: stringAt(record, 'active'), completed: stringAt(record, 'completed'), sla: stringAt(record, 'sla'), tone: enumAt(record, 'tone', ['success', 'warning']) }
})

const parseReports: ProjectionParser<ReportRecord[]> = (value) => arrayOf(value, (item) => {
  const record = asRecord(item)
  return { title: stringAt(record, 'title'), type: stringAt(record, 'type'), date: stringAt(record, 'date'), state: stringAt(record, 'state'), facts: stringAt(record, 'facts') }
})

const parseServices: ProjectionParser<ServiceStatusRecord[]> = (value) => arrayOf(value, (item) => {
  const record = asRecord(item)
  return { name: stringAt(record, 'name'), detail: stringAt(record, 'detail'), value: stringAt(record, 'value'), status: stringAt(record, 'status'), tone: enumAt(record, 'tone', ['success', 'warning']) }
})

const parseCommandReceipt: ProjectionParser<CommandReceipt> = (value) => {
  const record = asRecord(value, 'INVALID_COMMAND_RESULT')
  const commandType = enumAt(record, 'commandType', ['create_repair_request', 'transition_repair_request', 'append_subsidy_transaction'] as const)
  const status = record.status
  const revision = record.revision
  const transactionId = record.transactionId
  const idempotent = record.idempotent
  if (status !== undefined && !['requested', 'under_review', 'assigned', 'scheduled', 'in_progress', 'repairer_submitted', 'needs_correction', 'center_verified', 'completed', 'reopened', 'rejected', 'cancelled'].includes(String(status))) throw new OperationsRepositoryError('INVALID_COMMAND_RESULT', 'Command response status is unsupported.')
  if (revision !== undefined && (typeof revision !== 'number' || !Number.isSafeInteger(revision) || revision < 1)) throw new OperationsRepositoryError('INVALID_COMMAND_RESULT', 'Command response revision is invalid.')
  if (transactionId !== undefined && typeof transactionId !== 'string') throw new OperationsRepositoryError('INVALID_COMMAND_RESULT', 'Command response transactionId is invalid.')
  if (idempotent !== undefined && typeof idempotent !== 'boolean') throw new OperationsRepositoryError('INVALID_COMMAND_RESULT', 'Command response idempotent is invalid.')
  return { commandType, tenantId: stringAt(record, 'tenantId'), resourceId: stringAt(record, 'resourceId'), eventId: stringAt(record, 'eventId'), ...optional('status', status as DomainRepairStatus | undefined), ...optional('revision', revision as number | undefined), ...optional('transactionId', transactionId as string | undefined), ...optional('idempotent', idempotent as boolean | undefined) }
}

/**
 * Production adapter for server-owned read projections and Domain Command HTTP
 * functions. A non-2xx response, invalid DTO, missing token, or network error
 * is an explicit failure; it never substitutes synthetic data.
 */
export class FirebaseOperationsRepository implements ProductOperationsRepository {
  constructor(private readonly dependencies?: FirebaseOperationsRepositoryDependencies) {}

  loadDashboard() { return this.read('loadDashboard', 'dashboard', parseDashboard) }
  listUsers() { return this.read('listUsers', 'users', parseUsers) }
  listDevices() { return this.read('listDevices', 'devices', parseDevices) }
  listRepairs() { return this.read('listRepairs', 'repairs', parseRepairs) }
  listLedger() { return this.read('listLedger', 'ledger', parseLedger) }
  listInspections() { return this.read('listInspections', 'inspections', parseInspections) }
  listPartners() { return this.read('listPartners', 'partners', parsePartners) }
  listReports() { return this.read('listReports', 'reports', parseReports) }
  listServices() { return this.read('listServices', 'services', parseServices) }

  async advanceRepair(_command: RepairAdvanceCommand): Promise<RepairTransitionResult> {
    throw new OperationsRepositoryError('INVALID_COMMAND', 'The production console must collect an explicit target status, current revision, and any required assignment or subsidy fields before it transitions a repair.')
  }

  async transitionRepair(command: RepairTransitionCommand): Promise<CommandReceipt> {
    const body: Record<string, unknown> = {
      repairRequestId: command.repairRequestId,
      expectedRevision: command.expectedRevision,
      toStatus: command.toStatus,
      ...optional('note', command.note),
    }
    if (command.toStatus === 'assigned') {
      body.repairStationId = command.repairStationId
      body.repairerFirebaseUid = command.repairerFirebaseUid
    }
    if (command.toStatus === 'center_verified') {
      body.subsidyDecisionId = command.subsidyDecisionId
      if (command.subsidyAccountId !== undefined) body.subsidyAccountId = command.subsidyAccountId
    }
    return this.command('transitionRepairRequest', body)
  }

  async appendSubsidyTransaction(command: SubsidyCommand): Promise<CommandReceipt> {
    return this.command('appendSubsidyTransaction', {
      accountId: command.accountId,
      personId: command.personId,
      policyVersionId: command.policyVersionId,
      transactionType: command.transactionType,
      amountKrw: command.amountKrw,
      reasonCode: command.reasonCode,
      ...optional('workOrderId', command.workOrderId),
      ...optional('reversesTransactionId', command.reversesTransactionId),
      ...optional('note', command.note),
    })
  }

  private configuration(operation: string): FirebaseOperationsRepositoryDependencies {
    if (!this.dependencies) throw new NotConfiguredError(operation)
    return this.dependencies
  }

  private async read<T>(operation: string, projection: keyof OperationsApiEndpoints['projections'], parse: ProjectionParser<T>): Promise<T> {
    const config = this.configuration(operation)
    const endpoints = this.endpoints(config, operation)
    return this.request<T>(endpoints.projections[projection], 'GET', undefined, parse, operation)
  }

  private async command(endpoint: 'transitionRepairRequest' | 'appendSubsidyTransaction', body: Record<string, unknown>): Promise<CommandReceipt> {
    const config = this.configuration(endpoint)
    const endpoints = this.endpoints(config, endpoint)
    return this.request(endpoints[endpoint], 'POST', { tenantId: config.tokens.tenantId, ...body }, parseCommandReceipt, endpoint, this.idempotencyKey(config))
  }

  private endpoints(config: FirebaseOperationsRepositoryDependencies, operation: string): OperationsApiEndpoints {
    if (config.endpoints) return config.endpoints
    if (config.baseUrl) return createOperationsApiEndpoints({ baseUrl: config.baseUrl, commandBaseUrl: config.commandBaseUrl })
    throw new NotConfiguredError(operation)
  }

  private async request<T>(url: string, method: 'GET' | 'POST', body: Record<string, unknown> | undefined, parse: ProjectionParser<T>, operation: string, idempotencyKey?: string): Promise<T> {
    if (!url) throw new NotConfiguredError(operation)
    const config = this.configuration(operation)
    const [idToken, appCheckToken] = await Promise.all([config.tokens.getIdToken(), config.tokens.getAppCheckToken()])
    if (!idToken) throw new OperationsRepositoryError('AUTH_REQUIRED', 'A Firebase ID token is required before accessing operations data.', 401)
    if (!appCheckToken) throw new OperationsRepositoryError('APP_CHECK_REQUIRED', 'A Firebase App Check token is required before accessing operations data.', 401)
    const headers: Record<string, string> = { Authorization: `Bearer ${idToken}`, 'X-Firebase-AppCheck': appCheckToken, 'X-Tenant-Id': config.tokens.tenantId, Accept: 'application/json' }
    if (body) headers['Content-Type'] = 'application/json'
    if (idempotencyKey) headers['Idempotency-Key'] = idempotencyKey
    let response: Response
    try {
      response = await (config.fetch ?? globalThis.fetch)(url, { method, headers, ...(body ? { body: JSON.stringify(body) } : {}) })
    } catch {
      throw new OperationsRepositoryError('NETWORK_FAILURE', `The ${operation} request could not reach the operations API.`)
    }
    const payload = await response.json().catch(() => undefined)
    if (!response.ok) {
      const error = payload && typeof payload === 'object' && 'error' in payload ? (payload as { error?: unknown }).error : undefined
      const errorRecord = error && typeof error === 'object' ? error as Record<string, unknown> : undefined
      const code = typeof errorRecord?.code === 'string' ? errorRecord.code : 'REQUEST_REJECTED'
      const message = typeof errorRecord?.message === 'string' ? errorRecord.message : `The ${operation} request was rejected.`
      throw new OperationsRepositoryError(code, message, response.status)
    }
    return parse(payload)
  }

  private idempotencyKey(config: FirebaseOperationsRepositoryDependencies): string {
    const key = config.createIdempotencyKey?.() ?? globalThis.crypto?.randomUUID?.()
    if (!key || typeof key !== 'string') throw new OperationsRepositoryError('NOT_CONFIGURED', 'A cryptographically unique idempotency key provider is required for operations commands.')
    return key
  }
}

export type ProductOperationsRepositorySource = 'demo' | 'firebase'

/**
 * Demo remains opt-in by composition, never as an error fallback. When the
 * production source is selected, missing HTTP/token configuration stays a
 * visible `NOT_CONFIGURED` error rather than exposing synthetic records.
 */
export const createProductOperationsRepository = (
  source: ProductOperationsRepositorySource = 'demo',
  dependencies?: FirebaseOperationsRepositoryDependencies,
): ProductOperationsRepository => source === 'firebase'
  ? new FirebaseOperationsRepository(dependencies)
  : new DemoOperationsRepository()
