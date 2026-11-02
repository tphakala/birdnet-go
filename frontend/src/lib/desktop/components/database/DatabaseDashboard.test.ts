import { describe, it, expect, vi, beforeEach } from 'vitest';
import { screen, waitFor, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { t } from '$lib/i18n';
import type { MigrationStatus, PrerequisitesResponse } from '$lib/types/migration';

const { apiGet, apiPost } = vi.hoisted(() => ({
  apiGet: vi.fn(),
  apiPost: vi.fn(),
}));

vi.mock('$lib/utils/api', async importOriginal => ({
  ...(await importOriginal<typeof import('$lib/utils/api')>()),
  api: { get: apiGet, post: apiPost },
}));

vi.mock('$lib/utils/ReconnectingEventSource', () => ({
  ReconnectingEventSource: class {
    addEventListener = vi.fn();
    close = vi.fn();
  },
}));

import { ApiError } from '$lib/utils/api';
import { connectionState } from '$lib/stores/connectionState.svelte';
import DatabaseDashboard from './DatabaseDashboard.svelte';
import { renderTyped } from '../../../../test/render-helpers';

const STATUS_URL = '/api/v2/system/database/migration/status';
const PREREQUISITES_URL = '/api/v2/system/database/migration/prerequisites';
const RUN_URL = '/api/v2/system/database/migration/prerequisites/run';
const START_URL = '/api/v2/system/database/migration/start';
const STATUS_POLL_INTERVAL_MS = 2000;
const PREREQUISITES_POLL_INTERVAL_MS = 2000;
const NOT_CONFIGURED = 'Migration is not configured';

const idleStatus = {
  state: 'idle',
  total_records: 0,
  migrated_records: 0,
  progress_percent: 0,
  worker_running: false,
  worker_paused: false,
  can_start: true,
  can_pause: false,
  can_resume: false,
  can_retry_validation: false,
  can_cancel: false,
  dirty_id_count: 0,
  is_v2_only_mode: false,
} satisfies MigrationStatus;

const migratingStatus = {
  ...idleStatus,
  state: 'migrating',
  worker_running: true,
  can_start: false,
  can_pause: true,
  can_cancel: true,
} satisfies MigrationStatus;

const startablePrerequisites = {
  all_passed: true,
  can_start_migration: true,
  checks: [],
  critical_failures: 0,
  warnings: 0,
  checked_at: '2026-10-24T10:00:00Z',
  status: 'completed',
  run_id: 1,
} satisfies PrerequisitesResponse;

function apiError(message: string, status: number): ApiError {
  return new ApiError(message, status, new Response(null, { status }));
}

/** Outcome of every POST except the prerequisites run: a value resolves, an Error rejects. */
let actionOutcome: unknown = null;

/** Makes every migration action POST (start, pause, ...) fail with error. */
function rejectActions(error: Error): void {
  actionOutcome = error;
}

/** Answers the prerequisites run POST with prerequisites and every other POST with actionOutcome. */
function servePost(prerequisites: unknown): void {
  apiPost.mockImplementation((url: string) => {
    const outcome = url === RUN_URL ? prerequisites : actionOutcome;
    if (outcome === undefined) return Promise.reject(apiError(NOT_CONFIGURED, 503));
    return outcome instanceof Error ? Promise.reject(outcome) : Promise.resolve(outcome);
  });
}

/**
 * Answers each GET by URL; anything not listed fails, like an unconfigured install. The
 * prerequisites run POST answers with the PREREQUISITES_URL route, like the GET does.
 */
function serve(routes: Record<string, unknown>): void {
  servePost(routes[PREREQUISITES_URL]);
  apiGet.mockImplementation((url: string) => {
    const route = Object.entries(routes).find(([prefix]) => url.startsWith(prefix));
    if (!route) return Promise.reject(apiError(NOT_CONFIGURED, 503));
    const [, value] = route;
    return value instanceof Error ? Promise.reject(value) : Promise.resolve(value);
  });
}

describe('DatabaseDashboard migration errors', () => {
  beforeEach(() => {
    apiGet.mockReset();
    apiPost.mockReset();
    actionOutcome = null;
  });

  it('shows the server message when the status request is refused', async () => {
    serve({
      [STATUS_URL]: apiError(NOT_CONFIGURED, 503),
      [PREREQUISITES_URL]: startablePrerequisites,
    });

    renderTyped(DatabaseDashboard);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent(NOT_CONFIGURED);
  });

  it('shows a translated message when the status request fails without an API error', async () => {
    serve({
      [STATUS_URL]: new Error('socket closed'),
      [PREREQUISITES_URL]: startablePrerequisites,
    });

    renderTyped(DatabaseDashboard);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent(t('system.database.migration.errors.fetchFailed'));
    expect(alert).not.toHaveTextContent('socket closed');
  });

  it('shows no alert when the migration status loads', async () => {
    serve({ [STATUS_URL]: idleStatus, [PREREQUISITES_URL]: startablePrerequisites });

    renderTyped(DatabaseDashboard);

    await screen.findByRole('button', { name: t('system.database.migration.actions.start') });
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('shows the message when starting the migration is rejected', async () => {
    const user = userEvent.setup();
    serve({ [STATUS_URL]: idleStatus, [PREREQUISITES_URL]: startablePrerequisites });
    rejectActions(apiError('Another migration is already running', 409));

    renderTyped(DatabaseDashboard);

    await user.click(
      await screen.findByRole('button', { name: t('system.database.migration.actions.start') })
    );
    const dialog = await screen.findByRole('dialog', {
      name: t('system.database.migration.confirmDialog.title'),
    });
    await user.click(within(dialog).getByRole('checkbox'));
    await user.click(
      within(dialog).getByRole('button', { name: t('system.database.migration.actions.start') })
    );

    await waitFor(() => expect(apiPost).toHaveBeenCalledWith(START_URL));
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Another migration is already running');
  });
  it('shows why the migration cannot start when the prerequisites request fails', async () => {
    serve({
      [STATUS_URL]: idleStatus,
      [PREREQUISITES_URL]: apiError('Request timeout.', 408),
    });

    renderTyped(DatabaseDashboard);

    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Request timeout.'));
    const helper = screen.getByText(
      t('system.database.migration.progress.prerequisitesUnavailable')
    );
    // The confirm dialog has its own Start button; the one in the card points at the helper
    const start = screen
      .getAllByRole('button', { name: t('system.database.migration.actions.start') })
      .find(b => b.getAttribute('aria-describedby') === helper.id);
    expect(start).toHaveAttribute('aria-disabled', 'true');
    expect(
      screen.queryByText(t('system.database.migration.progress.prerequisitesNotMet'))
    ).not.toBeInTheDocument();
  });

  it('hides the state cards, keeps the independent panels and offers a retry while the status is unknown', async () => {
    const user = userEvent.setup();
    serve({
      [STATUS_URL]: apiError(NOT_CONFIGURED, 503),
      [PREREQUISITES_URL]: startablePrerequisites,
    });

    renderTyped(DatabaseDashboard);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent(NOT_CONFIGURED);
    // Backups come from their own endpoint and stay; the state cards would claim "Idle"
    expect(screen.getByText(t('system.database.backup.history.title'))).toBeInTheDocument();
    expect(
      screen.queryByText(t('system.database.migration.strip.readyToMigrate'))
    ).not.toBeInTheDocument();

    serve({ [STATUS_URL]: idleStatus, [PREREQUISITES_URL]: startablePrerequisites });
    await user.click(within(alert).getByRole('button', { name: t('common.retry') }));

    await screen.findByText(t('system.database.migration.strip.readyToMigrate'));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    // The Retry button that had focus is gone; focus must not fall back to the page body
    expect(document.activeElement).not.toBe(document.body);
  });

  it('offers Retry when a status refresh fails while an earlier status is still shown', async () => {
    const user = userEvent.setup();
    let statusCalls = 0;
    apiGet.mockImplementation((url: string) => {
      if (url === STATUS_URL) {
        statusCalls += 1;
        return statusCalls === 1
          ? Promise.resolve(idleStatus)
          : Promise.reject(apiError('Status unavailable', 500));
      }
      if (url.startsWith(PREREQUISITES_URL)) return Promise.resolve(startablePrerequisites);
      return Promise.reject(apiError(NOT_CONFIGURED, 503));
    });
    servePost(startablePrerequisites);

    renderTyped(DatabaseDashboard);

    await user.click(
      await screen.findByRole('button', { name: t('system.database.migration.actions.start') })
    );
    const dialog = await screen.findByRole('dialog', {
      name: t('system.database.migration.confirmDialog.title'),
    });
    await user.click(within(dialog).getByRole('checkbox'));
    await user.click(
      within(dialog).getByRole('button', { name: t('system.database.migration.actions.start') })
    );

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Status unavailable');
    expect(screen.getByText(t('system.database.backup.history.title'))).toBeInTheDocument();
    expect(within(alert).getByRole('button', { name: t('common.retry') })).toBeInTheDocument();
  });

  it('lets the user dismiss a failed action message', async () => {
    const user = userEvent.setup();
    serve({ [STATUS_URL]: migratingStatus });
    rejectActions(apiError('Pause refused', 409));

    renderTyped(DatabaseDashboard);

    await user.click(
      await screen.findByRole('button', { name: t('system.database.migration.actions.pause') })
    );
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Pause refused');

    await user.click(within(alert).getByRole('button', { name: t('common.aria.dismissAlert') }));

    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('keeps a failed action message when status polling succeeds afterwards', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
      serve({ [STATUS_URL]: migratingStatus });
      rejectActions(apiError('Pause refused', 409));

      renderTyped(DatabaseDashboard);

      await user.click(
        await screen.findByRole('button', { name: t('system.database.migration.actions.pause') })
      );
      await screen.findByRole('alert');
      const statusRequests = () => apiGet.mock.calls.filter(([url]) => url === STATUS_URL).length;
      const before = statusRequests();

      await vi.advanceTimersByTimeAsync(STATUS_POLL_INTERVAL_MS * 2);

      expect(statusRequests()).toBeGreaterThan(before);
      expect(screen.getByRole('alert')).toHaveTextContent('Pause refused');
    } finally {
      vi.useRealTimers();
    }
  });
  it('marks Retry busy and ignores further clicks while the retried request is pending', async () => {
    const user = userEvent.setup();
    serve({
      [STATUS_URL]: apiError(NOT_CONFIGURED, 503),
      [PREREQUISITES_URL]: startablePrerequisites,
    });

    renderTyped(DatabaseDashboard);

    const retry = await screen.findByRole('button', { name: t('common.retry') });
    let finish: (status: MigrationStatus) => void = () => {};
    const pending = new Promise<MigrationStatus>(resolve => (finish = resolve));
    apiGet.mockClear();
    apiGet.mockReturnValue(pending);

    await user.click(retry);
    await user.click(retry);

    expect(retry).toHaveAttribute('aria-disabled', 'true');
    expect(retry).toHaveTextContent(t('common.retrying'));
    expect(apiGet).toHaveBeenCalledTimes(1);

    serve({ [STATUS_URL]: idleStatus, [PREREQUISITES_URL]: startablePrerequisites });
    finish(idleStatus);

    await screen.findByText(t('system.database.backup.history.title'));
  });
});

describe('DatabaseDashboard prerequisites run', () => {
  beforeEach(() => {
    apiGet.mockReset();
    apiPost.mockReset();
    actionOutcome = null;
  });

  const runningSnapshot = (completedSteps: number) =>
    ({
      all_passed: false,
      can_start_migration: false,
      checks: [],
      critical_failures: 0,
      warnings: 0,
      status: 'running',
      run_id: 1,
      progress: {
        completed_steps: completedSteps,
        total_steps: 9,
        current_check_name: 'Disk Space',
      },
    }) satisfies PrerequisitesResponse;

  /** True while the card's Start button is blocked (the confirm dialog is closed). */
  const startBlocked = () =>
    screen
      .getAllByRole('button', { name: t('system.database.migration.actions.start') })
      .some(b => b.getAttribute('aria-disabled') === 'true');

  it('starts the run on mount, polls until it completes, enables Start and stops polling', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      let polls = 0;
      apiGet.mockImplementation((url: string) => {
        if (url === STATUS_URL) return Promise.resolve(idleStatus);
        if (url === PREREQUISITES_URL) {
          polls += 1;
          return Promise.resolve(polls === 1 ? runningSnapshot(3) : startablePrerequisites);
        }
        return Promise.reject(apiError(NOT_CONFIGURED, 503));
      });
      apiPost.mockImplementation((url: string) =>
        url === RUN_URL
          ? Promise.resolve(runningSnapshot(0))
          : Promise.reject(apiError(NOT_CONFIGURED, 503))
      );

      renderTyped(DatabaseDashboard);

      await waitFor(() => expect(apiPost).toHaveBeenCalledWith(RUN_URL, { force: false }));
      await waitFor(() => expect(startBlocked()).toBe(true));

      await vi.advanceTimersByTimeAsync(PREREQUISITES_POLL_INTERVAL_MS);
      expect(polls).toBeGreaterThanOrEqual(1);
      expect(startBlocked()).toBe(true);

      await vi.advanceTimersByTimeAsync(PREREQUISITES_POLL_INTERVAL_MS);
      await waitFor(() => expect(startBlocked()).toBe(false));

      // Polling stops once the run is completed
      const settledPolls = polls;
      await vi.advanceTimersByTimeAsync(PREREQUISITES_POLL_INTERVAL_MS * 2);
      expect(polls).toBe(settledPolls);
    } finally {
      vi.useRealTimers();
    }
  });

  /**
   * Serves a running run from the POST and each GET from outcomes in turn, the last one
   * repeating. Returns the number of polls made so far.
   */
  function serveRun(outcomes: unknown[]): () => number {
    let polls = 0;
    apiGet.mockImplementation((url: string) => {
      if (url === STATUS_URL) return Promise.resolve(idleStatus);
      if (url !== PREREQUISITES_URL) return Promise.reject(apiError(NOT_CONFIGURED, 503));
      const outcome = outcomes[Math.min(polls, outcomes.length - 1)];
      polls += 1;
      return outcome instanceof Error ? Promise.reject(outcome) : Promise.resolve(outcome);
    });
    apiPost.mockImplementation((url: string) =>
      url === RUN_URL
        ? Promise.resolve(runningSnapshot(0))
        : Promise.reject(apiError(NOT_CONFIGURED, 503))
    );
    return () => polls;
  }

  it('keeps polling after a poll fails and recovers when it succeeds', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const polls = serveRun([apiError('Status unavailable', 500), startablePrerequisites]);

      renderTyped(DatabaseDashboard);
      await waitFor(() => expect(startBlocked()).toBe(true));

      await vi.advanceTimersByTimeAsync(PREREQUISITES_POLL_INTERVAL_MS);
      await waitFor(() =>
        expect(screen.getByRole('alert')).toHaveTextContent('Status unavailable')
      );
      expect(startBlocked()).toBe(true);

      await vi.advanceTimersByTimeAsync(PREREQUISITES_POLL_INTERVAL_MS);
      await waitFor(() => expect(startBlocked()).toBe(false));
      expect(polls()).toBeGreaterThanOrEqual(2);
      expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it('ignores an older poll that resolves after a newer run request', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      // Poll 1 fails so Retry shows; poll 2 is held pending while Retry forces a run
      let polls = 0;
      let resolveOldPoll: (value: unknown) => void = () => {};
      apiGet.mockImplementation((url: string) => {
        if (url === STATUS_URL) return Promise.resolve(idleStatus);
        if (url !== PREREQUISITES_URL) return Promise.reject(apiError(NOT_CONFIGURED, 503));
        polls += 1;
        if (polls === 1) return Promise.reject(apiError('Status unavailable', 500));
        if (polls === 2) {
          return new Promise(resolve => {
            resolveOldPoll = resolve;
          });
        }
        return Promise.resolve(runningSnapshot(2));
      });
      apiPost.mockImplementation((url: string) =>
        url === RUN_URL
          ? Promise.resolve(runningSnapshot(0))
          : Promise.reject(apiError(NOT_CONFIGURED, 503))
      );
      const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });

      renderTyped(DatabaseDashboard);
      await vi.advanceTimersByTimeAsync(PREREQUISITES_POLL_INTERVAL_MS);
      const alert = await screen.findByRole('alert');
      await vi.advanceTimersByTimeAsync(PREREQUISITES_POLL_INTERVAL_MS);
      await waitFor(() => expect(polls).toBe(2));

      await user.click(within(alert).getByRole('button', { name: t('common.retry') }));
      await waitFor(() => expect(apiPost).toHaveBeenCalledWith(RUN_URL, { force: true }));

      // The older poll answers last, with a completed snapshot
      resolveOldPoll(startablePrerequisites);
      await vi.advanceTimersByTimeAsync(0);
      expect(startBlocked()).toBe(true);

      // Polling went on: the next poll still gets asked
      await vi.advanceTimersByTimeAsync(PREREQUISITES_POLL_INTERVAL_MS);
      expect(polls).toBeGreaterThanOrEqual(3);
      expect(startBlocked()).toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });

  it('sends no poll while a run request is in flight', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      let polls = 0;
      let resolveRun: (value: unknown) => void = () => {};
      apiGet.mockImplementation((url: string) => {
        if (url === STATUS_URL) return Promise.resolve(idleStatus);
        if (url !== PREREQUISITES_URL) return Promise.reject(apiError(NOT_CONFIGURED, 503));
        polls += 1;
        return polls === 1
          ? Promise.reject(apiError('Status unavailable', 500))
          : Promise.resolve(runningSnapshot(2));
      });
      // The mount-time run answers at once; only the forced run from Retry is held
      apiPost.mockImplementation((url: string, body?: { force?: boolean }) => {
        if (url !== RUN_URL) return Promise.reject(apiError(NOT_CONFIGURED, 503));
        if (!body?.force) return Promise.resolve(runningSnapshot(0));
        return new Promise(resolve => {
          resolveRun = resolve;
        });
      });
      const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });

      renderTyped(DatabaseDashboard);
      await vi.advanceTimersByTimeAsync(PREREQUISITES_POLL_INTERVAL_MS);
      const alert = await screen.findByRole('alert');
      await user.click(within(alert).getByRole('button', { name: t('common.retry') }));
      await waitFor(() => expect(apiPost).toHaveBeenCalledWith(RUN_URL, { force: true }));

      const pollsBefore = polls;
      await vi.advanceTimersByTimeAsync(PREREQUISITES_POLL_INTERVAL_MS * 2);
      expect(polls).toBe(pollsBefore);

      // Once the run answers, polling follows the run again
      resolveRun(runningSnapshot(0));
      await vi.advanceTimersByTimeAsync(PREREQUISITES_POLL_INTERVAL_MS);
      expect(polls).toBeGreaterThan(pollsBefore);
    } finally {
      vi.useRealTimers();
    }
  });

  it('skips polls while offline and resumes when back online', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const polls = serveRun([startablePrerequisites]);

      renderTyped(DatabaseDashboard);
      await waitFor(() => expect(startBlocked()).toBe(true));
      connectionState.isOnline = false;

      await vi.advanceTimersByTimeAsync(PREREQUISITES_POLL_INTERVAL_MS * 3);
      expect(polls()).toBe(0);

      connectionState.isOnline = true;
      await vi.advanceTimersByTimeAsync(PREREQUISITES_POLL_INTERVAL_MS);
      await waitFor(() => expect(startBlocked()).toBe(false));
      expect(polls()).toBeGreaterThanOrEqual(1);
    } finally {
      connectionState.isOnline = true;
      vi.useRealTimers();
    }
  });

  it('stops polling when the dashboard is unmounted', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const polls = serveRun([runningSnapshot(1)]);

      const { unmount } = renderTyped(DatabaseDashboard);
      await waitFor(() => expect(startBlocked()).toBe(true));
      await vi.advanceTimersByTimeAsync(PREREQUISITES_POLL_INTERVAL_MS);
      expect(polls()).toBeGreaterThanOrEqual(1);

      unmount();
      const atUnmount = polls();
      await vi.advanceTimersByTimeAsync(PREREQUISITES_POLL_INTERVAL_MS * 3);

      expect(polls()).toBe(atUnmount);
    } finally {
      vi.useRealTimers();
    }
  });

  it('starts the run again when a poll finds the server has no run', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const idleSnapshot = {
        ...startablePrerequisites,
        all_passed: false,
        can_start_migration: false,
        status: 'idle',
        run_id: 0,
      } satisfies PrerequisitesResponse;
      apiGet.mockImplementation((url: string) =>
        url === STATUS_URL
          ? Promise.resolve(idleStatus)
          : url === PREREQUISITES_URL
            ? Promise.resolve(idleSnapshot)
            : Promise.reject(apiError(NOT_CONFIGURED, 503))
      );
      let runs = 0;
      apiPost.mockImplementation((url: string) => {
        if (url !== RUN_URL) return Promise.reject(apiError(NOT_CONFIGURED, 503));
        runs += 1;
        return Promise.resolve(runs === 1 ? runningSnapshot(1) : startablePrerequisites);
      });

      renderTyped(DatabaseDashboard);

      await waitFor(() => expect(runs).toBe(1));
      await vi.advanceTimersByTimeAsync(PREREQUISITES_POLL_INTERVAL_MS);

      await waitFor(() => expect(runs).toBe(2));
      expect(apiPost).toHaveBeenLastCalledWith(RUN_URL, { force: false });
      await waitFor(() => expect(startBlocked()).toBe(false));
    } finally {
      vi.useRealTimers();
    }
  });

  it('shows the error and a Retry that forces a run when the first POST fails', async () => {
    const user = userEvent.setup();
    apiGet.mockImplementation((url: string) =>
      url === STATUS_URL
        ? Promise.resolve(idleStatus)
        : Promise.reject(apiError(NOT_CONFIGURED, 503))
    );
    let runs = 0;
    apiPost.mockImplementation((url: string) => {
      if (url !== RUN_URL) return Promise.reject(apiError(NOT_CONFIGURED, 503));
      runs += 1;
      return runs === 1
        ? Promise.reject(apiError('Request timeout.', 408))
        : Promise.resolve(startablePrerequisites);
    });

    renderTyped(DatabaseDashboard);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Request timeout.');
    expect(startBlocked()).toBe(true);
    expect(
      screen.getByText(t('system.database.migration.progress.prerequisitesUnavailable'))
    ).toBeInTheDocument();

    await user.click(within(alert).getByRole('button', { name: t('common.retry') }));

    await waitFor(() => expect(apiPost).toHaveBeenLastCalledWith(RUN_URL, { force: true }));
    await waitFor(() => expect(startBlocked()).toBe(false));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('forces a new run from Re-run', async () => {
    const user = userEvent.setup();
    serve({ [STATUS_URL]: idleStatus, [PREREQUISITES_URL]: startablePrerequisites });

    renderTyped(DatabaseDashboard);

    await user.click(
      await screen.findByRole('button', {
        name: t('system.database.migration.prerequisites.rerun'),
      })
    );

    await waitFor(() => expect(apiPost).toHaveBeenCalledWith(RUN_URL, { force: true }));
  });

  it('blocks Start while a forced run request is in flight over an old passing snapshot', async () => {
    const user = userEvent.setup();
    serve({ [STATUS_URL]: idleStatus, [PREREQUISITES_URL]: startablePrerequisites });

    renderTyped(DatabaseDashboard);
    const rerun = await screen.findByRole('button', {
      name: t('system.database.migration.prerequisites.rerun'),
    });
    await waitFor(() => expect(startBlocked()).toBe(false));

    // The forced run POST stays pending: the old passing snapshot must not enable Start
    let resolveRun: (value: unknown) => void = () => {};
    apiPost.mockImplementation((url: string) =>
      url === RUN_URL
        ? new Promise(resolve => {
            resolveRun = resolve;
          })
        : Promise.reject(apiError(NOT_CONFIGURED, 503))
    );
    await user.click(rerun);

    await waitFor(() => expect(startBlocked()).toBe(true));

    resolveRun(startablePrerequisites);
    await waitFor(() => expect(startBlocked()).toBe(false));
  });

  it('keeps Start blocked and says the checks did not finish when the run failed', async () => {
    serve({
      [STATUS_URL]: idleStatus,
      [PREREQUISITES_URL]: {
        ...startablePrerequisites,
        can_start_migration: false,
        status: 'failed',
        error: 'The checks took longer than the time limit',
      },
    });

    renderTyped(DatabaseDashboard);

    const expected = t('system.database.migration.prerequisites.runFailed', {
      error: 'The checks took longer than the time limit',
    });
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(expected));
    expect(startBlocked()).toBe(true);
    // The reason under Start comes from the dashboard, not from the panel's own alert
    expect(
      screen.getByText(t('system.database.migration.progress.prerequisitesUnavailable'))
    ).toBeInTheDocument();
  });
});
