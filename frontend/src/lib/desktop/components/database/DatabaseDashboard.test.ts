import { describe, it, expect, vi, beforeEach } from 'vitest';
import { screen, waitFor, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { t } from '$lib/i18n';
import type {
  DatabaseStats,
  MigrationStatus,
  PrerequisiteCheck,
  PrerequisitesResponse,
} from '$lib/types/migration';
import type { DatabaseOverviewResponse } from '$lib/types/database';
import type { LegacyStatus } from '$lib/types/legacy';

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
const LEGACY_STATS_URL = '/api/v2/system/database/stats';
const V2_STATS_URL = '/api/v2/system/database/v2/stats';
const BACKUP_JOBS_URL = '/api/v2/system/database/backup/jobs';
const OVERVIEW_URL = '/api/v2/system/database/overview';
const LEGACY_STATUS_URL = '/api/v2/system/database/legacy/status';
const CLEANUP_URL = '/api/v2/system/database/legacy/cleanup';
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

const v2OnlyStatus = { ...idleStatus, is_v2_only_mode: true } satisfies MigrationStatus;

const databaseStats = {
  type: 'sqlite',
  location: '/data/birdnet.db',
  size_bytes: 1024,
  total_detections: 10,
  connected: true,
} satisfies DatabaseStats;

const overview = {
  engine: 'sqlite',
  status: 'connected',
  location: '/data/birdnet.db',
  size_bytes: 1024,
  total_detections: 10,
  total_tables: 1,
  tables: [],
  performance: {
    read_latency_avg_ms: 1,
    read_latency_max_ms: 2,
    write_latency_avg_ms: 1,
    write_latency_max_ms: 2,
    queries_per_sec: 1,
    queries_last_hour: 60,
    slow_query_count: 0,
  },
  detection_rate_24h: [],
} satisfies DatabaseOverviewResponse;

const cleanableLegacy = {
  exists: true,
  can_cleanup: true,
  reason: '',
  size_bytes: 2048,
  total_records: 10,
  last_modified: null,
  location: '/data/birdnet-legacy.db',
  tables: [],
} satisfies LegacyStatus;

/** Endpoints every view reads besides the migration status; tests override them as needed. */
const healthyRoutes: Record<string, unknown> = {
  [PREREQUISITES_URL]: startablePrerequisites,
  [LEGACY_STATS_URL]: databaseStats,
  [V2_STATS_URL]: databaseStats,
  [BACKUP_JOBS_URL]: { jobs: [] },
  [OVERVIEW_URL]: overview,
  [LEGACY_STATUS_URL]: cleanableLegacy,
};

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
 * Answers a GET by URL from routes on top of healthyRoutes; anything not listed fails, like
 * an unconfigured install.
 */
function answer(url: string, routes: Record<string, unknown> = {}): Promise<unknown> {
  const route = Object.entries({ ...healthyRoutes, ...routes }).find(([prefix]) =>
    url.startsWith(prefix)
  );
  if (!route) return Promise.reject(apiError(NOT_CONFIGURED, 503));
  const [, value] = route;
  return value instanceof Error ? Promise.reject(value) : Promise.resolve(value);
}

/**
 * Serves GETs through answer. The prerequisites run POST answers with the
 * PREREQUISITES_URL route, like the GET does.
 */
function serve(routes: Record<string, unknown>): void {
  servePost({ ...healthyRoutes, ...routes }[PREREQUISITES_URL]);
  apiGet.mockImplementation((url: string) => answer(url, routes));
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
    expect(alert).toHaveTextContent(t('system.database.migration.errors.fetchFailed'));
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
    expect(alert).toHaveTextContent(t('errors.api.unknownError'));
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

    await waitFor(() =>
      expect(apiPost).toHaveBeenCalledWith(START_URL, {
        total_records: databaseStats.total_detections,
      })
    );
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Another migration is already running');
    // A migration that started anyway must show: the status is fetched again after the failure
    const statusRequests = () => apiGet.mock.calls.filter(([url]) => url === STATUS_URL).length;
    await waitFor(() => expect(statusRequests()).toBeGreaterThan(1));
  });

  it('runs the prerequisites again when the start says no run is ready', async () => {
    const user = userEvent.setup();
    serve({ [STATUS_URL]: idleStatus, [PREREQUISITES_URL]: startablePrerequisites });
    const notReady = apiError('Run the pre-migration checks first', 409);
    notReady.errorKey = 'errors.migration.prerequisitesNotReady';
    rejectActions(notReady);

    renderTyped(DatabaseDashboard);

    await user.click(
      await screen.findByRole('button', { name: t('system.database.migration.actions.start') })
    );
    const dialog = await screen.findByRole('dialog', {
      name: t('system.database.migration.confirmDialog.title'),
    });
    await user.click(within(dialog).getByRole('checkbox'));
    apiPost.mockClear();
    await user.click(
      within(dialog).getByRole('button', { name: t('system.database.migration.actions.start') })
    );

    await waitFor(() => expect(apiPost).toHaveBeenCalledWith(RUN_URL, { force: false }));
  });

  it('forces a new run when the start finds a failed check, and shows it', async () => {
    const user = userEvent.setup();
    const failedCheck = {
      id: 'disk_space',
      name: 'Disk Space',
      description: '',
      status: 'failed',
      message: 'Only 0.1GB available',
      severity: 'critical',
    } satisfies PrerequisiteCheck;
    const blocked = {
      ...startablePrerequisites,
      all_passed: false,
      can_start_migration: false,
      critical_failures: 1,
      checks: [failedCheck],
      run_id: 2,
    } satisfies PrerequisitesResponse;
    apiGet.mockImplementation((url: string) =>
      url === STATUS_URL
        ? Promise.resolve(idleStatus)
        : Promise.reject(apiError(NOT_CONFIGURED, 503))
    );
    let runs = 0;
    const refused = apiError('Pre-migration check failed: Disk Space', 400);
    refused.errorKey = 'errors.migration.preFlightFailed';
    apiPost.mockImplementation((url: string) => {
      if (url !== RUN_URL) return Promise.reject(refused);
      runs += 1;
      return Promise.resolve(runs === 1 ? startablePrerequisites : blocked);
    });

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

    await waitFor(() => expect(apiPost).toHaveBeenCalledWith(RUN_URL, { force: true }));
    // The real failing check replaces the stale all-passed run, and Start follows it
    expect(await screen.findByText('Disk Space:')).toBeInTheDocument();
    await waitFor(() =>
      expect(
        screen
          .getAllByRole('button', { name: t('system.database.migration.actions.start') })
          .some(b => b.getAttribute('aria-disabled') === 'true')
      ).toBe(true)
    );
    // The refusal stays on screen while and after the re-run
    expect(screen.getByText('Pre-migration check failed: Disk Space')).toBeInTheDocument();
  });

  /** Opens the confirm dialog from the card's Start button and confirms it. */
  async function confirmStart(user: ReturnType<typeof userEvent.setup>): Promise<HTMLElement> {
    // The confirm dialog has a Start button of its own; the card's is the one outside it
    const start = await waitFor(() => {
      const card = screen
        .getAllByRole('button', { name: t('system.database.migration.actions.start') })
        .find(b => !b.closest('[role="dialog"]'));
      if (!card) throw new Error('card Start button not rendered');
      return card;
    });
    await user.click(start);
    const dialog = await screen.findByRole('dialog', {
      name: t('system.database.migration.confirmDialog.title'),
    });
    await user.click(within(dialog).getByRole('checkbox'));
    await user.click(
      within(dialog).getByRole('button', { name: t('system.database.migration.actions.start') })
    );
    return start;
  }

  const diskFailure = {
    id: 'disk_space',
    name: 'Disk Space',
    description: '',
    status: 'failed',
    message: 'Only 0.1GB available',
    severity: 'critical',
  } satisfies PrerequisiteCheck;
  const blockedRun = {
    ...startablePrerequisites,
    all_passed: false,
    can_start_migration: false,
    critical_failures: 1,
    checks: [diskFailure],
    run_id: 2,
  } satisfies PrerequisitesResponse;

  it('keeps the Start button, focused and never natively disabled, through a refused start', async () => {
    const user = userEvent.setup();
    apiGet.mockImplementation((url: string) =>
      url === STATUS_URL
        ? Promise.resolve(idleStatus)
        : Promise.reject(apiError(NOT_CONFIGURED, 503))
    );
    let runs = 0;
    let refuse: (e: Error) => void = () => {};
    const pending = new Promise<never>((_, reject) => (refuse = reject));
    apiPost.mockImplementation((url: string) => {
      if (url === START_URL) return pending;
      runs += 1;
      return Promise.resolve(runs === 1 ? startablePrerequisites : blockedRun);
    });

    renderTyped(DatabaseDashboard);
    const start = await confirmStart(user);

    // While the request runs the button stays focusable: aria-disabled, not disabled
    await waitFor(() => expect(start).toHaveAttribute('aria-disabled', 'true'));
    expect(start).not.toBeDisabled();

    const refused = apiError('Pre-migration check failed: Disk Space', 400);
    refused.errorKey = 'errors.migration.preFlightFailed';
    refuse(refused);

    await screen.findByText('Disk Space:');
    // The same element survives the refusal and keeps keyboard focus; it now says why it is blocked
    expect(start.isConnected).toBe(true);
    expect(document.activeElement).toBe(start);
    expect(start).toHaveAttribute('aria-disabled', 'true');
    const reason = document.getElementById(start.getAttribute('aria-describedby') ?? '');
    expect(reason).toHaveTextContent(
      t('system.database.migration.progress.prerequisitesFailedList', { checks: 'Disk Space' })
    );
  });

  it('clears the refusal when a run with the same id but a new start time allows the start', async () => {
    // A server restart restarts the run ids, so a run id alone cannot tell runs apart
    const user = userEvent.setup();
    apiGet.mockImplementation((url: string) =>
      url === STATUS_URL
        ? Promise.resolve(idleStatus)
        : Promise.reject(apiError(NOT_CONFIGURED, 503))
    );
    let runs = 0;
    const notReady = apiError('Run the pre-migration checks first', 409);
    notReady.errorKey = 'errors.migration.prerequisitesNotReady';
    apiPost.mockImplementation((url: string) => {
      if (url === START_URL) return Promise.reject(notReady);
      runs += 1;
      const startedAt = runs === 1 ? '2026-11-02T10:00:00Z' : '2026-11-02T11:00:00Z';
      return Promise.resolve({ ...startablePrerequisites, run_id: 1, started_at: startedAt });
    });

    renderTyped(DatabaseDashboard);
    await confirmStart(user);

    await waitFor(() => expect(runs).toBe(2));
    await waitFor(() =>
      expect(screen.queryByText('Run the pre-migration checks first')).not.toBeInTheDocument()
    );
  });

  it('does not run the checks again when a start times out', async () => {
    const user = userEvent.setup();
    serve({ [STATUS_URL]: idleStatus, [PREREQUISITES_URL]: startablePrerequisites });
    const timedOut = apiError('The migration start took too long', 504);
    timedOut.errorKey = 'errors.migration.startFailed';
    rejectActions(timedOut);

    renderTyped(DatabaseDashboard);
    await confirmStart(user);

    await screen.findByText('The migration start took too long');
    // The re-run, if any, comes after the status refetch that follows the refusal: let it settle
    const statusRequests = () => apiGet.mock.calls.filter(([url]) => url === STATUS_URL).length;
    await waitFor(() => expect(statusRequests()).toBeGreaterThan(1));
    await new Promise(resolve => setTimeout(resolve, 0));
    const runPosts = () => apiPost.mock.calls.filter(([url]) => url === RUN_URL).length;
    expect(runPosts()).toBe(1);
  });

  it('clears the refusal message once a new run allows the start, and keeps it while it fails', async () => {
    const user = userEvent.setup();
    apiGet.mockImplementation((url: string) =>
      url === STATUS_URL
        ? Promise.resolve(idleStatus)
        : Promise.reject(apiError(NOT_CONFIGURED, 503))
    );
    let runs = 0;
    const refused = apiError('Pre-migration check failed: Disk Space', 400);
    refused.errorKey = 'errors.migration.preFlightFailed';
    apiPost.mockImplementation((url: string) => {
      if (url === START_URL) return Promise.reject(refused);
      runs += 1;
      if (runs === 1) return Promise.resolve(startablePrerequisites);
      // The forced re-run after the refusal still fails; the user's Re-run passes
      return Promise.resolve(runs === 2 ? blockedRun : { ...startablePrerequisites, run_id: 3 });
    });

    renderTyped(DatabaseDashboard);
    await confirmStart(user);

    const banner = await screen.findByText('Pre-migration check failed: Disk Space');
    await screen.findByText('Disk Space:');
    expect(banner).toBeInTheDocument();

    await user.click(
      screen.getByRole('button', { name: t('system.database.migration.prerequisites.rerun') })
    );

    await waitFor(() =>
      expect(screen.queryByText('Pre-migration check failed: Disk Space')).toBeNull()
    );
  });

  it('keeps a start failure that is not a refused check when a later run passes', async () => {
    const user = userEvent.setup();
    apiGet.mockImplementation((url: string) =>
      url === STATUS_URL
        ? Promise.resolve(idleStatus)
        : Promise.reject(apiError(NOT_CONFIGURED, 503))
    );
    const failed = apiError('Failed to initialize migration', 500);
    failed.errorKey = 'errors.migration.initFailed';
    let runs = 0;
    apiPost.mockImplementation((url: string) => {
      if (url === START_URL) return Promise.reject(failed);
      runs += 1;
      return Promise.resolve(
        runs === 1 ? startablePrerequisites : { ...startablePrerequisites, run_id: 3 }
      );
    });

    renderTyped(DatabaseDashboard);
    await confirmStart(user);
    await screen.findByText('Failed to initialize migration');

    // The checks were fine, so a later passing run says nothing about this failure
    await user.click(
      screen.getByRole('button', { name: t('system.database.migration.prerequisites.rerun') })
    );
    await waitFor(() => expect(runs).toBe(2));
    await new Promise(resolve => setTimeout(resolve, 0));
    expect(screen.getByText('Failed to initialize migration')).toBeInTheDocument();
  });

  it('ignores an older status response that arrives after a newer one, so no state change is faked', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
      let statusCalls = 0;
      let resolveOldPoll: (value: unknown) => void = () => {};
      apiGet.mockImplementation((url: string) => {
        if (url === PREREQUISITES_URL) return Promise.resolve(startablePrerequisites);
        if (url !== STATUS_URL) return Promise.reject(apiError(NOT_CONFIGURED, 503));
        statusCalls += 1;
        // Call 1 loads the page, call 2 is the poll that stays pending, call 3 is the refetch
        // after Pause and answers first
        if (statusCalls === 2) {
          return new Promise(resolve => {
            resolveOldPoll = resolve;
          });
        }
        return Promise.resolve(migratingStatus);
      });
      servePost(startablePrerequisites);
      const runPosts = () => apiPost.mock.calls.filter(([url]) => url === RUN_URL).length;

      renderTyped(DatabaseDashboard);
      const pause = await screen.findByRole('button', {
        name: t('system.database.migration.actions.pause'),
      });
      await vi.advanceTimersByTimeAsync(STATUS_POLL_INTERVAL_MS);
      await waitFor(() => expect(statusCalls).toBe(2));
      const before = runPosts();

      await user.click(pause);
      await waitFor(() => expect(statusCalls).toBeGreaterThanOrEqual(3));

      // The older poll answers last, with a state the newer answer already moved past
      resolveOldPoll(idleStatus);
      await vi.advanceTimersByTimeAsync(0);

      expect(runPosts()).toBe(before);
      expect(
        screen.getByRole('button', { name: t('system.database.migration.actions.pause') })
      ).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it('runs the prerequisites again when the migration state returns to idle', async () => {
    const user = userEvent.setup();
    const pausedStatus = {
      ...idleStatus,
      state: 'paused',
      worker_paused: true,
      can_start: false,
      can_resume: true,
      can_cancel: true,
    } satisfies MigrationStatus;
    let statusCalls = 0;
    apiGet.mockImplementation((url: string) => {
      if (url === STATUS_URL) {
        statusCalls += 1;
        return Promise.resolve(statusCalls === 1 ? pausedStatus : idleStatus);
      }
      if (url === PREREQUISITES_URL) return Promise.resolve(startablePrerequisites);
      return Promise.reject(apiError(NOT_CONFIGURED, 503));
    });
    servePost(startablePrerequisites);

    renderTyped(DatabaseDashboard);

    await user.click(
      await screen.findByRole('button', { name: t('system.database.migration.actions.cancel') })
    );

    // One run on mount, one more once the state is idle again
    const runPosts = () => apiPost.mock.calls.filter(([url]) => url === RUN_URL).length;
    await waitFor(() => expect(runPosts()).toBe(2));
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
      return answer(url, { [PREREQUISITES_URL]: startablePrerequisites });
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
        return answer(url);
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
      if (url !== PREREQUISITES_URL) return answer(url);
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
        if (url !== PREREQUISITES_URL) return answer(url);
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
        if (url !== PREREQUISITES_URL) return answer(url);
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
            : answer(url, { [PREREQUISITES_URL]: apiError(NOT_CONFIGURED, 503) })
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
        : answer(url, { [PREREQUISITES_URL]: apiError(NOT_CONFIGURED, 503) })
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

describe('DatabaseDashboard data errors', () => {
  beforeEach(() => {
    apiGet.mockReset();
    apiPost.mockReset();
    actionOutcome = null;
  });

  it.each([
    {
      surface: 'legacy statistics',
      status: idleStatus,
      failing: { [LEGACY_STATS_URL]: apiError('Legacy stats lost', 500) },
      texts: () => [
        t('system.database.stats.fetchFailed'),
        t('system.database.migration.strip.legacy'),
        'Legacy stats lost',
      ],
    },
    {
      surface: 'v2 statistics',
      status: idleStatus,
      failing: { [V2_STATS_URL]: apiError('V2 stats lost', 500) },
      texts: () => [
        t('system.database.stats.fetchFailed'),
        t('system.database.migration.strip.v2Target'),
        'V2 stats lost',
      ],
    },
    {
      surface: 'backup history',
      status: idleStatus,
      failing: { [BACKUP_JOBS_URL]: apiError('Backups lost', 500) },
      texts: () => [t('system.database.backup.history.loadFailed'), 'Backups lost'],
      // A failed load must not claim there are no backups
      absent: () => [t('system.database.backup.history.noBackups')],
    },
    {
      // Create Backup is disabled while a migration runs, so focus needs another target
      surface: 'backup history during a migration',
      status: migratingStatus,
      failing: { [BACKUP_JOBS_URL]: apiError('Backups lost', 500) },
      texts: () => [t('system.database.backup.history.loadFailed'), 'Backups lost'],
    },
    {
      surface: 'legacy status in the v2-only view',
      status: v2OnlyStatus,
      failing: { [LEGACY_STATUS_URL]: apiError('Legacy lost', 500) },
      texts: () => [t('system.database.legacy.fetchFailed'), 'Legacy lost'],
      // Focus stays by the cleanup card at the bottom, not on the whole page
      focusAway: () => [t('system.database.dashboard.tables.title')],
    },
  ])('shows why the $surface failed and recovers on Retry', async row => {
    const { status, failing, texts } = row;
    const absent = row.absent?.() ?? [];
    const user = userEvent.setup();
    serve({ [STATUS_URL]: status, ...failing });

    renderTyped(DatabaseDashboard);

    const alert = await screen.findByRole('alert');
    for (const text of texts()) expect(alert).toHaveTextContent(text);
    for (const text of absent) expect(screen.queryByText(text)).not.toBeInTheDocument();

    serve({ [STATUS_URL]: status });
    await user.click(within(alert).getByRole('button', { name: t('common.retry') }));

    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
    // The focused Retry button is gone; focus must not fall back to the page body
    expect(document.activeElement).not.toBe(document.body);
    for (const text of row.focusAway?.() ?? []) {
      expect(document.activeElement).not.toHaveTextContent(text);
    }
  });

  it('refetches only the statistic that failed on Retry', async () => {
    const user = userEvent.setup();
    serve({ [STATUS_URL]: idleStatus, [V2_STATS_URL]: apiError('V2 stats lost', 500) });

    renderTyped(DatabaseDashboard);

    const alert = await screen.findByRole('alert');
    const requests = (url: string) => apiGet.mock.calls.filter(([called]) => called === url).length;
    const legacyBefore = requests(LEGACY_STATS_URL);
    const v2Before = requests(V2_STATS_URL);

    serve({ [STATUS_URL]: idleStatus });
    await user.click(within(alert).getByRole('button', { name: t('common.retry') }));

    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
    expect(requests(V2_STATS_URL)).toBe(v2Before + 1);
    expect(requests(LEGACY_STATS_URL)).toBe(legacyBefore);
  });

  it.each([
    {
      situation: 'there is no v2 database',
      first: apiError('Not found', 404),
      polled: apiError('Not found', 404),
    },
    {
      situation: 'a refresh fails while statistics are shown',
      first: databaseStats,
      polled: apiError('V2 refresh lost', 500),
    },
  ])('shows no statistics alert when $situation', async ({ first, polled }) => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      serve({ [STATUS_URL]: migratingStatus, [V2_STATS_URL]: first });

      renderTyped(DatabaseDashboard);
      await screen.findByRole('button', { name: t('system.database.migration.actions.pause') });

      // The active-migration poll refetches the v2 statistics
      serve({ [STATUS_URL]: migratingStatus, [V2_STATS_URL]: polled });
      const v2Requests = () => apiGet.mock.calls.filter(([url]) => url === V2_STATS_URL).length;
      const before = v2Requests();
      await vi.advanceTimersByTimeAsync(STATUS_POLL_INTERVAL_MS * 2);

      expect(v2Requests()).toBeGreaterThan(before);
      expect(screen.queryByText(t('system.database.stats.fetchFailed'))).not.toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it('lets the user dismiss a failed backup request', async () => {
    const user = userEvent.setup();
    serve({ [STATUS_URL]: idleStatus });
    rejectActions(apiError('Disk full', 507));

    renderTyped(DatabaseDashboard);

    await user.click(
      await screen.findByRole('button', {
        name: t('system.database.backup.history.createBackup'),
      })
    );

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent(t('system.database.backup.history.createFailed'));
    expect(alert).toHaveTextContent('Disk full');

    await user.click(within(alert).getByRole('button', { name: t('common.aria.dismissAlert') }));

    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(document.activeElement).not.toBe(document.body);

    // A new attempt clears an earlier refusal
    const create = screen.getByRole('button', {
      name: t('system.database.backup.history.createBackup'),
    });
    await user.click(create);
    await screen.findByRole('alert');
    actionOutcome = null;
    await user.click(create);
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
  });

  it('shows why deleting the legacy database failed and clears it on the next attempt', async () => {
    const user = userEvent.setup();
    serve({ [STATUS_URL]: v2OnlyStatus });
    rejectActions(apiError('Legacy database is still in use', 409));

    renderTyped(DatabaseDashboard);

    const confirmDelete = async () => {
      await user.click(
        await screen.findByRole('button', {
          name: t('system.database.legacy.cleanup.deleteButton'),
        })
      );
      const dialog = await screen.findByRole('dialog', {
        name: t('system.database.legacy.cleanup.confirmTitle'),
      });
      await user.click(within(dialog).getByRole('checkbox'));
      await user.click(
        within(dialog).getByRole('button', {
          name: t('system.database.legacy.cleanup.deleteConfirmButton'),
        })
      );
    };

    await confirmDelete();
    await waitFor(() => expect(apiPost).toHaveBeenCalledWith(CLEANUP_URL));
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent(t('system.database.legacy.cleanup.failed'));
    expect(alert).toHaveTextContent('Legacy database is still in use');

    await user.click(within(alert).getByRole('button', { name: t('common.aria.dismissAlert') }));

    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(document.activeElement).not.toBe(document.body);

    // A refusal left on screen goes away when the next attempt starts
    await confirmDelete();
    await screen.findByRole('alert');
    actionOutcome = null;
    await confirmDelete();
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
  });
  it('says the connection is lost when the v2-only overview cannot load because the browser is offline', async () => {
    connectionState.isOnline = false;
    try {
      serve({ [STATUS_URL]: v2OnlyStatus });

      renderTyped(DatabaseDashboard);

      const alert = await screen.findByRole('alert');
      expect(alert).toHaveTextContent(t('system.database.dashboard.fetchFailed'));
      expect(alert).toHaveTextContent(t('connectivity.offline'));
    } finally {
      connectionState.isOnline = true;
    }
  });

  it('shows why the v2-only overview failed and reloads it with Retry', async () => {
    const user = userEvent.setup();
    serve({ [STATUS_URL]: v2OnlyStatus, [OVERVIEW_URL]: apiError('Overview unavailable', 500) });

    renderTyped(DatabaseDashboard);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent(t('system.database.dashboard.fetchFailed'));
    expect(alert).toHaveTextContent('Overview unavailable');

    serve({ [STATUS_URL]: v2OnlyStatus });
    await user.click(within(alert).getByRole('button', { name: t('common.retry') }));

    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
  });
});
