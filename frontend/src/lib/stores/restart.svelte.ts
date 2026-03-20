/**
 * restart.svelte.ts
 *
 * Reactive store for server restart state.
 * Fetches restart availability and pending restart-required status from the backend.
 */
import { api } from '$lib/utils/api';
import { getLogger } from '$lib/utils/logger';

const logger = getLogger('restartStore');

interface RestartStatus {
  binary_restart_available: boolean;
  container_restart_available: boolean;
  restart_required: boolean;
  restart_reasons: string[];
}

/** Reactive restart state */
export const restartState = $state<RestartStatus>({
  binary_restart_available: true,
  container_restart_available: false,
  restart_required: false,
  restart_reasons: [],
});

/** Whether a restart is currently being initiated */
export const restartInProgress = $state({ value: false });

/** Fetch restart status from the backend */
export async function fetchRestartStatus(): Promise<void> {
  try {
    const data = await api.get<RestartStatus>('/api/v2/system/restart-status');
    Object.assign(restartState, data);
  } catch (error) {
    logger.error('Failed to fetch restart status', error);
  }
}

/** Request a binary restart */
export async function requestBinaryRestart(): Promise<boolean> {
  try {
    restartInProgress.value = true;
    await api.post('/api/v2/control/restart-server');
    return true;
  } catch (error) {
    restartInProgress.value = false;
    logger.error('Failed to request binary restart', error);
    return false;
  }
}

/** Request a container restart */
export async function requestContainerRestart(): Promise<boolean> {
  try {
    restartInProgress.value = true;
    await api.post('/api/v2/control/restart-container');
    return true;
  } catch (error) {
    restartInProgress.value = false;
    logger.error('Failed to request container restart', error);
    return false;
  }
}
