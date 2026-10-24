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
import DatabaseDashboard from './DatabaseDashboard.svelte';
import { renderTyped } from '../../../../test/render-helpers';

const STATUS_URL = '/api/v2/system/database/migration/status';
const PREREQUISITES_URL = '/api/v2/system/database/migration/prerequisites';
const START_URL = '/api/v2/system/database/migration/start';
const STATUS_POLL_INTERVAL_MS = 2000;
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
} satisfies PrerequisitesResponse;

function apiError(message: string, status: number): ApiError {
  return new ApiError(message, status, new Response(null, { status }));
}

/** Answers each GET by URL; anything not listed fails, like an unconfigured install. */
function serve(routes: Record<string, unknown>): void {
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
  });

  it('shows the server message when the status request is refused', async () => {
    serve({ [STATUS_URL]: apiError(NOT_CONFIGURED, 503) });

    renderTyped(DatabaseDashboard);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent(NOT_CONFIGURED);
  });

  it('shows a translated message when the status request fails without an API error', async () => {
    serve({ [STATUS_URL]: new Error('socket closed') });

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
    apiPost.mockRejectedValue(apiError('Another migration is already running', 409));

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
    apiPost.mockResolvedValue(null);

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
    apiPost.mockRejectedValue(apiError('Pause refused', 409));

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
      apiPost.mockRejectedValue(apiError('Pause refused', 409));

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
    serve({ [STATUS_URL]: apiError(NOT_CONFIGURED, 503) });

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
