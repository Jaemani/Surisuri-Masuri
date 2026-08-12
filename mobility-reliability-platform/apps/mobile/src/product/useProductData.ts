import { useCallback, useEffect, useState } from 'react';

import type { CreateRepairRequestInput, ProductRole, ProductSnapshot } from './types';
import { ProductRepositoryError } from './repository';
import type { ProductRepository } from './repository';
import { selectProductView } from './state';

type ProductDataPhase = 'loading' | 'ready' | 'error';

export type ProductDataState = {
  phase: ProductDataPhase;
  snapshot: ProductSnapshot | null;
  errorCode: ProductRepositoryError['code'] | 'UNKNOWN' | null;
};

const initialState: ProductDataState = { phase: 'loading', snapshot: null, errorCode: null };

export function useProductData(repository: ProductRepository) {
  const [state, setState] = useState<ProductDataState>(initialState);

  const refresh = useCallback(async () => {
    setState((current) => ({ ...current, phase: 'loading', errorCode: null }));
    try {
      const snapshot = await repository.getSnapshot();
      setState({ phase: 'ready', snapshot, errorCode: null });
    } catch (error) {
      setState({
        phase: 'error',
        snapshot: null,
        errorCode: error instanceof ProductRepositoryError ? error.code : 'UNKNOWN',
      });
    }
  }, [repository]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const createRepairRequest = useCallback(async (input: CreateRepairRequestInput) => {
    const request = await repository.createRepairRequest(input);
    setState((current) => current.snapshot
      ? { ...current, phase: 'ready', snapshot: { ...current.snapshot, repairRequest: request }, errorCode: null }
      : current);
    return request;
  }, [repository]);

  const setRole = useCallback(async (role: ProductRole) => {
    const roleSession = await repository.setRole(role);
    setState((current) => current.snapshot
      ? { ...current, phase: 'ready', snapshot: { ...current.snapshot, roleSession }, errorCode: null }
      : current);
    return roleSession;
  }, [repository]);

  const view = state.snapshot ? selectProductView(state.snapshot) : null;
  return { ...state, view, refresh, createRepairRequest, setRole };
}
