declare module '@mobility-reliability/domain-workflows' {
  export function assertRepairTransition(input: {
    from: string;
    to: string;
    role: string;
    workOrder: {
      repairStationId?: string;
      billedAmountKrw?: number;
      submittedAt?: string;
      publicFundingInvolved?: boolean;
      subsidyDecisionId?: string;
    };
  }): true;
  export function projectSubsidyLedger(transactions: Array<{
    transactionId: string;
    transactionType: 'allocation' | 'reservation' | 'execution' | 'release' | 'adjustment' | 'reversal';
    amountKrw: number;
    workOrderId?: string;
    reversesTransactionId?: string;
  }>): {
    allocatedKrw: number;
    adjustmentKrw: number;
    reservedKrw: number;
    executedKrw: number;
    availableKrw: number;
  };
}
