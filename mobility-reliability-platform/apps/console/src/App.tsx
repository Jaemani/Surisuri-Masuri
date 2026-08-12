import { useEffect, useMemo, useState } from 'react'
import {
  createProductOperationsRepository,
  type DashboardData,
  type DeviceRecord,
  type LedgerEntry,
  type RepairRecord,
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
  const advanceRepair = () => {
    if (!activeRepair) return
    void repository.advanceRepair({ type: 'repair.advance', repairId: activeRepair.id, actorId: 'console-demo-operator' }).then((result) => {
      setRepairs(result.repairs)
      setLedger(result.ledger)
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
        {page === 'home' ? <Dashboard data={dashboard} activeRepair={activeRepair} repairs={repairs} ledger={ledger} selectedRepair={selectedRepair} setSelectedRepair={setSelectedRepair} onAdvance={advanceRepair} onNavigate={navigate} notify={notify} /> : <GenericPage page={page} search={search} setSearch={setSearch} users={filteredUsers} devices={devices} repairs={repairs} ledger={ledger} onNavigate={navigate} notify={notify} />}
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
    <div className="metric-grid">{data.metrics.map((metric) => <Metric key={metric.label} {...metric} onClick={() => onNavigate(metric.icon === 'repair' ? 'repairs' : metric.icon === 'check' ? 'inspections' : metric.icon === 'money' ? 'subsidy' : 'system')} />)}</div>
    <div className="dashboard-grid top-grid"><section className="panel attention-panel"><SectionHeading title="지금 확인이 필요한 일" action="전체 보기" onAction={() => onNavigate('repairs')} /><div className="attention-list">{data.attention.map((item) => <Attention key={item.title} {...item} onClick={() => onNavigate(item.destination)} />)}</div></section><section className="panel week-panel"><SectionHeading title="이번 주 운영 현황" action="보고서 보기" onAction={() => onNavigate('reports')} /><div className="week-chart"><div className="chart-y"><span>12</span><span>8</span><span>4</span><span>0</span></div><div className="chart-area"><div className="grid-line line-1" /><div className="grid-line line-2" /><div className="grid-line line-3" /><div className="bars">{data.weeklyBars.map((bar) => <Bar key={bar.day} {...bar} />)}</div></div></div><div className="chart-legend"><span><i className="legend-dot completed" />처리 완료</span><span><i className="legend-dot pending" />처리 대기</span><strong>{data.weeklyChange} <small>지난주 대비</small></strong></div></section></div>
    <section className="panel workflow-panel"><div className="workflow-heading"><div><SectionHeading title="수리 요청 진행 현황" action="수리 운영 열기" onAction={() => onNavigate('repairs')} /><p>접수부터 센터 검증까지, 한 건의 흐름을 놓치지 않도록 관리하세요.</p></div><button className="ghost-button" onClick={() => notify('필터: 최근 30일을 적용했습니다.')}>최근 30일 <Icon name="chevron" size={14} /></button></div><div className="workflow-board">{workflowOrder.map((stage) => <div className={`workflow-column ${stage === activeRepair.stage ? 'selected-column' : ''}`} key={stage}><div className="column-title"><span className={`column-marker ${stage}`} />{workflowLabels[stage]}<b>{repairs.filter((repair) => repair.stage === stage).length}</b></div>{repairs.filter((repair) => repair.stage === stage).map((repair) => <button className={`repair-card ${selectedRepair === repair.id ? 'selected' : ''}`} key={repair.id} onClick={() => setSelectedRepair(repair.id)}><div className="repair-card-top"><span>{repair.id}</span><StatusPill tone={repair.priority === '높음' ? 'danger' : repair.priority === '낮음' ? 'muted' : 'neutral'}>{repair.priority}</StatusPill></div><strong>{repair.issue}</strong><div className="repair-person"><Avatar name={repair.user} color={repair.user === '박정호' ? 'blue' : repair.user === '이경자' ? 'lilac' : 'mint'} /><span>{repair.user}</span><small>{repair.request}</small></div><div className="repair-card-footer"><span>{repair.partner === '미배정' ? <em className="unassigned">파트너 미배정</em> : repair.partner}</span><span>{repair.amount}</span></div></button>)}</div>)}</div></section>
    <div className="dashboard-grid bottom-grid"><section className="panel selected-panel"><SectionHeading title="선택한 요청" action="상세 보기" onAction={() => onNavigate('repairs')} /><div className="selected-summary"><div className="selected-title"><div className="request-avatar"><Icon name="repair" size={20} /></div><div><div className="request-id">{activeRepair.id} <StatusPill tone={activeRepair.stage === 'verified' ? 'success' : 'warning'}>{workflowLabels[activeRepair.stage]}</StatusPill></div><h3>{activeRepair.issue}</h3><p>{activeRepair.user} · {activeRepair.device}</p></div></div><div className="stage-line">{workflowOrder.map((stage, index) => <div className={`stage-step ${workflowOrder.indexOf(activeRepair.stage) >= index ? 'done' : ''}`} key={stage}><span>{workflowOrder.indexOf(activeRepair.stage) > index ? '✓' : index + 1}</span><small>{workflowLabels[stage]}</small>{index < workflowOrder.length - 1 && <i />}</div>)}</div><div className="action-row"><button className="primary-button compact" onClick={onAdvance}>{activeRepair.stage === 'new' ? '파트너 배정하기' : activeRepair.stage === 'assigned' ? '수리 제출 확인' : activeRepair.stage === 'submitted' ? '센터 검증 완료' : '검증 완료됨'} <Icon name="arrow" size={15} /></button><button className="icon-button bordered" aria-label="더 보기" onClick={() => notify('감사 로그와 요청 메모를 준비 중입니다.')}><Icon name="more" size={18} /></button></div></div></section><section className="panel ledger-panel"><SectionHeading title="최근 지원금 원장" action="전체 원장" onAction={() => onNavigate('subsidy')} /><div className="ledger-list">{ledger.map((item) => <div className="ledger-row" key={item.id}><div className="ledger-date">{item.date}<small>{item.id}</small></div><div className="ledger-main"><strong>{item.item}</strong><span>{item.user} · {item.actor}</span></div><div className="ledger-amount"><strong>{item.amount}</strong><StatusPill tone={item.state === '예약' ? 'warning' : item.state === '집행 완료' ? 'success' : 'muted'}>{item.state}</StatusPill></div></div>)}</div><div className="ledger-foot"><span>이번 달 누적 집행</span><strong>₩2,480,000 <small>/ ₩4,000,000</small></strong></div></section></div>
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

function GenericPage({ page, search, setSearch, users: filteredUsers, devices, repairs, ledger, onNavigate, notify }: { page: PageKey; search: string; setSearch: (value: string) => void; users: UserRecord[]; devices: DeviceRecord[]; repairs: RepairRecord[]; ledger: LedgerEntry[]; onNavigate: (page: PageKey) => void; notify: (message: string) => void }) {
  const meta = pageTitles[page]
  if (page === 'repairs') return <RepairOperations repairs={repairs} onNavigate={onNavigate} notify={notify} />
  if (page === 'subsidy') return <SubsidyPage ledger={ledger} notify={notify} />
  const action = page === 'users' ? '이용자 등록' : page === 'devices' ? '기기 등록' : page === 'partners' ? '파트너 추가' : page === 'reports' ? '새 보고서' : undefined
  return <><div className="page-intro inner"><div><p className="eyebrow">{meta.eyebrow}</p><h1>{meta.title}</h1><p className="intro-copy">{meta.description}</p></div><div className="intro-actions">{action && <button className="primary-button" onClick={() => notify(`${action} 기능은 데모에서 준비 중입니다.`)}><span>＋</span> {action}</button>}</div></div><div className="toolbar"><div className="search-field"><Icon name="search" size={17} /><input value={search} onChange={(event) => setSearch(event.target.value)} placeholder={`${meta.title} 검색`} /></div><button className="filter-button" onClick={() => notify('필터 메뉴를 열었습니다.')}>필터 <span>⌄</span></button><button className="filter-button" onClick={() => notify('내보내기 준비 중입니다.')}>내보내기 <Icon name="arrow" size={14} /></button></div>{page === 'users' ? <UsersTable users={filteredUsers} notify={notify} /> : page === 'devices' ? <DevicesTable devices={devices} notify={notify} /> : page === 'inspections' ? <InspectionsTable notify={notify} /> : page === 'partners' ? <PartnersTable notify={notify} /> : page === 'reports' ? <ReportsTable notify={notify} /> : <SystemStatus notify={notify} />}</>
}

function TableShell({ children, headers }: { children: React.ReactNode; headers: string[] }) {
  return <section className="panel table-panel"><div className="table-header-row">{headers.map((header) => <span key={header}>{header}</span>)}</div>{children}</section>
}

function UsersTable({ users: filteredUsers, notify }: { users: UserRecord[]; notify: (message: string) => void }) {
  return <TableShell headers={['이용자', '연결 상태', '배정 기기', '마지막 활동', '운영 상태', '']}><div className="table-body">{filteredUsers.map((user) => <div className="data-row" key={user.code}><div className="person-cell"><Avatar name={user.name} color={user.color} /><div><strong>{user.name}</strong><span>{user.code}</span></div></div><div>{user.relation === '본인' ? <StatusPill tone="info">본인 인증</StatusPill> : <StatusPill tone="neutral">보호자 연결</StatusPill>}</div><div className="muted-cell">{user.device}</div><div className="muted-cell">{user.last}</div><div><StatusPill tone={user.status === '정상' ? 'success' : user.status === '점검 권장' ? 'warning' : 'muted'}>{user.status}</StatusPill></div><button className="row-more" onClick={() => notify(`${user.name}님의 상세 정보를 열었습니다.`)}><Icon name="more" size={18} /></button></div>)}</div><div className="table-footer">전체 24명 중 5명 표시 <span>1 / 5</span></div></TableShell>
}

function DevicesTable({ devices, notify }: { devices: DeviceRecord[]; notify: (message: string) => void }) {
  return <TableShell headers={['기기', '현재 이용자', '모델', '배터리', '누적 사용량', '다음 점검', '상태']}><div className="table-body">{devices.map((device) => <div className="data-row device-row" key={device.id}><div className="device-cell"><div className="device-thumb"><Icon name="device" size={17} /></div><div><strong>{device.id}</strong><span>{device.health}</span></div></div><div className="muted-cell">{device.user}</div><div className="muted-cell">{device.model}</div><div className="battery"><span><i style={{ width: device.battery }} /></span>{device.battery}</div><div className="muted-cell">{device.mileage}</div><div className="muted-cell">{device.inspection}</div><StatusPill tone={device.state === '정상' ? 'success' : device.state === '주의' ? 'warning' : 'muted'}>{device.state}</StatusPill><button className="row-more" onClick={() => notify(`${device.id} 상세 상태를 열었습니다.`)}><Icon name="more" size={18} /></button></div>)}</div></TableShell>
}

function InspectionsTable({ notify }: { notify: (message: string) => void }) {
  const rows = [{ user: '박정호', device: 'MOB-23991', due: '08. 16', reason: '조향부 진동 증가', score: '주의', confidence: '높음' }, { user: '이경자', device: 'MOB-23874', due: '08. 14', reason: '주행 데이터 3일 부족', score: '데이터 부족', confidence: '—' }, { user: '윤옥순', device: 'MOB-23218', due: '08. 18', reason: '정기 점검 주기 도래', score: '관찰', confidence: '보통' }, { user: '최민수', device: 'MOB-23703', due: '09. 22', reason: '정기 점검 주기 도래', score: '양호', confidence: '높음' }]
  return <TableShell headers={['대상', '기기', '예정일', '근거', '판정', '신뢰도', '']}><div className="table-body">{rows.map((row) => <div className="data-row" key={row.device}><div className="person-cell"><Avatar name={row.user} color="blue" /><div><strong>{row.user}</strong><span>예방점검 대상</span></div></div><div className="muted-cell">{row.device}</div><div className="date-cell">{row.due}<small>2026</small></div><div className="muted-cell reason-cell">{row.reason}<small>Fact ID · F-08{row.device.slice(-2)}</small></div><StatusPill tone={row.score === '주의' ? 'warning' : row.score === '양호' ? 'success' : 'muted'}>{row.score}</StatusPill><span className="confidence">{row.confidence}</span><button className="row-more" onClick={() => notify(`${row.user}님의 근거 상세를 열었습니다.`)}><Icon name="chevron" size={17} /></button></div>)}</div></TableShell>
}

function PartnersTable({ notify }: { notify: (message: string) => void }) {
  const rows = [{ name: '한마음 모빌리티', contact: '김도현 · 02-321-8842', active: '3건', completed: '18건', sla: '1.4일', tone: 'success' }, { name: '케어휠 수리소', contact: '장유진 · 02-884-2011', active: '2건', completed: '12건', sla: '2.1일', tone: 'warning' }, { name: '서부 보장구 센터', contact: '이재훈 · 02-771-0390', active: '0건', completed: '24건', sla: '1.1일', tone: 'success' }]
  return <TableShell headers={['파트너', '담당자', '진행 중', '최근 완료', '평균 처리', '상태', '']}><div className="table-body">{rows.map((row) => <div className="data-row" key={row.name}><div className="person-cell"><div className="partner-logo">{row.name.slice(0, 1)}</div><div><strong>{row.name}</strong><span>협력 수리 파트너</span></div></div><div className="muted-cell">{row.contact}</div><div className="number-cell">{row.active}</div><div className="number-cell">{row.completed}</div><div className="muted-cell">{row.sla}</div><StatusPill tone={row.tone as 'success' | 'warning'}>{row.tone === 'success' ? '정상' : '확인 필요'}</StatusPill><button className="row-more" onClick={() => notify(`${row.name} 정보를 열었습니다.`)}><Icon name="more" size={18} /></button></div>)}</div></TableShell>
}

function ReportsTable({ notify }: { notify: (message: string) => void }) {
  const rows = [{ title: '7월 기관 운영 리포트', type: '월간 운영', date: '2026. 08. 01', state: '발행 완료', facts: '18' }, { title: '수리 지원금 집행 현황', type: '재정·감사', date: '2026. 08. 08', state: '검토 중', facts: '12' }, { title: '예방점검 우선순위 목록', type: '점검 운영', date: '2026. 08. 12', state: '초안', facts: '9' }]
  return <TableShell headers={['보고서', '유형', '기준일', '연결된 근거', '상태', '']}><div className="table-body">{rows.map((row) => <div className="data-row" key={row.title}><div className="report-cell"><div className="report-thumb"><Icon name="report" size={17} /></div><div><strong>{row.title}</strong><span>서울서부 복지관</span></div></div><StatusPill tone="info">{row.type}</StatusPill><div className="muted-cell">{row.date}</div><div className="fact-count"><Icon name="shield" size={14} /> Fact {row.facts}개</div><StatusPill tone={row.state === '발행 완료' ? 'success' : row.state === '검토 중' ? 'warning' : 'muted'}>{row.state}</StatusPill><button className="row-more" onClick={() => notify(`${row.title} 미리보기를 열었습니다.`)}><Icon name="chevron" size={17} /></button></div>)}</div></TableShell>
}

function SystemStatus({ notify }: { notify: (message: string) => void }) {
  const services = [{ name: '모바일 수집', detail: '최근 24시간 수집 성공률', value: '98.7%', status: '정상', tone: 'success' }, { name: '동기화 큐', detail: '미처리 이벤트 14건 · 최대 12분', value: '양호', status: '정상', tone: 'success' }, { name: '상태 projection', detail: '마지막 처리 지연 4분', value: '4분', status: '정상', tone: 'success' }, { name: '보고서 검증', detail: '최근 30일 검증 실패 1건', value: '주의', status: '확인 필요', tone: 'warning' }]
  return <div className="system-grid"><section className="panel system-panel"><SectionHeading title="서비스 상태" action="새로고침" onAction={() => notify('서비스 상태를 새로고침했습니다.')} />{services.map((service) => <div className="service-row" key={service.name}><div className="service-status"><span className={`pulse ${service.tone}`} /><div><strong>{service.name}</strong><span>{service.detail}</span></div></div><strong className="service-value">{service.value}</strong><StatusPill tone={service.tone as 'success' | 'warning'}>{service.status}</StatusPill><Icon name="chevron" size={16} /></div>)}</section><section className="panel privacy-panel"><div className="privacy-art"><Icon name="shield" size={26} /></div><p className="eyebrow">PRIVACY BY DEFAULT</p><h2>원본 이동경로는<br />기본 화면에 표시하지 않아요.</h2><p>콘솔에는 집계·위험 근거·데이터 품질·운영 상태만 표시됩니다. 원본 위치는 목적 제한된 접근과 감사 로그를 거칩니다.</p><button className="text-button" onClick={() => notify('개인정보 보호 원칙을 확인했습니다.')}>보호 원칙 보기 <Icon name="arrow" size={15} /></button></section></div>
}

function RepairOperations({ repairs, onNavigate, notify }: { repairs: RepairRecord[]; onNavigate: (page: PageKey) => void; notify: (message: string) => void }) {
  const [selected, setSelected] = useState(repairs[0].id)
  const repair = repairs.find((item) => item.id === selected) ?? repairs[0]
  return <><div className="page-intro inner"><div><p className="eyebrow">{pageTitles.repairs.eyebrow}</p><h1>수리 운영</h1><p className="intro-copy">새 요청을 놓치지 않고, 지원금 집행까지 한 흐름으로 기록합니다.</p></div><div className="intro-actions"><button className="ghost-button" onClick={() => onNavigate('subsidy')}><Icon name="money" size={16} /> 지원금 원장</button><button className="primary-button" onClick={() => notify('새 수리 요청 작성 화면은 데모에서 준비 중입니다.')}><span>＋</span> 새 수리 요청</button></div></div><div className="repair-layout"><section className="panel repair-list-panel"><div className="panel-title-row"><div><h2>전체 수리 요청</h2><p>최근 업데이트 순 · 7건 처리 대기</p></div><button className="filter-button">모든 상태 <span>⌄</span></button></div><div className="repair-list">{repairs.map((item) => <button className={`repair-list-row ${selected === item.id ? 'selected' : ''}`} key={item.id} onClick={() => setSelected(item.id)}><div className={`request-stage-dot ${item.stage}`} /><div className="repair-list-main"><div><strong>{item.issue}</strong><StatusPill tone={item.priority === '높음' ? 'danger' : 'neutral'}>{item.priority}</StatusPill></div><span>{item.id} · {item.user} · {item.device}</span></div><div className="repair-list-state"><strong>{workflowLabels[item.stage]}</strong><span>{item.request}</span></div><Icon name="chevron" size={16} /></button>)}</div></section><section className="panel repair-detail-panel"><div className="detail-kicker"><span>REQUEST DETAIL</span><button className="row-more" onClick={() => notify('감사 로그를 열었습니다.')}><Icon name="more" size={18} /></button></div><div className="detail-title"><div className="request-avatar large"><Icon name="repair" size={22} /></div><div><p>{repair.id}</p><h2>{repair.issue}</h2><span>{repair.request} 접수 · {repair.user}</span></div></div><div className="detail-block"><h3>진행 단계</h3><div className="detail-timeline">{workflowOrder.map((stage, index) => <div className={`detail-stage ${workflowOrder.indexOf(repair.stage) >= index ? 'done' : ''}`} key={stage}><span>{workflowOrder.indexOf(repair.stage) > index ? '✓' : index + 1}</span><div><strong>{workflowLabels[stage]}</strong><small>{stage === 'new' ? '센터 요청 접수됨' : stage === 'assigned' ? `파트너 · ${repair.partner}` : stage === 'submitted' ? '수리 결과 및 비용 제출됨' : '담당자 검증 및 예약 완료'}</small></div></div>)}</div></div><div className="detail-block"><h3>지원금 예약</h3><div className="reserve-box"><div><span>예상 집행 금액</span><strong>{repair.amount}</strong></div><StatusPill tone={repair.stage === 'verified' ? 'success' : 'warning'}>{repair.stage === 'verified' ? '집행 완료' : '예약 대기'}</StatusPill></div></div><div className="detail-actions"><button className="primary-button" onClick={() => notify(repair.stage === 'verified' ? '이미 검증이 완료된 요청입니다.' : `${repair.id} 다음 단계 작업을 시작했습니다.`)}>{repair.stage === 'new' ? '파트너 배정하기' : repair.stage === 'assigned' ? '수리 제출 확인' : repair.stage === 'submitted' ? '센터 검증 완료' : '검증 완료'} <Icon name="arrow" size={15} /></button><button className="ghost-button" onClick={() => notify('요청 메모를 준비 중입니다.')}>메모 남기기</button></div></section></div></>
}

function SubsidyPage({ notify, ledger }: { notify: (message: string) => void; ledger: LedgerEntry[] }) {
  return <><div className="page-intro inner"><div><p className="eyebrow">{pageTitles.subsidy.eyebrow}</p><h1>지원금 원장</h1><p className="intro-copy">수리 요청의 예약·집행·검증 내역을 감사 가능한 흐름으로 확인합니다.</p></div><div className="intro-actions"><button className="ghost-button" onClick={() => notify('CSV 내보내기 준비 중입니다.')}><Icon name="report" size={16} /> 원장 내보내기</button></div></div><div className="ledger-metrics"><div className="ledger-stat"><span>2026년 배정 예산</span><strong>₩4,000,000</strong><small>기관 운영 예산</small></div><div className="ledger-stat green"><span>집행 완료</span><strong>₩2,480,000</strong><small>62% · 11건</small></div><div className="ledger-stat orange"><span>예약 중</span><strong>₩365,000</strong><small>3건 · 검증 대기</small></div><div className="ledger-stat purple"><span>잔여 예산</span><strong>₩1,155,000</strong><small>이번 달 기준</small></div></div><section className="panel table-panel"><div className="ledger-table-top"><div><h2>지원금 변동 원장</h2><p>모든 예약과 집행은 수리 요청 및 담당자 기록에 연결됩니다.</p></div><button className="filter-button">최근 30일 <span>⌄</span></button></div><div className="table-header-row ledger-headers"><span>일자 / 요청</span><span>대상</span><span>항목</span><span>금액</span><span>상태</span><span>실행 주체</span></div><div className="table-body">{ledger.map((item) => <div className="data-row ledger-data-row" key={item.id}><div className="ledger-date"><strong>{item.date}</strong><span>{item.id}</span></div><div className="muted-cell">{item.user}</div><div className="muted-cell">{item.item}</div><strong className="amount-cell">{item.amount}</strong><StatusPill tone={item.state === '집행 완료' ? 'success' : item.state === '예약' ? 'warning' : 'muted'}>{item.state}</StatusPill><div className="muted-cell actor-cell">{item.actor}</div><button className="row-more" onClick={() => notify(`${item.id} 감사 로그를 열었습니다.`)}><Icon name="chevron" size={16} /></button></div>)}</div></section></>
}

export default App
