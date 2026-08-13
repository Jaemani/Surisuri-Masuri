import { useEffect, useMemo, useState } from 'react'
import reliabilityComparisonArtifact from './data/reliabilityComparisonArtifact.json'
import {
  createProductOperationsRepository,
  runCenterVerificationAndSubsidyExecution,
  type FundingAuthorityResult,
  OperationsRepositoryError,
  type DashboardData,
  type DeviceRecord,
  type LedgerEntry,
  type RepairRecord,
  type RepairAdvanceCommand,
  type RepairTransitionResult,
  type UserRecord,
  type WorkflowStage,
} from './data/productOperationsRepository'

type IconName = 'home' | 'users' | 'device' | 'repair' | 'check' | 'money' | 'partner' | 'report' | 'system' | 'search' | 'bell' | 'arrow' | 'more' | 'shield' | 'chevron'
type PageKey = 'home' | 'users' | 'devices' | 'repairs' | 'inspections' | 'subsidy' | 'partners' | 'reports' | 'system'

const iconPaths: Record<IconName, string> = {
  home: 'M3 10.5 12 3l9 7.5v9a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 19.5z M9 21v-6h6v6',
  users: 'M16 20v-1.7a3.3 3.3 0 0 0-3.3-3.3H6.3A3.3 3.3 0 0 0 3 18.3V20 M9.5 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8 M17 8a3.5 3.5 0 1 1-1.2 6.8',
  device: 'M7 2.8h10A1.8 1.8 0 0 1 18.8 4.6v14.8a1.8 1.8 0 0 1-1.8 1.8H7a1.8 1.8 0 0 1-1.8-1.8V4.6A1.8 1.8 0 0 1 7 2.8z M9 17.8h6',
  repair: 'M14.2 5.2a4.1 4.1 0 0 0-5.7 5.7L3.2 16.2a2 2 0 1 0 2.8 2.8l5.3-5.3a4.1 4.1 0 0 0 5.7-5.7l-2.7 2.7-2.8-2.8z',
  check: 'm5 12 4.2 4.2L19.5 6',
  money: 'M3 6.5h18v13H3z M3 10h18 M7 15h.01 M12 15h5',
  partner: 'M4 19.5V8.8L12 4l8 4.8v10.7 M8 19.5v-5h8v5 M2.5 19.5h19',
  report: 'M5 3h14v18H5z M8 8h8 M8 12h8 M8 16h5',
  system: 'M12 8.5a3.5 3.5 0 1 0 0 7 3.5 3.5 0 0 0 0-7z M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1-1.8 1.8-.1-.1a1.7 1.7 0 0 0-1.9-.3 1.7 1.7 0 0 0-1 1.6v.2h-2.5V20a1.7 1.7 0 0 0-1-1.6 1.7 1.7 0 0 0-1.9.3l-.1.1-1.8-1.8.1-.1a1.7 1.7 0 0 0 .3-1.9 1.7 1.7 0 0 0-1.6-1H6v-2.5h.2a1.7 1.7 0 0 0 1.6-1 1.7 1.7 0 0 0-.3-1.9l-.1-.1L9.2 7l.1.1a1.7 1.7 0 0 0 1.9.3 1.7 1.7 0 0 0 1-1.6v-.2h2.5v.2a1.7 1.7 0 0 0 1 1.6 1.7 1.7 0 0 0 1.9-.3l.1-.1 1.8 1.8-.1.1a1.7 1.7 0 0 0-.3 1.9 1.7 1.7 0 0 0 1.6 1h.2v2.5h-.2a1.7 1.7 0 0 0-1.3.7z',
  search: 'm20 20-4.8-4.8 M10.8 17a6.2 6.2 0 1 1 0-12.4 6.2 6.2 0 0 1 0 12.4z',
  bell: 'M18 9a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9 M10 21h4',
  arrow: 'M5 12h13 M14 7l5 5-5 5',
  more: 'M5 12h.01 M12 12h.01 M19 12h.01',
  shield: 'M12 3 20 6v5c0 5-3.5 8.2-8 10-4.5-1.8-8-5-8-10V6z M9 12l2 2 4-4',
  chevron: 'm9 18 6-6-6-6',
}

function Icon({ name, size = 18 }: { name: IconName; size?: number }) {
  return <svg className="icon" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d={iconPaths[name]} /></svg>
}

const navigation: { key: PageKey; label: string; icon: IconName; count?: number }[] = [
  { key: 'home', label: '오늘 할 일', icon: 'home' },
  { key: 'users', label: '이용자 · 보호자', icon: 'users' },
  { key: 'devices', label: '기기 관리', icon: 'device', count: 4 },
  { key: 'repairs', label: '수리 운영', icon: 'repair', count: 7 },
  { key: 'inspections', label: '예방점검', icon: 'check', count: 12 },
  { key: 'subsidy', label: '지원금 원장', icon: 'money' },
  { key: 'partners', label: '파트너', icon: 'partner' },
  { key: 'reports', label: '보고서', icon: 'report' },
]

const pageTitles: Record<PageKey, { title: string; eyebrow: string; description: string }> = {
  home: { title: '오늘 할 일', eyebrow: 'OPERATIONS / 2026. 08. 13', description: '기관 운영에 필요한 우선순위를 한눈에 확인하세요.' },
  users: { title: '이용자 · 보호자', eyebrow: 'PEOPLE / ACTIVE DIRECTORY', description: '기관에 등록된 이용자와 보호자 연결 상태를 관리합니다.' },
  devices: { title: '기기 관리', eyebrow: 'ASSETS / DEVICE HEALTH', description: '배정된 기기의 현재 상태와 점검 이력을 확인합니다.' },
  repairs: { title: '수리 운영', eyebrow: 'SERVICE DESK / REPAIR FLOW', description: '접수부터 센터 검증까지 수리 흐름을 관리합니다.' },
  inspections: { title: '예방점검', eyebrow: 'RELIABILITY / PREVENTION', description: '데이터 근거와 함께 점검 우선순위를 정리합니다.' },
  subsidy: { title: '지원금 원장', eyebrow: 'FINANCE / AUDIT LEDGER', description: '수리비 예약과 집행을 투명한 원장으로 추적합니다.' },
  partners: { title: '파트너', eyebrow: 'NETWORK / REPAIR PARTNERS', description: '기관과 협력 수리소의 업무 현황을 확인합니다.' },
  reports: { title: '보고서', eyebrow: 'INSIGHTS / EXPORTS', description: '기관 양식과 근거가 연결된 운영 보고서를 만듭니다.' },
  system: { title: '시스템', eyebrow: 'CONTROL PLANE / STATUS', description: '수집·동기화·권한·데이터 품질 상태를 점검합니다.' },
}

const workflowLabels: Record<WorkflowStage, string> = { new: '새 요청', assigned: '파트너 배정', submitted: '수리 제출', verified: '센터 검증' }
const workflowOrder: WorkflowStage[] = ['new', 'assigned', 'submitted', 'verified']

type RepairAdvanceDetails = Pick<RepairAdvanceCommand, 'repairStationId' | 'repairerFirebaseUid' | 'note'>
type AdvanceRepair = (repairId: string, details?: RepairAdvanceDetails) => Promise<RepairTransitionResult>

function StatusPill({ children, tone = 'neutral' }: { children: React.ReactNode; tone?: 'neutral' | 'success' | 'warning' | 'danger' | 'info' | 'muted' }) {
  return <span className={`status-pill ${tone}`}>{children}</span>
}

function Avatar({ name, color = 'blue' }: { name: string; color?: string }) {
  return <span className={`avatar ${color}`}>{name.slice(0, 1)}</span>
}

function SectionHeading({ title, action, onAction }: { title: string; action?: string; onAction?: () => void }) {
  return <div className="section-heading"><h2>{title}</h2>{action && <button className="text-button" onClick={onAction}>{action}<Icon name="arrow" size={15} /></button>}</div>
}

