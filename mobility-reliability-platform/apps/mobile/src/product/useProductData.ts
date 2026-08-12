import { useCallback, useEffect, useState } from 'react';

import { isRepairerProductSnapshot } from './types';
import type { CreateRepairRequestInput, ProductRole, ProductSnapshot, RepairerJobCommand } from './types';
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
      && current.snapshot.roleSession.role === 'user'
      ? { ...current, phase: 'ready', snapshot: { ...current.snapshot, repairRequest: request }, errorCode: null }
      : current);
    return request;
  }, [repository]);

  const setRole = useCallback(async (role: ProductRole) => {
    const roleSession = await repository.setRole(role);
    const snapshot = await repository.getSnapshot();
    setState({ phase: 'ready', snapshot, errorCode: null });
    return roleSession;
  }, [repository]);

  const transitionRepairJob = useCallback(async (input: RepairerJobCommand) => {
    const job = await repository.transitionRepairJob(input);
    setState((current) => {
      const snapshot = current.snapshot;
      if (!snapshot || !isRepairerProductSnapshot(snapshot)) return current;
      return { ...current, phase: 'ready', snapshot: { ...snapshot, repairJobs: snapshot.repairJobs.map((candidate) => candidate.id === job.id ? job : candidate) }, errorCode: null };
    });
    return job;
  }, [repository]);

  const view = state.snapshot ? selectProductView(state.snapshot) : null;
  return { ...state, view, refresh, createRepairRequest, transitionRepairJob, setRole };
}
