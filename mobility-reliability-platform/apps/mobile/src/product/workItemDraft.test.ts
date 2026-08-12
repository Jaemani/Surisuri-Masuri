import { describe, expect, it } from 'vitest';

import { draftsFromRepairItems, emptyWorkItemDraft, parseWorkItemDrafts } from './workItemDraft';

describe('structured repair work item drafts', () => {
  it('parses multiple items and derives the billed total', () => {
    const result = parseWorkItemDrafts([
      { ...emptyWorkItemDraft('one'), categoryCode: 'brakes', actionCode: 'repair', lineAmountText: '85000' },
      { ...emptyWorkItemDraft('two'), categoryCode: 'battery', actionCode: 'replace', quantityText: '2', lineAmountText: '30000' },
    ]);
    expect(result.valid).toBe(true);
    expect(result.total).toBe(115000);
    expect(result.items).toEqual([
      { categoryCode: 'brakes', categoryLabel: '브레이크', actionCode: 'repair', actionLabel: '수리', quantity: 1, lineAmountKrw: 85000 },
      { categoryCode: 'battery', categoryLabel: '배터리', actionCode: 'replace', actionLabel: '교체', quantity: 2, lineAmountKrw: 30000 },
    ]);
  });

  it('rejects empty, over-limit, invalid quantity and invalid totals', () => {
    expect(parseWorkItemDrafts([]).valid).toBe(false);
    expect(parseWorkItemDrafts(Array.from({ length: 21 }, (_, index) => ({ ...emptyWorkItemDraft(String(index)), lineAmountText: '1' }))).valid).toBe(false);
    expect(parseWorkItemDrafts([{ ...emptyWorkItemDraft(), quantityText: '0', lineAmountText: '1000' }]).valid).toBe(false);
    expect(parseWorkItemDrafts([{ ...emptyWorkItemDraft(), lineAmountText: '0' }]).valid).toBe(false);
    expect(parseWorkItemDrafts([{ ...emptyWorkItemDraft(), lineAmountText: '100000001' }]).valid).toBe(false);
  });

  it('restores bounded editable drafts from server projection items', () => {
    const drafts = draftsFromRepairItems([{ categoryCode: 'controls', categoryLabel: '조작부', actionCode: 'adjust', actionLabel: '조정', quantity: 1, lineAmountKrw: 12000 }]);
    expect(drafts[0]).toMatchObject({ categoryCode: 'controls', actionCode: 'adjust', quantityText: '1', lineAmountText: '12000' });
  });
});