function App() {
  const repository = useMemo(() => createProductOperationsRepository(), [])
  const [page, setPage] = useState<PageKey>('home')
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const [dashboard, setDashboard] = useState<DashboardData | null>(null)
  const [users, setUsers] = useState<UserRecord[]>([])
  const [devices, setDevices] = useState<DeviceRecord[]>([])
  const [repairs, setRepairs] = useState<RepairRecord[]>([])
  const [ledger, setLedger] = useState<LedgerEntry[]>([])
  const [selectedRepair, setSelectedRepair] = useState('')
  const [toast, setToast] = useState('')
  const [search, setSearch] = useState('')
  const notify = (message: string) => { setToast(message); window.setTimeout(() => setToast(''), 2600) }

  useEffect(() => {
    void Promise.all([
      repository.loadDashboard(),
      repository.listUsers(),
      repository.listDevices(),
      repository.listRepairs(),
      repository.listLedger(),
    ]).then(([nextDashboard, nextUsers, nextDevices, nextRepairs, nextLedger]) => {
      setDashboard(nextDashboard)
      setUsers(nextUsers)
      setDevices(nextDevices)
      setRepairs(nextRepairs)
      setLedger(nextLedger)
      setSelectedRepair(nextRepairs[0]?.id ?? '')
    }).catch((error: unknown) => notify(error instanceof Error ? error.message : '운영 데이터를 불러오지 못했습니다.'))
  }, [repository])

  const activeRepair = repairs.find((repair) => repair.id === selectedRepair) ?? repairs[0]
  const filteredUsers = useMemo(() => users.filter((user) => `${user.name}${user.code}${user.device}`.toLowerCase().includes(search.toLowerCase())), [search])

  const navigate = (nextPage: PageKey) => { setPage(nextPage); setSidebarOpen(false); setSearch('') }
  const advanceRepair: AdvanceRepair = async (repairId, details) => {
    const result = await repository.advanceRepair({
      type: 'repair.advance',
      repairId,
      actorId: 'console-demo-operator',
      ...details,
    })
    setRepairs(result.repairs)
    setLedger(result.ledger)
    return result
  }

  const refreshRepairProjection = async () => {
    const [nextRepairs, nextLedger] = await Promise.all([repository.listRepairs(), repository.listLedger()])
    setRepairs(nextRepairs)
    setLedger(nextLedger)
    return nextRepairs
  }

  const verifyRepairFunding = async (repair: RepairRecord, note?: string): Promise<FundingAuthorityResult & { message: string }> => {
    const result = await runCenterVerificationAndSubsidyExecution({ repository, repair, ...(note ? { note } : {}), refresh: refreshRepairProjection })
    if (result.execution === 'executed') return { ...result, message: '센터 검증과 지원금 집행을 각각 기록하고 최신 원장을 확인했습니다.' }
    if (result.reason === 'projection_refresh_failed' || result.reason === 'projection_not_confirmed') return { ...result, message: '센터 검증 명령은 접수됐지만 최신 projection 확인이 지연되어 지원금 집행을 보류했습니다.' }
    return { ...result, message: `센터 검증은 완료됐지만 지원금 집행은 대기 중입니다. ${result.error ? repairCommandError(result.error) : '불변 원장 거래가 projection에 나타난 뒤 다시 확인해 주세요.'}` }
  }

  const handleDashboardAdvance = () => {
    if (!activeRepair) return
    if (activeRepair.stage === 'verified') return
    if (activeRepair.stage === 'new' || activeRepair.stage === 'assigned' || activeRepair.stage === 'submitted') {
      setPage('repairs')
      return
    }
    void advanceRepair(activeRepair.id).then((result) => {
      notify(result.nextStage ? `${activeRepair.id} · ${workflowLabels[result.nextStage]} 단계로 이동했습니다.` : '이미 센터 검증이 완료된 요청입니다.')
    }).catch((error: unknown) => notify(error instanceof Error ? error.message : '수리 요청을 변경하지 못했습니다.'))
  }

  if (!dashboard || !activeRepair) return <div className="app-shell"><main className="main-area"><div className="content-wrap"><p className="intro-copy">운영 데이터를 불러오는 중…</p></div></main></div>

  return <div className="app-shell">
    <aside className={`sidebar ${sidebarOpen ? 'open' : ''}`}>
      <div className="brand"><div className="brand-mark"><span /></div><div><strong>모두의 이동</strong><small>기관 운영 콘솔</small></div></div>
      <div className="demo-chip"><span className="demo-dot" /> DEMO · SYNTHETIC DATA</div>
      <nav className="side-nav" aria-label="주 메뉴">
        <p className="nav-label">운영</p>
        {navigation.slice(0, 5).map((item) => <NavItem key={item.key} item={item} active={page === item.key} onClick={() => navigate(item.key)} />)}
        <p className="nav-label second">관리</p>
        {navigation.slice(5).map((item) => <NavItem key={item.key} item={item} active={page === item.key} onClick={() => navigate(item.key)} />)}
      </nav>
      <div className="sidebar-bottom"><div className="help-card"><div className="help-icon">?</div><div><strong>운영 지원이 필요하신가요?</strong><span>도움말 센터 열기</span></div><Icon name="arrow" size={16} /></div><div className="profile"><Avatar name="김" color="navy" /><div><strong>김은정</strong><span>기관 운영자</span></div><Icon name="more" size={18} /></div></div>
    </aside>
    {sidebarOpen && <button className="sidebar-backdrop" aria-label="메뉴 닫기" onClick={() => setSidebarOpen(false)} />}
    <main className="main-area">
      <header className="topbar"><button className="mobile-menu" onClick={() => setSidebarOpen(true)} aria-label="메뉴 열기"><span /><span /><span /></button><div className="breadcrumb"><span>서울서부 복지관</span><Icon name="chevron" size={14} /><strong>{pageTitles[page].title}</strong></div><div className="top-actions"><button className="icon-button search-toggle" aria-label="검색"><Icon name="search" size={19} /></button><div className="notification"><button className="icon-button" aria-label="알림"><Icon name="bell" size={19} /></button><span /></div><div className="top-divider" /><div className="top-user"><Avatar name="김" color="navy" /><span>김은정</span><Icon name="chevron" size={14} /></div></div></header>
      <div className="content-wrap">
        <div className="demo-banner"><div className="banner-symbol"><Icon name="shield" size={18} /></div><div><strong>데모 환경 · 합성 데이터</strong><span>화면에 표시되는 이용자·기기·금액은 모두 실제 운영 데이터가 아닌 시연용 데이터입니다.</span></div><button onClick={() => notify('데모 환경 안내를 확인했습니다.')}>확인</button></div>
        {page === 'home' ? <Dashboard data={dashboard} activeRepair={activeRepair} repairs={repairs} ledger={ledger} selectedRepair={selectedRepair} setSelectedRepair={setSelectedRepair} onAdvance={handleDashboardAdvance} onNavigate={navigate} notify={notify} /> : <GenericPage page={page} search={search} setSearch={setSearch} users={filteredUsers} devices={devices} repairs={repairs} ledger={ledger} onAdvance={advanceRepair} onVerifyFunding={verifyRepairFunding} onRefresh={refreshRepairProjection} onNavigate={navigate} notify={notify} />}
      </div>
    </main>
    {toast && <div className="toast"><span className="toast-check"><Icon name="check" size={14} /></span>{toast}</div>}
  </div>
}

function NavItem({ item, active, onClick }: { item: typeof navigation[number]; active: boolean; onClick: () => void }) {
  return <button className={`nav-item ${active ? 'active' : ''}`} onClick={onClick}><Icon name={item.icon} size={18} /><span>{item.label}</span>{item.count && <em>{item.count}</em>}</button>
}

