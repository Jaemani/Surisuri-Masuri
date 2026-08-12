import { describe, expect, it } from 'vitest'

import {
  DemoOperationsRepository,
  FirebaseOperationsRepository,
  OperationsRepositoryError,
  type FirebaseOperationsRepositoryDependencies,
} from './productOperationsRepository'

const projectionBase = 'https://projection.example.test/v1/console'
const commandBase = 'https://command.example.test'

const dependencies = (fetch: typeof globalThis.fetch, overrides: Partial<FirebaseOperationsRepositoryDependencies> = {}): FirebaseOperationsRepositoryDependencies => ({
  tokens: {
    tenantId: 'tenant-seoul-west',
    getIdToken: async () => 'id-token',
    getAppCheckToken: async () => 'app-check-token',
  },
  endpoints: {
    projections: {
      dashboard: `${projectionBase}/dashboard`,
      users: `${projectionBase}/users`,
      devices: `${projectionBase}/devices`,
      repairs: `${projectionBase}/repairs`,
      ledger: `${projectionBase}/ledger`,
      inspections: `${projectionBase}/inspections`,
      partners: `${projectionBase}/partners`,
      reports: `${projectionBase}/reports`,
      services: `${projectionBase}/services`,
    },
    transitionRepairRequest: `${commandBase}/transitionRepairRequest`,
    appendSubsidyTransaction: `${commandBase}/appendSubsidyTransaction`,
  },
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
    expect(calls[0].input).toBe(`${projectionBase}/dashboard`)
    expect(calls[0].init).toMatchObject({ method: 'GET' })
    expect(calls[0].init?.headers).toMatchObject({
      Authorization: 'Bearer id-token',
      'X-Firebase-AppCheck': 'app-check-token',
      'X-Tenant-Id': 'tenant-seoul-west',
    })
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

  it('fails closed when a projection is malformed instead of substituting synthetic data', async () => {
    const fetch = async () => json([{ id: 'repair-1' }])
    const repository = new FirebaseOperationsRepository(dependencies(fetch as typeof globalThis.fetch))

    await expect(repository.listRepairs()).rejects.toMatchObject({ code: 'INVALID_PROJECTION' })
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
