import { describe, expect, it } from 'vitest'

import {
  DemoOperationsRepository,
  FirebaseOperationsRepository,
  OperationsRepositoryError,
  createOperationsApiEndpoints,
  runCenterVerificationAndSubsidyExecution,
  type FirebaseOperationsRepositoryDependencies,
} from './productOperationsRepository'

const projectionBase = 'https://projection.example.test/v1'
const commandBase = 'https://command.example.test'

const dependencies = (fetch: typeof globalThis.fetch, overrides: Partial<FirebaseOperationsRepositoryDependencies> = {}): FirebaseOperationsRepositoryDependencies => ({
  tokens: {
    tenantId: 'tenant-seoul-west',
    getIdToken: async () => 'id-token',
    getAppCheckToken: async () => 'app-check-token',
  },
  endpoints: createOperationsApiEndpoints({ baseUrl: projectionBase, commandBaseUrl: commandBase }),
  fetch,
  createIdempotencyKey: () => '3b789d1c-5220-4fab-afdf-61b53b130fa4',
  ...overrides,
})

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

const dashboardProjection = {
  metrics: [{ label: '처리할 수리 요청', value: '7', suffix: '건', trend: '지난주보다 2건 많아요', tone: 'orange', icon: 'repair' }],
  attention: [{ icon: 'repair', color: 'orange', title: '새 수리 요청 1건', description: '점검이 필요합니다', time: '8분 전', action: '배정하기', destination: 'repairs' }],
  weeklyBars: [{ day: '월', value: 4, active: true }],
  weeklyChange: '+18%',
}

describe('DemoOperationsRepository', () => {
  it('returns isolated read models instead of shared mutable demo arrays', async () => {
    const repository = new DemoOperationsRepository()
    const first = await repository.listRepairs()
    first[0].issue = 'mutated outside repository'

    const second = await repository.listRepairs()
    expect(second[0].issue).toBe('주행 중 좌측 쏠림')
  })

  it('returns device timelines as isolated synthetic read models', async () => {
    const repository = new DemoOperationsRepository()
    const first = await repository.listDevices()
    expect(first[0]?.timeline.length).toBeGreaterThan(0)
    first[0]!.timeline[0]!.title = 'mutated outside repository'

    const second = await repository.listDevices()
    expect(second[0]!.timeline[0]!.title).not.toBe('mutated outside repository')
  })

  it('advances a repair deterministically and leaves an auditable ledger projection intact', async () => {
    const repository = new DemoOperationsRepository()
    const result = await repository.advanceRepair({
      type: 'repair.advance',
      repairId: 'SR-2026-081',
      actorId: 'console-demo-operator',
    })

    expect(result.nextStage).toBe('assigned')
    expect(result.repair.partner).toBe('한마음 모빌리티')
    expect(result.ledger).toHaveLength(4)
  })

  it('keeps center verification and subsidy execution as separate authoritative demo mutations', async () => {
    const repository = new DemoOperationsRepository()
    const submitted = (await repository.listRepairs()).find((repair) => repair.id === 'SR-2026-074')!

    await repository.transitionRepair({ repairRequestId: submitted.id, expectedRevision: submitted.revision, toStatus: 'center_verified', subsidyAccountId: submitted.subsidyContext!.accountId, subsidyDecisionId: 'decision-test' })
    const verified = (await repository.listRepairs()).find((repair) => repair.id === submitted.id)!
    expect(verified).toMatchObject({ domainStatus: 'center_verified', revision: 5, subsidyContext: { executionState: 'execution_pending' } })
    expect(await repository.listLedger()).toHaveLength(4)

    await repository.appendSubsidyTransaction({ accountId: submitted.subsidyContext!.accountId, personId: submitted.subsidyContext!.personId, policyVersionId: submitted.subsidyContext!.policyVersionId, transactionType: 'execution', amountKrw: submitted.billedAmountKrw!, workOrderId: submitted.id, reasonCode: 'CENTER_VERIFIED_REPAIR_EXECUTION' })
    const executed = (await repository.listRepairs()).find((repair) => repair.id === submitted.id)!
    expect(executed.subsidyContext?.executionState).toBe('executed')
    expect(await repository.listLedger()).toEqual(expect.arrayContaining([expect.objectContaining({ transactionId: 'demo-execution-SR-2026-074', transactionType: 'execution', id: 'SR-2026-074' })]))
  })

  it('rejects subsidy execution before center verification', async () => {
    const repository = new DemoOperationsRepository()
    const submitted = (await repository.listRepairs()).find((repair) => repair.id === 'SR-2026-074')!

    await expect(repository.appendSubsidyTransaction({ accountId: submitted.subsidyContext!.accountId, personId: submitted.subsidyContext!.personId, policyVersionId: submitted.subsidyContext!.policyVersionId, transactionType: 'execution', amountKrw: submitted.billedAmountKrw!, workOrderId: submitted.id, reasonCode: 'CENTER_VERIFIED_REPAIR_EXECUTION' })).rejects.toMatchObject({ code: 'EXECUTION_STATUS_INVALID' })
  })

  it('preserves verified state when the separate execution command fails', async () => {
    class ExecutionFailureRepository extends DemoOperationsRepository {
      override async appendSubsidyTransaction(): Promise<never> {
        throw new OperationsRepositoryError('EXECUTION_REJECTED', 'synthetic execution rejection', 409)
      }
    }
    const repository = new ExecutionFailureRepository()
    const repair = (await repository.listRepairs()).find((item) => item.id === 'SR-2026-074')!

    const result = await runCenterVerificationAndSubsidyExecution({ repository, repair, refresh: () => repository.listRepairs() })

    expect(result).toMatchObject({ verification: 'verified', execution: 'pending', reason: 'execution_failed' })
    expect((await repository.listRepairs()).find((item) => item.id === repair.id)).toMatchObject({ domainStatus: 'center_verified', subsidyContext: { executionState: 'execution_pending' } })
  })

  it('does not report execution until the immutable transaction is visible in the refreshed projection', async () => {
    class DelayedExecutionProjectionRepository extends DemoOperationsRepository {
      override async appendSubsidyTransaction() {
        return { commandType: 'append_subsidy_transaction' as const, tenantId: 'demo-tenant', resourceId: 'account-choi', eventId: 'event-delayed', transactionId: 'tx-delayed' }
      }
    }
    const repository = new DelayedExecutionProjectionRepository()
    const repair = (await repository.listRepairs()).find((item) => item.id === 'SR-2026-074')!

    const result = await runCenterVerificationAndSubsidyExecution({ repository, repair, refresh: () => repository.listRepairs() })

    expect(result).toEqual({ verification: 'verified', execution: 'pending', reason: 'execution_not_confirmed' })
    expect((await repository.listRepairs()).find((item) => item.id === repair.id)?.subsidyContext?.executionState).toBe('execution_pending')
  })
})