function Dashboard({ data, activeRepair, repairs, ledger, selectedRepair, setSelectedRepair, onAdvance, onNavigate, notify }: { data: DashboardData; activeRepair: RepairRecord; repairs: RepairRecord[]; ledger: LedgerEntry[]; selectedRepair: string; setSelectedRepair: (id: string) => void; onAdvance: () => void; onNavigate: (page: PageKey) => void; notify: (message: string) => void }) {
  return <>
    <div className="page-intro"><div><p className="eyebrow">{pageTitles.home.eyebrow}</p><h1>좋은 아침이에요, 은정님 <span className="wave">✦</span></h1><p className="intro-copy">오늘 기관에서 확인이 필요한 업무를 정리했어요.</p></div><div className="intro-actions"><button className="date-button">2026. 08. 13 <Icon name="chevron" size={15} /></button><button className="primary-button" onClick={() => notify('새 수리 요청 작성 화면은 데모에서 준비 중입니다.')}><span>＋</span> 새 수리 요청</button></div></div>
    <div className="metric-grid">{data.metrics.map((metric) => <Metric key={metric.label} {...metric} onClick={() => onNavigate(metric.icon === 'device' ? 'devices' : metric.icon === 'repair' ? 'repairs' : metric.icon === 'check' ? 'inspections' : metric.icon === 'money' ? 'subsidy' : 'system')} />)}</div>
    <div className="dashboard-grid top-grid"><section className="panel attention-panel"><SectionHeading title="지금 확인이 필요한 일" action="전체 보기" onAction={() => onNavigate('repairs')} /><div className="attention-list">{data.attention.map((item) => <Attention key={item.title} {...item} onClick={() => onNavigate(item.destination)} />)}</div></section><section className="panel week-panel"><SectionHeading title="이번 주 운영 현황" action="보고서 보기" onAction={() => onNavigate('reports')} /><div className="week-chart"><div className="chart-y"><span>12</span><span>8</span><span>4</span><span>0</span></div><div className="chart-area"><div className="grid-line line-1" /><div className="grid-line line-2" /><div className="grid-line line-3" /><div className="bars">{data.weeklyBars.map((bar) => <Bar key={bar.day} {...bar} />)}</div></div></div><div className="chart-legend"><span><i className="legend-dot completed" />처리 완료</span><span><i className="legend-dot pending" />처리 대기</span><strong>{data.weeklyChange} <small>지난주 대비</small></strong></div></section></div>
    <section className="panel workflow-panel"><div className="workflow-heading"><div><SectionHeading title="수리 요청 진행 현황" action="수리 운영 열기" onAction={() => onNavigate('repairs')} /><p>접수부터 센터 검증까지, 한 건의 흐름을 놓치지 않도록 관리하세요.</p></div><button className="ghost-button" onClick={() => notify('필터: 최근 30일을 적용했습니다.')}>최근 30일 <Icon name="chevron" size={14} /></button></div><div className="workflow-board">{workflowOrder.map((stage) => <div className={`workflow-column ${stage === activeRepair.stage ? 'selected-column' : ''}`} key={stage}><div className="column-title"><span className={`column-marker ${stage}`} />{workflowLabels[stage]}<b>{repairs.filter((repair) => repair.stage === stage).length}</b></div>{repairs.filter((repair) => repair.stage === stage).map((repair) => <button className={`repair-card ${selectedRepair === repair.id ? 'selected' : ''}`} key={repair.id} onClick={() => setSelectedRepair(repair.id)}><div className="repair-card-top"><span>{repair.id}</span><StatusPill tone={repair.priority === '높음' ? 'danger' : repair.priority === '낮음' ? 'muted' : 'neutral'}>{repair.priority}</StatusPill></div><strong>{repair.issue}</strong><div className="repair-person"><Avatar name={repair.user} color={repair.user === '박정호' ? 'blue' : repair.user === '이경자' ? 'lilac' : 'mint'} /><span>{repair.user}</span><small>{repair.request}</small></div><div className="repair-card-footer"><span>{repair.partner === '미배정' ? <em className="unassigned">파트너 미배정</em> : repair.partner}</span><span>{repair.amount}</span></div></button>)}</div>)}</div></section>
    <div className="dashboard-grid bottom-grid"><section className="panel selected-panel"><SectionHeading title="선택한 요청" action="상세 보기" onAction={() => onNavigate('repairs')} /><div className="selected-summary"><div className="selected-title"><div className="request-avatar"><Icon name="repair" size={20} /></div><div><div className="request-id">{activeRepair.id} <StatusPill tone={activeRepair.stage === 'verified' ? 'success' : 'warning'}>{workflowLabels[activeRepair.stage]}</StatusPill></div><h3>{activeRepair.issue}</h3><p>{activeRepair.user} · {activeRepair.device}</p></div></div><div className="stage-line">{workflowOrder.map((stage, index) => <div className={`stage-step ${workflowOrder.indexOf(activeRepair.stage) >= index ? 'done' : ''}`} key={stage}><span>{workflowOrder.indexOf(activeRepair.stage) > index ? '✓' : index + 1}</span><small>{workflowLabels[stage]}</small>{index < workflowOrder.length - 1 && <i />}</div>)}</div><div className="action-row"><button className="primary-button compact" onClick={onAdvance} disabled={activeRepair.stage === 'verified'}>{activeRepair.stage === 'new' ? '파트너 배정하기' : activeRepair.stage === 'assigned' ? '수리사 처리 대기' : activeRepair.stage === 'submitted' ? '센터 검증 정보 입력' : '검증 완료됨'} {activeRepair.stage !== 'assigned' && activeRepair.stage !== 'verified' && <Icon name="arrow" size={15} />}</button><button className="icon-button bordered" aria-label="더 보기" onClick={() => notify('감사 로그와 요청 메모를 준비 중입니다.')}><Icon name="more" size={18} /></button></div></div></section><section className="panel ledger-panel"><SectionHeading title="최근 지원금 원장" action="전체 원장" onAction={() => onNavigate('subsidy')} /><div className="ledger-list">{ledger.map((item) => <div className="ledger-row" key={item.transactionId}><div className="ledger-date">{item.date}<small>{item.id}</small></div><div className="ledger-main"><strong>{item.item}</strong><span>{item.user} · {item.actor}</span></div><div className="ledger-amount"><strong>{item.amount}</strong><StatusPill tone={item.state === '예약' ? 'warning' : item.state === '집행 완료' ? 'success' : 'muted'}>{item.state}</StatusPill></div></div>)}</div><div className="ledger-foot"><span>이번 달 누적 집행</span><strong>₩2,480,000 <small>/ ₩4,000,000</small></strong></div></section></div>
  </>
}

function Metric({ label, value, suffix, trend, tone, icon, onClick }: { label: string; value: string; suffix: string; trend: string; tone: string; icon: IconName; onClick: () => void }) {
  return <button className="metric-card" onClick={onClick}><div className={`metric-icon ${tone}`}><Icon name={icon} size={19} /></div><div className="metric-copy"><span>{label}</span><strong>{value}<small>{suffix}</small></strong><em>{trend}</em></div><Icon name="arrow" size={16} /></button>
}

function Attention({ icon, color, title, description, time, action, onClick }: { icon: IconName; color: string; title: string; description: string; time: string; action: string; onClick: () => void }) {
  return <button className="attention-row" onClick={onClick}><div className={`attention-icon ${color}`}><Icon name={icon} size={17} /></div><div className="attention-copy"><strong>{title}</strong><span>{description}</span></div><div className="attention-meta"><small>{time}</small><b>{action}<Icon name="arrow" size={14} /></b></div></button>
}

function Bar({ day, value, active = false, muted = false }: { day: string; value: number; active?: boolean; muted?: boolean }) {
  return <div className="bar-group"><div className={`bar ${active ? 'active' : ''} ${muted ? 'muted' : ''}`} style={{ height: `${Math.max(value * 8, 9)}%` }}><span>{value}</span></div><small>{day}</small></div>
}

