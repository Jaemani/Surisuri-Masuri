import { describe, expect, it } from 'vitest';
import { validateRepairIntake } from './repairIntake';

const valid = { category: '바퀴·타이어' as const, detail: '어제부터 오른쪽 바퀴에서 큰 소리가 나요', publicFundingInvolved: true, requestedAmountKrw: '120000', idempotencyKey: 'stable-repair-key' };

describe('repair intake validation', () => {
  it('maps category, detail, funding and stable retry key to the command', () => {
    expect(validateRepairIntake(valid)).toEqual({ valid: true, errors: null, input: { title: '바퀴·타이어: 어제부터 오른쪽 바퀴에서 큰 소리가 나요', publicFundingInvolved: true, requestedAmountKrw: 120000, idempotencyKey: 'stable-repair-key' } });
  });

  it('omits the amount for a non-funded consultation', () => {
    expect(validateRepairIntake({ ...valid, publicFundingInvolved: false, requestedAmountKrw: '' })).toMatchObject({ valid: true, input: { publicFundingInvolved: false } });
  });

  it('requires category, 10-character detail, funding choice, and funded amount', () => {
    expect(validateRepairIntake({ category: null, detail: '짧아요', publicFundingInvolved: null, requestedAmountKrw: '' })).toEqual({ valid: false, input: null, errors: { category: '가장 가까운 증상 분류를 골라 주세요.', detail: '문제가 언제, 어떻게 생겼는지 10자 이상 적어 주세요.', publicFundingInvolved: '수리비 지원 신청 여부를 골라 주세요.' } });
    expect(validateRepairIntake({ ...valid, requestedAmountKrw: '' })).toMatchObject({ valid: false, errors: { requestedAmountKrw: '지원금을 신청하려면 예상 수리비를 입력해 주세요.' } });
  });

  it('enforces the server summary and KRW bounds', () => {
    expect(validateRepairIntake({ ...valid, detail: '가'.repeat(500) }).valid).toBe(false);
    expect(validateRepairIntake({ ...valid, requestedAmountKrw: '100000000' }).valid).toBe(true);
    expect(validateRepairIntake({ ...valid, requestedAmountKrw: '100000001' })).toMatchObject({ valid: false });
  });
});