describe('FirebaseOperationsRepository', () => {
  it('fails explicitly until the Domain Command API is configured', async () => {
    const repository = new FirebaseOperationsRepository()
    await expect(repository.listRepairs()).rejects.toMatchObject({
      code: 'NOT_CONFIGURED',
    })
  })

  it('reads a server projection with injected ID token, App Check, and tenant scope', async () => {
    const calls: { input: RequestInfo | URL; init?: RequestInit }[] = []
    const fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ input, init })
      return json(dashboardProjection)
    }
    const repository = new FirebaseOperationsRepository(dependencies(fetch as typeof globalThis.fetch))

    await expect(repository.loadDashboard()).resolves.toEqual(dashboardProjection)
    expect(calls).toHaveLength(1)
    expect(calls[0].input).toBe(`${projectionBase}/getConsoleOperationsSnapshot?projection=dashboard`)
    expect(calls[0].init).toMatchObject({ method: 'GET' })
    expect(calls[0].init?.headers).toMatchObject({
      Authorization: 'Bearer id-token',
      'X-Firebase-AppCheck': 'app-check-token',
      'X-Tenant-Id': 'tenant-seoul-west',
    })
  })

  it('derives all endpoint URLs from the deployed Functions origins', async () => {
    const calls: { input: RequestInfo | URL; init?: RequestInit }[] = []
    const fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ input, init })
      return json(dashboardProjection)
    }
    const configured = dependencies(fetch as typeof globalThis.fetch)
    const repository = new FirebaseOperationsRepository({
      ...configured,
      endpoints: undefined,
      baseUrl: projectionBase,
      commandBaseUrl: commandBase,
    })

    await repository.loadDashboard()

    expect(calls[0].input).toBe(`${projectionBase}/getConsoleOperationsSnapshot?projection=dashboard`)
    expect(calls[0].init?.headers).toMatchObject({ 'X-Tenant-Id': 'tenant-seoul-west' })
  })

  it('sends a canonical repair transition only through the command endpoint', async () => {
    const calls: { input: RequestInfo | URL; init?: RequestInit }[] = []
    const fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ input, init })
      return json({ commandType: 'transition_repair_request', tenantId: 'tenant-seoul-west', resourceId: 'repair-1', eventId: 'event-1', revision: 2, status: 'assigned' })
    }
    const repository = new FirebaseOperationsRepository(dependencies(fetch as typeof globalThis.fetch))

    await expect(repository.transitionRepair({ repairRequestId: 'repair-1', expectedRevision: 1, toStatus: 'assigned', repairStationId: 'station-7', repairerFirebaseUid: 'repairer-uid' })).resolves.toMatchObject({ revision: 2, status: 'assigned' })
    expect(calls[0].input).toBe(`${commandBase}/transitionRepairRequest`)
    expect(calls[0].init?.headers).toMatchObject({ 'Idempotency-Key': '3b789d1c-5220-4fab-afdf-61b53b130fa4' })
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({ tenantId: 'tenant-seoul-west', repairRequestId: 'repair-1', expectedRevision: 1, toStatus: 'assigned', repairStationId: 'station-7', repairerFirebaseUid: 'repairer-uid' })
    expect(String(calls[0].init?.body)).not.toContain('actorId')
  })

  it('sends subsidy mutations through the separate append command with idempotency', async () => {
    const calls: { input: RequestInfo | URL; init?: RequestInit }[] = []
    const fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ input, init })
      return json({ commandType: 'append_subsidy_transaction', tenantId: 'tenant-seoul-west', resourceId: 'account-1', eventId: 'event-2', transactionId: 'tx-2' })
    }
    const repository = new FirebaseOperationsRepository(dependencies(fetch as typeof globalThis.fetch))

    await repository.appendSubsidyTransaction({ accountId: 'account-1', personId: 'person-1', policyVersionId: 'policy-2026', transactionType: 'reservation', amountKrw: 180000, workOrderId: 'repair-1', reasonCode: 'REPAIR_ESTIMATE' })
    expect(calls[0].input).toBe(`${commandBase}/appendSubsidyTransaction`)
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({ tenantId: 'tenant-seoul-west', accountId: 'account-1', personId: 'person-1', policyVersionId: 'policy-2026', transactionType: 'reservation', amountKrw: 180000, reasonCode: 'REPAIR_ESTIMATE', workOrderId: 'repair-1' })
  })

  it('uses a distinct idempotency key for verification and execution commands', async () => {
    const calls: RequestInit[] = []
    let keyIndex = 0
    const fetch = async (_input: RequestInfo | URL, init?: RequestInit) => {
      calls.push(init ?? {})
      const body = JSON.parse(String(init?.body))
      return body.toStatus
        ? json({ commandType: 'transition_repair_request', tenantId: 'tenant-seoul-west', resourceId: 'repair-1', eventId: 'event-verify', revision: 7, status: 'center_verified' })
        : json({ commandType: 'append_subsidy_transaction', tenantId: 'tenant-seoul-west', resourceId: 'account-1', eventId: 'event-execute', transactionId: 'tx-execute' })
    }
    const repository = new FirebaseOperationsRepository(dependencies(fetch as typeof globalThis.fetch, { createIdempotencyKey: () => `command-key-${++keyIndex}` }))

    await repository.transitionRepair({ repairRequestId: 'repair-1', expectedRevision: 6, toStatus: 'center_verified', subsidyAccountId: 'account-1', subsidyDecisionId: 'decision-1' })
    await repository.appendSubsidyTransaction({ accountId: 'account-1', personId: 'person-1', policyVersionId: 'policy-2026', transactionType: 'execution', amountKrw: 72000, workOrderId: 'repair-1', reasonCode: 'CENTER_VERIFIED_REPAIR_EXECUTION' })

    expect(calls.map((call) => (call.headers as Record<string, string>)['Idempotency-Key'])).toEqual(['command-key-1', 'command-key-2'])
  })

  it('fails closed when a projection is malformed instead of substituting synthetic data', async () => {
    const fetch = async () => json([{ id: 'repair-1' }])
    const repository = new FirebaseOperationsRepository(dependencies(fetch as typeof globalThis.fetch))

    await expect(repository.listRepairs()).rejects.toMatchObject({ code: 'INVALID_PROJECTION' })
  })

  it('parses the purpose-limited repair authority and subsidy context', async () => {
    const projection = [{
      id: 'repair-1', user: '이용자 C-1042', device: 'MOB-1', issue: '브레이크', request: '2026. 08. 13', partner: '한마음', amount: '₩72,000',
      workItems: [{ categoryCode: 'brakes', categoryLabel: '브레이크', actionCode: 'repair', actionLabel: '수리', quantity: 1, lineAmountKrw: 72000 }],
      stage: 'submitted', domainStatus: 'repairer_submitted', priority: '보통', revision: 6, publicFundingInvolved: true, billedAmountKrw: 72000,
      subsidyContext: { accountId: 'account-1', personId: 'person-1', policyVersionId: 'policy-1', reservedAmountKrw: 72000, executedAmountKrw: 0, executionState: 'verification_required' },
    }]
    const repository = new FirebaseOperationsRepository(dependencies((async () => json(projection)) as typeof globalThis.fetch))

    await expect(repository.listRepairs()).resolves.toEqual(projection)
  })

  it.each([
    { field: 'revision', mutate: (projection: Array<Record<string, any>>) => { projection[0].revision = 1.5 } },
    { field: 'quantity', mutate: (projection: Array<Record<string, any>>) => { projection[0].workItems[0].quantity = 0 } },
    { field: 'line amount', mutate: (projection: Array<Record<string, any>>) => { projection[0].workItems[0].lineAmountKrw = -1 } },
  ])('rejects unsafe authority projection $field', async ({ mutate }) => {
    const projection: Array<Record<string, any>> = [{
      id: 'repair-1', user: '이용자', device: 'MOB-1', issue: '브레이크', request: '오늘', partner: '한마음', amount: '₩1',
      workItems: [{ categoryCode: 'brakes', categoryLabel: '브레이크', actionCode: 'repair', actionLabel: '수리', quantity: 1, lineAmountKrw: 1 }],
      stage: 'submitted', domainStatus: 'repairer_submitted', priority: '보통', revision: 1, publicFundingInvolved: true, billedAmountKrw: 1,
      subsidyContext: { accountId: 'account-1', personId: 'person-1', policyVersionId: 'policy-1', reservedAmountKrw: 1, executedAmountKrw: 0, executionState: 'verification_required' },
    }]
    mutate(projection)
    const repository = new FirebaseOperationsRepository(dependencies((async () => json(projection)) as typeof globalThis.fetch))

    await expect(repository.listRepairs()).rejects.toMatchObject({ code: 'INVALID_PROJECTION' })
  })

  it('parses a device timeline projection with exact safe fields', async () => {
    const timeline = [{ id: 'event-1', date: '2026. 08. 13', title: '수리 기록을 확인했어요', detail: '브레이크 수리', tone: 'success' }]
    const fetch = async () => json([{
      id: 'MOB-1', user: '이용자 C-1042', model: 'EV-2', health: '양호', battery: '78%', mileage: '128 km', inspection: '2026. 09. 03', state: '정상', timeline,
    }])
    const repository = new FirebaseOperationsRepository(dependencies(fetch as typeof globalThis.fetch))

    await expect(repository.listDevices()).resolves.toEqual([{
      id: 'MOB-1', user: '이용자 C-1042', model: 'EV-2', health: '양호', battery: '78%', mileage: '128 km', inspection: '2026. 09. 03', state: '정상', timeline,
    }])
  })

  it.each([
    { label: 'timeline is missing', timeline: undefined },
    { label: 'timeline is not an array', timeline: {} },
    { label: 'timeline tone is unsupported', timeline: [{ id: 'event-1', date: '2026. 08. 13', title: '수리 기록', detail: '브레이크 수리', tone: 'teal' }] },
    { label: 'timeline detail is missing', timeline: [{ id: 'event-1', date: '2026. 08. 13', title: '수리 기록', tone: 'success' }] },
  ])('fails closed when $label', async ({ timeline }) => {
    const device = {
      id: 'MOB-1', user: '이용자 C-1042', model: 'EV-2', health: '양호', battery: '78%', mileage: '128 km', inspection: '2026. 09. 03', state: '정상',
      ...(timeline === undefined ? {} : { timeline }),
    }
    const fetch = async () => json([device])
    const repository = new FirebaseOperationsRepository(dependencies(fetch as typeof globalThis.fetch))

    await expect(repository.listDevices()).rejects.toMatchObject({ code: 'INVALID_PROJECTION' })
  })

  it('propagates a command rejection with its backend code and never retries as a direct write', async () => {
    let callCount = 0
    const fetch = async () => {
      callCount += 1
      return json({ error: { code: 'REVISION_CONFLICT', message: 'reload before trying again' } }, 409)
    }
    const repository = new FirebaseOperationsRepository(dependencies(fetch as typeof globalThis.fetch))

    await expect(repository.transitionRepair({ repairRequestId: 'repair-1', expectedRevision: 1, toStatus: 'under_review' })).rejects.toMatchObject({ code: 'REVISION_CONFLICT', status: 409 } satisfies Partial<OperationsRepositoryError>)
    expect(callCount).toBe(1)
  })

  it('does not issue any HTTP request without a valid App Check token', async () => {
    let callCount = 0
    const fetch = async () => {
      callCount += 1
      return json(dashboardProjection)
    }
    const base = dependencies(fetch as typeof globalThis.fetch)
    const repository = new FirebaseOperationsRepository({ ...base, tokens: { ...base.tokens, getAppCheckToken: async () => '' } })

    await expect(repository.loadDashboard()).rejects.toMatchObject({ code: 'APP_CHECK_REQUIRED' })
    expect(callCount).toBe(0)
  })

})