function GenericPage({ page, search, setSearch, users: filteredUsers, devices, repairs, ledger, onAdvance, onVerifyFunding, onRefresh, onNavigate, notify }: { page: PageKey; search: string; setSearch: (value: string) => void; users: UserRecord[]; devices: DeviceRecord[]; repairs: RepairRecord[]; ledger: LedgerEntry[]; onAdvance: AdvanceRepair; onVerifyFunding: (repair: RepairRecord, note?: string) => Promise<FundingAuthorityViewResult>; onRefresh: () => Promise<unknown>; onNavigate: (page: PageKey) => void; notify: (message: string) => void }) {
  const meta = pageTitles[page]
  if (page === 'repairs') return <RepairOperations repairs={repairs} onAdvance={onAdvance} onVerifyFunding={onVerifyFunding} onRefresh={async () => { await onRefresh() }} onNavigate={onNavigate} />
  if (page === 'subsidy') return <SubsidyPage ledger={ledger} notify={notify} />
  if (page === 'devices') return <DevicesPage devices={devices} onNavigate={onNavigate} notify={notify} />
  if (page === 'reports') return <ReportsPage />
  const action = page === 'users' ? '이용자 등록' : page === 'partners' ? '파트너 추가' : undefined
  return <><div className="page-intro inner"><div><p className="eyebrow">{meta.eyebrow}</p><h1>{meta.title}</h1><p className="intro-copy">{meta.description}</p></div><div className="intro-actions">{action && <button className="primary-button" onClick={() => notify(`${action} 기능은 데모에서 준비 중입니다.`)}><span>＋</span> {action}</button>}</div></div><div className="toolbar"><div className="search-field"><Icon name="search" size={17} /><input value={search} onChange={(event) => setSearch(event.target.value)} placeholder={`${meta.title} 검색`} /></div><button className="filter-button" onClick={() => notify('필터 메뉴를 열었습니다.')}>필터 <span>⌄</span></button><button className="filter-button" onClick={() => notify('내보내기 준비 중입니다.')}>내보내기 <Icon name="arrow" size={14} /></button></div>{page === 'users' ? <UsersTable users={filteredUsers} notify={notify} /> : page === 'inspections' ? <InspectionsTable notify={notify} /> : page === 'partners' ? <PartnersTable notify={notify} /> : <SystemStatus notify={notify} />}</>
}

function TableShell({ children, headers }: { children: React.ReactNode; headers: string[] }) {
  return <section className="panel table-panel"><div className="table-header-row">{headers.map((header) => <span key={header}>{header}</span>)}</div>{children}</section>
}

function UsersTable({ users: filteredUsers, notify }: { users: UserRecord[]; notify: (message: string) => void }) {
  return <TableShell headers={['이용자', '연결 상태', '배정 기기', '마지막 활동', '운영 상태', '']}><div className="table-body">{filteredUsers.map((user) => <div className="data-row" key={user.code}><div className="person-cell"><Avatar name={user.name} color={user.color} /><div><strong>{user.name}</strong><span>{user.code}</span></div></div><div>{user.relation === '본인' ? <StatusPill tone="info">본인 인증</StatusPill> : <StatusPill tone="neutral">보호자 연결</StatusPill>}</div><div className="muted-cell">{user.device}</div><div className="muted-cell">{user.last}</div><div><StatusPill tone={user.status === '정상' ? 'success' : user.status === '점검 권장' ? 'warning' : 'muted'}>{user.status}</StatusPill></div><button className="row-more" onClick={() => notify(`${user.name}님의 상세 정보를 열었습니다.`)}><Icon name="more" size={18} /></button></div>)}</div><div className="table-footer">전체 24명 중 5명 표시 <span>1 / 5</span></div></TableShell>
}

function DevicesPage({ devices, onNavigate, notify }: { devices: DeviceRecord[]; onNavigate: (page: PageKey) => void; notify: (message: string) => void }) {
  const [selectedId, setSelectedId] = useState(devices[0]?.id ?? '')
  const [query, setQuery] = useState('')
  const filteredDevices = devices.filter((device) => `${device.id}${device.user}${device.model}`.toLowerCase().includes(query.toLowerCase()))
  const device = devices.find((item) => item.id === selectedId) ?? devices[0]

  useEffect(() => {
    if (devices.length && !devices.some((item) => item.id === selectedId)) setSelectedId(devices[0].id)
  }, [devices, selectedId])

  if (!device) return <div className="panel empty-state">표시할 기기가 없습니다.</div>

  return <>
    <div className="page-intro inner"><div><p className="eyebrow">{pageTitles.devices.eyebrow}</p><h1>기기 관리</h1><p className="intro-copy">현재 상태와 센터가 검증한 수리 이력을 함께 보고 다음 운영 조치를 정합니다.</p></div><div className="intro-actions"><button className="primary-button" onClick={() => notify('기기 등록 기능은 데모에서 준비 중입니다.')}><span>＋</span> 기기 등록</button></div></div>
    <div className="toolbar"><div className="search-field"><Icon name="search" size={17} /><label className="sr-only" htmlFor="device-search">기기 검색</label><input id="device-search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="기기·이용자·모델 검색" /></div><button className="filter-button" onClick={() => notify('기기 상태 필터를 열었습니다.')}>상태 필터 <span>⌄</span></button><span className="projection-note device-projection-note">합성 상태 · 검증 완료 수리 이력만 표시</span></div>
    <div className="device-layout"><section className="panel device-list-panel" aria-label="기기 목록"><div className="panel-title-row"><div><h2>배정 기기</h2><p>{filteredDevices.length}대 · 현재 이용자 기준</p></div><StatusPill tone="info">기관 자산</StatusPill></div><div className="device-list">{filteredDevices.map((item) => <button className={`device-list-row ${device.id === item.id ? 'selected' : ''}`} key={item.id} onClick={() => setSelectedId(item.id)} aria-pressed={device.id === item.id} aria-label={`${item.id} 상세 보기`}><div className="device-thumb"><Icon name="device" size={17} /></div><div className="device-list-copy"><strong>{item.id}</strong><span>{item.user} · {item.model}</span><small>{item.mileage} 사용 · 다음 점검 {item.inspection}</small></div><div className="device-list-meta"><StatusPill tone={item.state === '정상' ? 'success' : item.state === '주의' ? 'warning' : 'muted'}>{item.state}</StatusPill><span>{item.battery}</span></div><Icon name="chevron" size={16} /></button>)}</div>{!filteredDevices.length && <div className="empty-state">검색 조건에 맞는 기기가 없습니다.</div>}</section>
      <section className="panel device-detail-panel" aria-live="polite"><div className="detail-kicker"><span>DEVICE RECORD · VERIFIED HISTORY</span><StatusPill tone={device.state === '정상' ? 'success' : device.state === '주의' ? 'warning' : 'muted'}>{device.state}</StatusPill></div><div className="detail-title"><div className="device-thumb large"><Icon name="device" size={22} /></div><div><p>{device.id}</p><h2>{device.model}</h2><span>{device.user}님에게 배정 · {device.health}</span></div></div><div className="device-facts" aria-label="기기 현재 상태"><div><span>배터리</span><strong>{device.battery}</strong><div className="battery detail-battery"><span><i style={{ width: device.battery }} /></span></div></div><div><span>누적 사용량</span><strong>{device.mileage}</strong><small>모바일 기록 기반 합성값</small></div><div><span>다음 점검</span><strong>{device.inspection}</strong><small>현재 운영 일정</small></div></div><div className="detail-block"><div className="device-section-title"><div><h3>기기 타임라인</h3><p>센터 검증을 마친 완료 수리만 재생합니다.</p></div><Icon name="shield" size={17} /></div><div className="verified-timeline">{device.timeline.length ? device.timeline.map((event) => <article className={`verified-event ${event.tone}`} key={event.id}><span className="event-marker"><Icon name={event.tone === 'success' ? 'check' : event.tone === 'warning' ? 'repair' : 'device'} size={13} /></span><div><time>{event.date}</time><strong>{event.title}</strong><p>{event.detail}</p></div></article>) : <div className="timeline-empty"><Icon name="shield" size={17} /><div><strong>검증 완료된 수리 이력이 없습니다.</strong><p>진행 중 요청은 수리 운영에서 따로 확인하세요.</p></div></div>}</div></div><div className="device-action-panel"><div><span>다음 운영 조치</span><strong>{device.state === '주의' ? '예방점검 우선순위를 확인하세요.' : '현재 점검 일정을 유지하세요.'}</strong></div><div><button className="primary-button compact" onClick={() => onNavigate('inspections')}>예방점검 열기 <Icon name="arrow" size={15} /></button><button className="ghost-button" onClick={() => onNavigate('repairs')}>수리 운영</button></div></div></section></div>
  </>
}

