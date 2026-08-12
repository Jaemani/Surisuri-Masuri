import type { RepairWorkItem } from './types';

export type WorkItemDraft = {
  id: string;
  categoryCode: RepairWorkItem['categoryCode'];
  actionCode: RepairWorkItem['actionCode'];
  quantityText: string;
  lineAmountText: string;
};

export function emptyWorkItemDraft(id = 'item-1'): WorkItemDraft {
  return { id, categoryCode: 'wheel_tire', actionCode: 'repair', quantityText: '1', lineAmountText: '' };
}

export function draftsFromRepairItems(items: RepairWorkItem[]): WorkItemDraft[] {
  if (items.length === 0) return [emptyWorkItemDraft()];
  return items.slice(0, 20).map((item, index) => ({
    id: `item-${index + 1}`,
    categoryCode: item.categoryCode,
    actionCode: item.actionCode,
    quantityText: String(item.quantity),
    lineAmountText: String(item.lineAmountKrw),
  }));
}

export function parseWorkItemDrafts(drafts: WorkItemDraft[]): { items: RepairWorkItem[]; total: number; valid: boolean } {
  if (drafts.length < 1 || drafts.length > 20) return { items: [], total: 0, valid: false };
  const items: RepairWorkItem[] = [];
  let total = 0;
  for (const draft of drafts) {
    const quantity = Number(draft.quantityText);
    const lineAmountKrw = Number(draft.lineAmountText);
    if (!Number.isSafeInteger(quantity) || quantity < 1 || quantity > 20 || !Number.isSafeInteger(lineAmountKrw) || lineAmountKrw < 0 || lineAmountKrw > 100_000_000) {
      return { items: [], total: 0, valid: false };
    }
    const categoryLabel = categoryLabels[draft.categoryCode];
    const actionLabel = actionLabels[draft.actionCode];
    items.push({ categoryCode: draft.categoryCode, categoryLabel, actionCode: draft.actionCode, actionLabel, quantity, lineAmountKrw });
    total += lineAmountKrw;
    if (!Number.isSafeInteger(total) || total > 100_000_000) return { items: [], total: 0, valid: false };
  }
  return { items, total, valid: total > 0 };
}

export const categoryLabels: Record<RepairWorkItem['categoryCode'], string> = {
  wheel_tire: '바퀴·타이어', battery: '배터리', brakes: '브레이크', controls: '조작부', seat_frame: '시트·프레임', other: '기타',
};

export const actionLabels: Record<RepairWorkItem['actionCode'], string> = {
  inspect: '점검', adjust: '조정', repair: '수리', replace: '교체',
};
