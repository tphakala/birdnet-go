/**
 * Process list for the System overview: the rows, whether the first answer has
 * arrived, the last failure, and the Show All switch, kept in one place so they
 * agree.
 *
 * Every load takes a sequence number and only the answer to the latest request is
 * applied, so a periodic refresh that is still running for the other Show All mode
 * cannot overwrite the list after the switch changed. A failed refresh keeps the rows
 * already shown and reports why; the next success clears it. Changing Show All clears
 * the rows first, so rows from the other mode never stay under the new switch position.
 */

import { t } from '$lib/i18n';
import { api, ApiError } from '$lib/utils/api';
import type { ProcessInfo } from './types';

const PROCESSES_URL = '/api/v2/system/processes';
const ALL_PROCESSES_URL = `${PROCESSES_URL}?all=true`;

/** Process list state for one view; the System overview creates one per mount. */
export interface ProcessList {
  /** Rows of the current Show All mode from its latest successful load; empty until then */
  readonly processes: ProcessInfo[];
  /**
   * False from creation, and after each Show All change, until the newest load answers
   * (successfully or not); then true until the next Show All change
   */
  readonly loaded: boolean;
  /** Why the latest load failed; null after a success and while a Show All change loads */
  readonly error: string | null;
  /** Whether every process is listed, or only the app's own */
  readonly showAll: boolean;
  /** Fetches the list for the current Show All mode; never rejects */
  load: () => Promise<void>;
  /** Switches Show All, clears the other mode's rows and fetches the new mode; never rejects */
  setShowAll: (_value: boolean) => Promise<void>;
}

/**
 * Creates the process list state. Call load() for the first fetch and each refresh, and
 * setShowAll() from the switch; neither rejects, a failure is reported through `error`.
 */
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
      if (value !== showAll) {
        processes = [];
        error = null;
        loaded = false;
      }
      showAll = value;
      return load();
    },
  };
}