function InspectionsTable({ notify }: { notify: (message: string) => void }) {
  const rows = [
    { id: 'INS-DEMO-01', user: '박정호', device: 'MOB-23991', due: '오늘 확인', source: '이용자 접수', reason: '“주행 중 좌측 쏠림”으로 접수된 요청', status: '담당자 검토', tone: 'warning' as const, evidence: ['접수된 수리 요청 1건', '센터 검증 전 사용자 진술'], missing: '수리사 점검 결과와 부품 linkage', action: '수리 요청에서 확인' },
    { id: 'INS-DEMO-02', user: '이경자', device: 'MOB-23874', due: '판정 없음', source: '데이터 품질', reason: '최근 주행 요약을 확인할 수 없음', status: '판단 유보', tone: 'muted' as const, evidence: ['마지막 동기화 3일 전', '거리 기준선 입력 미충족'], missing: 'decision-time 거리 요약', action: '동기화 상태 확인' },
    { id: 'INS-DEMO-03', user: '윤옥순', device: 'MOB-23218', due: '08. 18', source: '운영 일정', reason: '센터가 등록한 정기점검일 도래', status: '일정 확인', tone: 'info' as const, evidence: ['등록된 점검 일정 2026. 08. 18'], missing: '모델 판정 없음 · 일정 근거만 사용', action: '일정 검토' },
    { id: 'INS-DEMO-04', user: '최민수', device: 'MOB-23703', due: '09. 22', source: '검증 이력', reason: '검증된 부품 교체 후 운영 일정 유지', status: '일정 유지', tone: 'success' as const, evidence: ['완료 수리 1건', '센터 등록 점검일 2026. 09. 22'], missing: '실제 reliability field metric 없음', action: '기기 이력 확인' },
  ]
  const [selectedId, setSelectedId] = useState(rows[0].id)
  const selected = rows.find((row) => row.id === selectedId) ?? rows[0]
  return <div className="inspection-workspace"><section className="inspection-summary" aria-label="예방점검 근거 상태 요약"><div><span>오늘 운영 검토</span><strong>1<small>건</small></strong><p>사용자 접수 · 모델 판정 아님</p></div><div><span>판단 유보</span><strong>1<small>건</small></strong><p>근거가 채워질 때까지 숫자 미표시</p></div><div><span>등록된 일정</span><strong>2<small>건</small></strong><p>센터 운영 일정 기준</p></div></section><div className="inspection-layout"><section className="panel inspection-list-panel" aria-label="예방점검 검토 목록"><div className="panel-title-row"><div><h2>근거별 검토함</h2><p>합성 데모 · 우선순위 예측이 아닙니다</p></div><StatusPill tone="info">R10 설명 UI</StatusPill></div><div className="inspection-list">{rows.map((row) => <button key={row.id} className={`inspection-list-row ${selected.id === row.id ? 'selected' : ''}`} onClick={() => setSelectedId(row.id)} aria-pressed={selected.id === row.id}><Avatar name={row.user} color="blue" /><div><strong>{row.user} · {row.device}</strong><span>{row.reason}</span><small>{row.source} · {row.due}</small></div><StatusPill tone={row.tone}>{row.status}</StatusPill><Icon name="chevron" size={16} /></button>)}</div></section><section className="panel inspection-evidence-panel" aria-live="polite"><div className="detail-kicker"><span>EVIDENCE REVIEW · SYNTHETIC DEMO</span><StatusPill tone={selected.tone}>{selected.status}</StatusPill></div><div className="inspection-detail-head"><p>{selected.id}</p><h2>{selected.user}님의 점검 검토</h2><span>{selected.device} · {selected.source}</span></div><div className="decision-boundary"><Icon name="shield" size={19} /><div><strong>{selected.reason}</strong><p>이 화면은 고장 시점이나 안전을 보증하지 않습니다. 확인된 운영 사실과 부족한 근거를 분리해 보여줍니다.</p></div></div><div className="evidence-columns"><div><span>현재 확인된 근거</span>{selected.evidence.map((fact) => <p key={fact}>✓ {fact}</p>)}</div><div><span>아직 필요한 정보</span><p>○ {selected.missing}</p></div></div><div className="inspection-next"><div><span>다음 운영 조치</span><strong>{selected.action}</strong></div><button className="primary-button compact" onClick={() => notify(`${selected.user}님의 ${selected.action} 항목을 열었습니다.`)}>검토 계속하기 <Icon name="arrow" size={15} /></button></div></section></div></div>
}

function PartnersTable({ notify }: { notify: (message: string) => void }) {
  const rows = [{ name: '한마음 모빌리티', contact: '김도현 · 02-321-8842', active: '3건', completed: '18건', sla: '1.4일', tone: 'success' }, { name: '케어휠 수리소', contact: '장유진 · 02-884-2011', active: '2건', completed: '12건', sla: '2.1일', tone: 'warning' }, { name: '서부 보장구 센터', contact: '이재훈 · 02-771-0390', active: '0건', completed: '24건', sla: '1.1일', tone: 'success' }]
  return <TableShell headers={['파트너', '담당자', '진행 중', '최근 완료', '평균 처리', '상태', '']}><div className="table-body">{rows.map((row) => <div className="data-row" key={row.name}><div className="person-cell"><div className="partner-logo">{row.name.slice(0, 1)}</div><div><strong>{row.name}</strong><span>협력 수리 파트너</span></div></div><div className="muted-cell">{row.contact}</div><div className="number-cell">{row.active}</div><div className="number-cell">{row.completed}</div><div className="muted-cell">{row.sla}</div><StatusPill tone={row.tone as 'success' | 'warning'}>{row.tone === 'success' ? '정상' : '확인 필요'}</StatusPill><button className="row-more" onClick={() => notify(`${row.name} 정보를 열었습니다.`)}><Icon name="more" size={18} /></button></div>)}</div></TableShell>
}

