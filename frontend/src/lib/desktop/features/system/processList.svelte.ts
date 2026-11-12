/**
 * Process list for the System overview: the rows, whether the first answer has
 * arrived, the last failure, and the Show All switch, kept in one place so they
 * agree.
 *
 * Every load takes a sequence number and only the answer to the latest request is
 * applied, so a periodic refresh that is still running for the other Show All mode
 * cannot overwrite the list after the switch changed. A failure keeps the rows
 * already shown and reports why; the next success clears it.
 */

import { t } from '$lib/i18n';
import { api, ApiError } from '$lib/utils/api';
import type { ProcessInfo } from './types';

const PROCESSES_URL = '/api/v2/system/processes';
const ALL_PROCESSES_URL = `${PROCESSES_URL}?all=true`;

export interface ProcessList {
  /** Rows from the latest successful load */
  readonly processes: ProcessInfo[];
  /** Whether any load has finished, successfully or not */
  readonly loaded: boolean;
  /** Why the latest load failed, or null after a success */
  readonly error: string | null;
  /** Whether every process is listed, or only the app's own */
  readonly showAll: boolean;
  /** Fetches the list for the current Show All mode; never rejects */
  load: () => Promise<void>;
  /** Switches Show All and fetches the list for the new mode; never rejects */
  setShowAll: (_value: boolean) => Promise<void>;
}

export function createProcessList(): ProcessList {
  let processes = $state<ProcessInfo[]>([]);
  let loaded = $state(false);
  let error = $state<string | null>(null);
  let showAll = $state(false);
  let latestRequest = 0;

  async function load(): Promise<void> {
    const request = ++latestRequest;
    try {
      const rows = await api.get<ProcessInfo[]>(showAll ? ALL_PROCESSES_URL : PROCESSES_URL);
      if (request !== latestRequest) return;
      processes = rows;
      error = null;
    } catch (e) {
      if (request !== latestRequest) return;
      error = e instanceof ApiError ? e.message : t('errors.api.unknownError');
    }
    loaded = true;
  }

  return {
    get processes() {
      return processes;
    },
    get loaded() {
      return loaded;
    },
    get error() {
      return error;
    },
    get showAll() {
      return showAll;
    },
    load,
    setShowAll(value: boolean): Promise<void> {
      showAll = value;
      return load();
    },
  };
}
