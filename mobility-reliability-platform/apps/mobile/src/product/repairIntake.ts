import type { CreateRepairRequestInput } from './types';

export const repairCategories = ['바퀴·타이어', '브레이크', '배터리·충전', '조향·주행', '좌석·프레임', '소리·진동', '기타'] as const;
export type RepairCategory = typeof repairCategories[number];

export type RepairIntakeDraft = {
  category: RepairCategory | null;
  detail: string;
  publicFundingInvolved: boolean | null;
  requestedAmountKrw: string;
  idempotencyKey?: string;
};

export type RepairIntakeErrors = { category?: string; detail?: string; publicFundingInvolved?: string; requestedAmountKrw?: string };
export type RepairIntakeValidation = { valid: true; input: CreateRepairRequestInput; errors: null } | { valid: false; input: null; errors: RepairIntakeErrors };

export function validateRepairIntake(draft: RepairIntakeDraft): RepairIntakeValidation {
  const detail = draft.detail.trim();
  const amountText = draft.requestedAmountKrw.trim();
  const errors: RepairIntakeErrors = {};
  if (!draft.category) errors.category = '가장 가까운 증상 분류를 골라 주세요.';
  if (detail.length < 10) errors.detail = '문제가 언제, 어떻게 생겼는지 10자 이상 적어 주세요.';
  const title = draft.category ? `${draft.category}: ${detail}` : detail;
  if (title.length > 500) errors.detail = '분류를 포함해 500자 이내로 적어 주세요.';
  if (draft.publicFundingInvolved === null) errors.publicFundingInvolved = '수리비 지원 신청 여부를 골라 주세요.';
  if (draft.publicFundingInvolved === true && amountText.length === 0) errors.requestedAmountKrw = '지원금을 신청하려면 예상 수리비를 입력해 주세요.';
  if (amountText.length > 0 && (!/^\d+$/.test(amountText) || !validAmount(amountText))) errors.requestedAmountKrw = '1원 이상 1억원 이하의 원 단위 금액을 입력해 주세요.';
  if (Object.keys(errors).length) return { valid: false, input: null, errors };
  const requestedAmountKrw = amountText ? Number(amountText) : undefined;
  return { valid: true, errors: null, input: { title, publicFundingInvolved: draft.publicFundingInvolved!, ...(requestedAmountKrw === undefined ? {} : { requestedAmountKrw }), ...(draft.idempotencyKey ? { idempotencyKey: draft.idempotencyKey } : {}) } };
}

function validAmount(value: string) { const numeric = Number(value); return Number.isSafeInteger(numeric) && numeric > 0 && numeric <= 100_000_000; }