function ReportsPage() {
  const battery = reliabilityComparisonArtifact.components.find((component) => component.component === 'battery')
  const controller = reliabilityComparisonArtifact.components.find((component) => component.component === 'controller')
  if (!battery || !controller) throw new Error('R10 reliability presentation is incomplete')
  const percentage = (value: number) => `${(value * 100).toFixed(value === 1 ? 0 : 1)}%`
  const metricList = (method: { sensitivity?: number; specificity?: number; brierScore?: number }) => {
    if (typeof method.sensitivity !== 'number' || typeof method.specificity !== 'number' || typeof method.brierScore !== 'number') return null
    return <dl className="baseline-metrics"><div><dt>Sensitivity</dt><dd>{percentage(method.sensitivity)}</dd></div><div><dt>Specificity</dt><dd>{percentage(method.specificity)}</dd></div><div><dt>Brier score</dt><dd>{method.brierScore.toFixed(3)}</dd></div></dl>
  }
  const baselines = [
    { key: 'fixed', method: battery.methods.fixedInterval, label: '고정주기', eyebrow: 'FIXED INTERVAL', threshold: '180일' },
    { key: 'distance', method: battery.methods.cumulativeDistance, label: '누적거리', eyebrow: 'CUMULATIVE DISTANCE', threshold: '1,000km' },
    { key: 'km', method: battery.methods.kaplanMeier, label: 'KM 기준선', eyebrow: 'KAPLAN–MEIER', threshold: `${reliabilityComparisonArtifact.comparisonContext.horizonDays}일` },
  ]

  return <>
    <div className="page-intro inner"><div><p className="eyebrow">INSIGHTS / R10 SYNTHETIC BASELINE</p><h1>보고서</h1><p className="intro-copy">합성 기준선의 비교 결과와 데이터 경계를 aggregate 수준에서 확인합니다.</p></div><StatusPill tone="muted">검토 전용</StatusPill></div>
    <section className="baseline-demo-banner" aria-label="합성 데이터 전용 안내"><div className="baseline-banner-icon"><Icon name="shield" size={20} /></div><div><strong>SYNTHETIC-ONLY · 배포 보류</strong><p>이 비교는 합성 데이터로만 생성되었습니다. 실제 현장 성능이나 개별 기기·이용자 위험 판정으로 사용할 수 없습니다.</p></div><StatusPill tone="warning">운영 연결 안 함</StatusPill></section>
    <section className="baseline-scope" aria-label="기준선 평가 범위"><div><span>데이터셋</span><strong>{reliabilityComparisonArtifact.datasetVersion}</strong></div><div><span>분할</span><strong>device-group-time holdout</strong></div><div><span>평가 cohort</span><strong>untouched test · {reliabilityComparisonArtifact.components.reduce((total, component) => total + component.sampleCount, 0)} observations</strong></div><div><span>배포 결정</span><strong>{reliabilityComparisonArtifact.deploymentDecision}</strong></div></section>
    <section className="baseline-source-panel" aria-label="학습과 평가 출처"><div><span className="baseline-source train">TRAIN CURVE / RULE</span><h2>학습 기준선</h2><p>Train split에서 점검 간격·거리 threshold와 KM 생존곡선을 고정합니다.</p></div><div><span className="baseline-source test">TEST METRICS</span><h2>테스트 평가 지표</h2><p>동일한 기준선은 untouched test cohort에서만 sensitivity·specificity·Brier를 계산합니다.</p></div></section>
    <section className="baseline-comparison" aria-label="합성 기준선 비교"><div className="baseline-section-heading"><div><p className="eyebrow">AGGREGATE COMPARISON</p><h2>배터리 strata · 세 가지 기준선</h2></div><span>개별 기기·이용자 단위 결과 없음</span></div><div className="baseline-card-grid">{baselines.map((baseline) => <article className="baseline-card" key={baseline.key}><div className="baseline-card-heading"><div><span>{baseline.eyebrow}</span><h3>{baseline.label}</h3></div><StatusPill tone="success">평가됨</StatusPill></div><div className="baseline-threshold"><strong>{baseline.threshold}</strong><span>배터리 synthetic test · n={battery.sampleCount}</span></div>{metricList(baseline.method)}<div className="baseline-card-foot"><span>출처</span><strong>TEST · aggregate</strong></div></article>)}</div></section>
    <section className="controller-abstention" aria-label="컨트롤러 판단 유보"><div className="controller-abstention-heading"><div><span className="baseline-source abstain">CONTROLLER / ABSTENTION</span><h2>컨트롤러는 판단을 유보합니다</h2></div><StatusPill tone="muted">판단 유보</StatusPill></div><p>Train component 표본 {controller.sampleCount}개가 최소 4개 기준에 미달해 세 기준선을 개별 운영 판정으로 연결하지 않습니다. 현재는 고정 점검 주기와 담당자 검토를 유지합니다.</p><div className="abstention-facts"><div><span>사유 코드</span><strong>{controller.methods.fixedInterval.abstentionReason}</strong></div><div><span>운영 fallback</span><strong>고정 점검 일정 유지</strong></div><div><span>허용 상태</span><strong>deployment · {reliabilityComparisonArtifact.deploymentDecision}</strong></div></div></section>
  </>
}

function SystemStatus({ notify }: { notify: (message: string) => void }) {
  const services = [{ name: '모바일 수집', detail: '최근 24시간 수집 성공률', value: '98.7%', status: '정상', tone: 'success' }, { name: '동기화 큐', detail: '미처리 이벤트 14건 · 최대 12분', value: '양호', status: '정상', tone: 'success' }, { name: '상태 projection', detail: '마지막 처리 지연 4분', value: '4분', status: '정상', tone: 'success' }, { name: '보고서 검증', detail: '최근 30일 검증 실패 1건', value: '주의', status: '확인 필요', tone: 'warning' }]
  return <div className="system-grid"><section className="panel system-panel"><SectionHeading title="서비스 상태" action="새로고침" onAction={() => notify('서비스 상태를 새로고침했습니다.')} />{services.map((service) => <div className="service-row" key={service.name}><div className="service-status"><span className={`pulse ${service.tone}`} /><div><strong>{service.name}</strong><span>{service.detail}</span></div></div><strong className="service-value">{service.value}</strong><StatusPill tone={service.tone as 'success' | 'warning'}>{service.status}</StatusPill><Icon name="chevron" size={16} /></div>)}</section><section className="panel privacy-panel"><div className="privacy-art"><Icon name="shield" size={26} /></div><p className="eyebrow">PRIVACY BY DEFAULT</p><h2>원본 이동경로는<br />기본 화면에 표시하지 않아요.</h2><p>콘솔에는 집계·위험 근거·데이터 품질·운영 상태만 표시됩니다. 원본 위치는 목적 제한된 접근과 감사 로그를 거칩니다.</p><button className="text-button" onClick={() => notify('개인정보 보호 원칙을 확인했습니다.')}>보호 원칙 보기 <Icon name="arrow" size={15} /></button></section></div>
}

const syntheticRepairStations = [
  { id: 'station-hanmaeum', label: '한마음 모빌리티 · 합성 station-hanmaeum' },
  { id: 'station-carewheel', label: '케어휠 수리소 · 합성 station-carewheel' },
  { id: 'station-western', label: '서부 보장구 센터 · 합성 station-western' },
]

const syntheticRepairers = [
  { uid: 'demo-repairer-kim', label: '김도현 · 합성 demo-repairer-kim' },
  { uid: 'demo-repairer-jang', label: '장유진 · 합성 demo-repairer-jang' },
  { uid: 'demo-repairer-lee', label: '이재훈 · 합성 demo-repairer-lee' },
]

type RepairCommandState = { status: 'idle' | 'submitting' | 'success' | 'error'; message?: string; conflict?: boolean }
type FundingAuthorityViewResult = FundingAuthorityResult & { message: string }

const repairCommandError = (error: unknown) => {
  if (error instanceof OperationsRepositoryError && error.code === 'REVISION_CONFLICT') return '다른 담당자가 먼저 변경했습니다. 최신 상태를 새로고침한 뒤 다시 시도해 주세요.'
  if (!(error instanceof Error)) return '수리 요청을 변경하지 못했습니다. 다시 확인해 주세요.'
  if (error.message.includes('REVISION_CONFLICT')) return '다른 담당자가 먼저 변경했습니다. 최신 상태를 새로고침한 뒤 다시 시도해 주세요.'
  return error.message || '수리 요청을 변경하지 못했습니다. 다시 확인해 주세요.'
}

