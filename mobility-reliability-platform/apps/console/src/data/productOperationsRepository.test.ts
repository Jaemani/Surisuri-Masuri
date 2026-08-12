import { describe, expect, it } from 'vitest'

import {
  DemoOperationsRepository,
  FirebaseOperationsRepository,
} from './productOperationsRepository'

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
})
