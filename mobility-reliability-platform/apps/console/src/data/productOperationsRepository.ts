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
}

export type RepairRecord = {
  id: string
  user: string
  device: string
  issue: string
  request: string
  partner: string
  amount: string
  stage: WorkflowStage
  priority: '높음' | '보통' | '낮음'
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
}

export class NotConfiguredError extends Error {
  readonly code = 'NOT_CONFIGURED'

  constructor(operation: string) {
    super(`NOT_CONFIGURED: Firebase Operations Repository is not configured for ${operation}.`)
    this.name = 'NotConfiguredError'
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
  { id: 'MOB-24018', user: '김서윤', model: '나래 EV-2', health: '양호', battery: '78%', mileage: '1,284 km', inspection: '2026. 09. 03', state: '정상' },
  { id: 'MOB-23991', user: '박정호', model: '오르빗 S1', health: '점검 권장', battery: '42%', mileage: '2,810 km', inspection: '2026. 08. 16', state: '주의' },
  { id: 'MOB-23874', user: '이경자', model: '나래 EV-2', health: '데이터 부족', battery: '61%', mileage: '—', inspection: '2026. 08. 14', state: '대기' },
  { id: 'MOB-23703', user: '최민수', model: '모빌리티 K3', health: '양호', battery: '88%', mileage: '948 km', inspection: '2026. 09. 22', state: '정상' },
]

const demoRepairs: RepairRecord[] = [
  { id: 'SR-2026-081', user: '박정호', device: 'MOB-23991', issue: '주행 중 좌측 쏠림', request: '오늘 08:35', partner: '미배정', amount: '₩180,000', stage: 'new', priority: '높음' },
  { id: 'SR-2026-079', user: '이경자', device: 'MOB-23874', issue: '충전 단자 접촉 불량', request: '어제 16:10', partner: '한마음 모빌리티', amount: '₩95,000', stage: 'assigned', priority: '보통' },
  { id: 'SR-2026-074', user: '최민수', device: 'MOB-23703', issue: '등받이 고정 레버 교체', request: '08. 10. 11:22', partner: '케어휠 수리소', amount: '₩72,000', stage: 'submitted', priority: '보통' },
  { id: 'SR-2026-069', user: '김서윤', device: 'MOB-24018', issue: '타이어 마모 점검', request: '08. 08. 14:05', partner: '한마음 모빌리티', amount: '₩58,000', stage: 'verified', priority: '낮음' },
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

    const updated = { ...repair, stage: nextStage, partner: nextStage === 'assigned' ? '한마음 모빌리티' : repair.partner }
    this.repairs = this.repairs.map((item) => item.id === updated.id ? updated : item)
    return { repair: clone(updated), repairs: clone(this.repairs), ledger: clone(this.ledger), nextStage }
  }
}

/** Firebase adapter seam. It will call the Domain Command API once configured;
 * it deliberately contains no Firestore client and performs no direct writes. */
export class FirebaseOperationsRepository implements ProductOperationsRepository {
  private fail<T>(operation: string): Promise<T> { return Promise.reject(new NotConfiguredError(operation)) }
  loadDashboard() { return this.fail<DashboardData>('loadDashboard') }
  listUsers() { return this.fail<UserRecord[]>('listUsers') }
  listDevices() { return this.fail<DeviceRecord[]>('listDevices') }
  listRepairs() { return this.fail<RepairRecord[]>('listRepairs') }
  listLedger() { return this.fail<LedgerEntry[]>('listLedger') }
  listInspections() { return this.fail<InspectionRecord[]>('listInspections') }
  listPartners() { return this.fail<PartnerRecord[]>('listPartners') }
  listReports() { return this.fail<ReportRecord[]>('listReports') }
  listServices() { return this.fail<ServiceStatusRecord[]>('listServices') }
  advanceRepair(_command: RepairAdvanceCommand) { return this.fail<RepairTransitionResult>('advanceRepair') }
}

export const createProductOperationsRepository = (): ProductOperationsRepository => new DemoOperationsRepository()