function RepairOperations({ repairs, onAdvance, onVerifyFunding, onRefresh, onNavigate }: { repairs: RepairRecord[]; onAdvance: AdvanceRepair; onVerifyFunding: (repair: RepairRecord, note?: string) => Promise<FundingAuthorityViewResult>; onRefresh: () => Promise<void>; onNavigate: (page: PageKey) => void }) {
  const [selected, setSelected] = useState(repairs[0]?.id ?? '')
  const [assignmentDraft, setAssignmentDraft] = useState({ repairStationId: '', repairerFirebaseUid: '', note: '' })
  const [reviewNote, setReviewNote] = useState('')
  const [verification, setVerification] = useState({ repair: false, amount: false, eligibility: false })
  const [commandState, setCommandState] = useState<RepairCommandState>({ status: 'idle' })
  const repair = repairs.find((item) => item.id === selected) ?? repairs[0]

  useEffect(() => {
    if (repairs.length && !repairs.some((item) => item.id === selected)) setSelected(repairs[0].id)
  }, [repairs, selected])

  useEffect(() => {
    setAssignmentDraft({ repairStationId: '', repairerFirebaseUid: '', note: '' })
    setReviewNote('')
    setVerification({ repair: false, amount: false, eligibility: false })
    setCommandState({ status: 'idle' })
  }, [repair?.id])

  if (!repair) return <div className="panel empty-state">표시할 수리 요청이 없습니다.</div>

  const isBusy = commandState.status === 'submitting'
  const submitAdvance = async (details?: RepairAdvanceDetails) => {
    if (isBusy) return
    setCommandState({ status: 'submitting', message: '서버에 변경을 요청하는 중입니다…' })
    try {
      const result = await onAdvance(repair.id, details)
      setCommandState({ status: 'success', message: result.nextStage ? `${workflowLabels[result.nextStage]} 단계로 변경되었습니다.` : '변경 결과를 확인했습니다.' })
    } catch (error) {
      const message = repairCommandError(error)
      setCommandState({ status: 'error', message, conflict: message.includes('새로고침') })
    }
  }

  const submitVerification = async () => {
    if (isBusy) return
    setCommandState({ status: 'submitting', message: '센터 검증 명령을 제출하는 중입니다…' })
    try {
      const result = await onVerifyFunding(repair, reviewNote || undefined)
      setCommandState({ status: result.execution === 'executed' ? 'success' : 'error', message: result.message })
    } catch (error) {
      const message = repairCommandError(error)
      setCommandState({ status: 'error', message, conflict: message.includes('새로고침') })
    }
  }

  const refreshAfterConflict = async () => {
    setCommandState({ status: 'submitting', message: '최신 projection을 불러오는 중입니다…' })
    try {
      await onRefresh()
      setCommandState({ status: 'idle', message: '최신 상태를 불러왔습니다. 명령을 다시 제출해 주세요.' })
    } catch (error) {
      setCommandState({ status: 'error', message: repairCommandError(error), conflict: true })
    }
  }

  const actionForm = repair.stage === 'new'
    ? <form className="repair-command-form" onSubmit={(event) => { event.preventDefault(); void submitAdvance(assignmentDraft) }}>
      <div className="command-form-heading"><div><h3>파트너 배정</h3><p>수리소와 담당 수리사를 선택하면 접수된 요청을 배정합니다.</p></div><StatusPill tone="warning">필수 입력</StatusPill></div>
      <div className="form-grid">
        <label htmlFor="repair-station">수리소 ID <span>(합성)</span></label>
        <select id="repair-station" value={assignmentDraft.repairStationId} onChange={(event) => setAssignmentDraft({ ...assignmentDraft, repairStationId: event.target.value })} required aria-describedby="assignment-help">
          <option value="">수리소를 선택하세요</option>
          {syntheticRepairStations.map((station) => <option key={station.id} value={station.id}>{station.label}</option>)}
        </select>
        <label htmlFor="repairer-uid">담당 수리사 Firebase UID <span>(합성)</span></label>
        <select id="repairer-uid" value={assignmentDraft.repairerFirebaseUid} onChange={(event) => setAssignmentDraft({ ...assignmentDraft, repairerFirebaseUid: event.target.value })} required aria-describedby="assignment-help">
          <option value="">담당 수리사를 선택하세요</option>
          {syntheticRepairers.map((repairer) => <option key={repairer.uid} value={repairer.uid}>{repairer.label}</option>)}
        </select>
      </div>
      <label className="full-field" htmlFor="assignment-note">배정 메모 <span>(선택)</span></label>
      <textarea id="assignment-note" value={assignmentDraft.note} onChange={(event) => setAssignmentDraft({ ...assignmentDraft, note: event.target.value })} placeholder="방문 일정이나 전달 사항을 남겨 주세요." rows={2} />
      <p className="form-help" id="assignment-help">실제 운영에서는 서버가 권한과 수리소·수리사 연결을 다시 확인합니다.</p>
      <button className="primary-button" type="submit" disabled={isBusy || !assignmentDraft.repairStationId || !assignmentDraft.repairerFirebaseUid} aria-busy={isBusy}>{isBusy ? '배정 요청 중…' : '파트너 배정하기'} <Icon name="arrow" size={15} /></button>
    </form>
    : repair.stage === 'assigned'
      ? <div className="repair-command-form completed-command"><div className="command-form-heading"><div><h3>수리사 처리 대기</h3><p><strong>{repair.partner}</strong>에 배정되었습니다. 수리 제출과 작업 상태 변경은 수리사가 수행합니다.</p></div><StatusPill tone="info">운영자 읽기 전용</StatusPill></div><div className="completion-note pending-note"><Icon name="repair" size={15} /> 수리사가 작업을 완료하면 제출된 수리 결과와 청구 금액을 이 화면에서 확인할 수 있습니다.</div></div>
      : repair.stage === 'submitted'
        ? <form className="repair-command-form" onSubmit={(event) => { event.preventDefault(); void submitVerification() }}>
          <div className="command-form-heading"><div><h3>센터 검증 · 지원금 집행</h3><p>검증과 집행은 서로 다른 명령입니다. 검증 성공 뒤 최신 projection을 읽고 집행을 별도로 요청합니다.</p></div><StatusPill tone="warning">2단계 명령</StatusPill></div>
          <div className="submission-summary" aria-label="수리 제출 요약"><span>제출 파트너 <strong>{repair.partner}</strong></span><span>청구 금액 <strong>{repair.amount}</strong></span><span>제출 상태 <strong>수리 결과 및 비용 제출됨</strong></span></div>
          <div className="submitted-work-items" aria-label="제출된 수리 작업">{repair.workItems.length ? repair.workItems.map((item, index) => <div className="reserve-box" key={`${item.categoryCode}-${item.actionCode}-${index}`}><div><span>{item.categoryLabel}</span><strong>{item.actionLabel} · {item.quantity}개</strong></div><b>{item.lineAmountKrw.toLocaleString('ko-KR')}원</b></div>) : <div className="completion-note pending-note">구조화된 수리 항목이 없어 검증할 수 없습니다.</div>}</div>
          <fieldset className="verification-list"><legend>검증 체크리스트</legend>
            <label className="check-field"><input type="checkbox" checked={verification.repair} onChange={(event) => setVerification({ ...verification, repair: event.target.checked })} /> <span>수리 결과를 확인했습니다.</span></label>
            <label className="check-field"><input type="checkbox" checked={verification.amount} onChange={(event) => setVerification({ ...verification, amount: event.target.checked })} /> <span>청구 금액을 확인했습니다.</span></label>
            <label className="check-field"><input type="checkbox" checked={verification.eligibility} onChange={(event) => setVerification({ ...verification, eligibility: event.target.checked })} /> <span>지원금 적격성을 확인했습니다.</span></label>
          </fieldset>
          <label className="full-field" htmlFor="verification-note">검증 메모 <span>(선택)</span></label>
          <textarea id="verification-note" value={reviewNote} onChange={(event) => setReviewNote(event.target.value)} placeholder="검증 근거를 남겨 주세요." rows={2} />
          <div className="authority-sequence" aria-label="검증 및 집행 순서"><span className="active">1 · 센터 검증</span><i /><span>2 · projection 확인</span><i /><span>3 · 지원금 집행</span></div>
          {repair.subsidyContext && <div className="completion-note pending-note"><Icon name="money" size={15} /> 결정 기록 {repair.subsidyContext.decisionId ? '확인' : '없음'} · 예약 {repair.subsidyContext.reservedAmountKrw.toLocaleString('ko-KR')}원</div>}
          <button className="primary-button" type="submit" disabled={isBusy || !repair.workItems.length || !repair.subsidyContext?.decisionId || repair.subsidyContext.reservedAmountKrw < (repair.billedAmountKrw ?? Number.MAX_SAFE_INTEGER) || !verification.repair || !verification.amount || !verification.eligibility} aria-busy={isBusy}>{isBusy ? '명령 처리 중…' : '검증 후 집행 요청'} <Icon name="arrow" size={15} /></button>
        </form>
        : <div className="repair-command-form completed-command"><div className="command-form-heading"><div><h3>센터 검증 완료</h3><p>{repair.subsidyContext?.executionState === 'executed' ? '지원금 집행 원장까지 확인되었습니다.' : '검증은 완료됐지만 지원금 집행은 아직 확인되지 않았습니다.'}</p></div><StatusPill tone={repair.subsidyContext?.executionState === 'executed' ? 'success' : 'warning'}>{repair.subsidyContext?.executionState === 'executed' ? '집행 완료' : '집행 대기'}</StatusPill></div><div className={`completion-note ${repair.subsidyContext?.executionState === 'executed' ? '' : 'pending-note'}`}><Icon name={repair.subsidyContext?.executionState === 'executed' ? 'check' : 'money'} size={15} /> projection revision {repair.revision} · {repair.domainStatus}</div>{repair.subsidyContext?.executionState === 'execution_pending' && <button className="primary-button" type="button" onClick={() => void submitVerification()} disabled={isBusy} aria-busy={isBusy}>{isBusy ? '집행 재시도 중…' : '지원금 집행 재시도'} <Icon name="arrow" size={15} /></button>}</div>

  return <><div className="page-intro inner"><div><p className="eyebrow">{pageTitles.repairs.eyebrow}</p><h1>수리 운영</h1><p className="intro-copy">현재 화면은 로컬 합성 adapter로 접수·배정·제출·검증을 시연합니다. 인증된 도메인 API 반영은 production composition에서만 적용됩니다.</p></div><div className="intro-actions"><button className="ghost-button" onClick={() => onNavigate('subsidy')}><Icon name="money" size={16} /> 지원금 원장</button></div></div><div className="repair-layout"><section className="panel repair-list-panel"><div className="panel-title-row"><div><h2>전체 수리 요청</h2><p>최근 업데이트 순 · {repairs.length}건 표시</p></div><span className="projection-note">합성 projection · revision 포함</span></div><div className="repair-list">{repairs.map((item) => <button className={`repair-list-row ${selected === item.id ? 'selected' : ''}`} key={item.id} onClick={() => setSelected(item.id)} aria-pressed={selected === item.id}><div className={`request-stage-dot ${item.stage}`} /><div className="repair-list-main"><div><strong>{item.issue}</strong><StatusPill tone={item.priority === '높음' ? 'danger' : 'neutral'}>{item.priority}</StatusPill></div><span>{item.id} · {item.user} · {item.device}</span></div><div className="repair-list-state"><strong>{workflowLabels[item.stage]}</strong><span>{item.request}</span></div><Icon name="chevron" size={16} /></button>)}</div></section><section className="panel repair-detail-panel"><div className="detail-kicker"><span>SYNTHETIC DEMO · REVISION {repair.revision} · {repair.domainStatus}</span><button className="row-more" aria-label="지원금 원장 열기" onClick={() => onNavigate('subsidy')}><Icon name="money" size={17} /></button></div><div className="detail-title"><div className="request-avatar large"><Icon name="repair" size={22} /></div><div><p>{repair.id}</p><h2>{repair.issue}</h2><span>{repair.request} 접수 · {repair.user}</span></div></div><div className="detail-block"><h3>진행 단계</h3><div className="detail-timeline">{workflowOrder.map((stage, index) => <div className={`detail-stage ${workflowOrder.indexOf(repair.stage) >= index ? 'done' : ''}`} key={stage}><span>{workflowOrder.indexOf(repair.stage) > index ? '✓' : index + 1}</span><div><strong>{workflowLabels[stage]}</strong><small>{stage === 'new' ? '센터 요청 접수됨' : stage === 'assigned' ? `파트너 · ${repair.partner}` : stage === 'submitted' ? '수리 결과 및 비용 제출됨' : '센터 검증 결과 기록됨'}</small></div></div>)}</div></div><div className="detail-block"><h3>지원금 상태</h3><div className="reserve-box"><div><span>수리사 제출 금액 · 읽기 전용</span><strong>{repair.amount}</strong></div><StatusPill tone={repair.subsidyContext?.executionState === 'executed' ? 'success' : 'warning'}>{repair.subsidyContext?.executionState === 'executed' ? '집행 완료' : repair.domainStatus === 'center_verified' ? '집행 대기' : '검증 대기'}</StatusPill></div></div>{actionForm}{commandState.message && <div className={`command-feedback ${commandState.status}`} role={commandState.status === 'error' ? 'alert' : 'status'} aria-live="polite">{commandState.message}{commandState.conflict && <button className="ghost-button refresh-command" type="button" onClick={() => void refreshAfterConflict()} disabled={isBusy}>새로고침</button>}</div>}</section></div></>
}

function SubsidyPage({ notify, ledger }: { notify: (message: string) => void; ledger: LedgerEntry[] }) {
  return <><div className="page-intro inner"><div><p className="eyebrow">{pageTitles.subsidy.eyebrow}</p><h1>지원금 원장</h1><p className="intro-copy">수리 요청의 예약·집행·검증 내역을 감사 가능한 흐름으로 확인합니다.</p></div><div className="intro-actions"><button className="ghost-button" onClick={() => notify('CSV 내보내기 준비 중입니다.')}><Icon name="report" size={16} /> 원장 내보내기</button></div></div><div className="ledger-metrics"><div className="ledger-stat"><span>2026년 배정 예산</span><strong>₩4,000,000</strong><small>기관 운영 예산</small></div><div className="ledger-stat green"><span>집행 완료</span><strong>₩2,480,000</strong><small>62% · 11건</small></div><div className="ledger-stat orange"><span>예약 중</span><strong>₩365,000</strong><small>3건 · 검증 대기</small></div><div className="ledger-stat purple"><span>잔여 예산</span><strong>₩1,155,000</strong><small>이번 달 기준</small></div></div><section className="panel table-panel"><div className="ledger-table-top"><div><h2>지원금 변동 원장</h2><p>모든 예약과 집행은 수리 요청 및 담당자 기록에 연결됩니다.</p></div><button className="filter-button">최근 30일 <span>⌄</span></button></div><div className="table-header-row ledger-headers"><span>일자 / 요청</span><span>대상</span><span>항목</span><span>금액</span><span>상태</span><span>실행 주체</span></div><div className="table-body">{ledger.map((item) => <div className="data-row ledger-data-row" key={item.transactionId}><div className="ledger-date"><strong>{item.date}</strong><span>{item.id}</span></div><div className="muted-cell">{item.user}</div><div className="muted-cell">{item.item}</div><strong className="amount-cell">{item.amount}</strong><StatusPill tone={item.state === '집행 완료' ? 'success' : item.state === '예약' ? 'warning' : 'muted'}>{item.state}</StatusPill><div className="muted-cell actor-cell">{item.actor}</div><button className="row-more" onClick={() => notify(`${item.transactionId} 감사 로그를 열었습니다.`)}><Icon name="chevron" size={16} /></button></div>)}</div></section></>
}

export default App
