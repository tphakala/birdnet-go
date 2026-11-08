import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { cleanup, screen } from '@testing-library/svelte';
import { t, getLocale } from '$lib/i18n';
import { renderTyped } from '../../../../../test/render-helpers';
import DatabaseMigrationProgress from './DatabaseMigrationProgress.svelte';
import type {
  MigrationStatus,
  PrerequisiteCheck,
  PrerequisitesResponse,
} from '$lib/types/migration';

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

const diskSpace = {
  id: 'disk_space',
  name: 'Disk Space',
  description: '',
  status: 'failed',
  message: 'Not enough free space',
  severity: 'critical',
} satisfies PrerequisiteCheck;

const recordCount = {
  id: 'record_count',
  name: 'Record Count',
  description: '',
  status: 'passed',
  message: 'Counted',
  severity: 'critical',
} satisfies PrerequisiteCheck;

const blockedPrerequisites = {
  all_passed: false,
  can_start_migration: false,
  checks: [diskSpace, recordCount],
  critical_failures: 1,
  warnings: 0,
  checked_at: '2026-10-24T10:00:00Z',
  status: 'completed',
  run_id: 1,
} satisfies PrerequisitesResponse;

const databaseIntegrity = {
  id: 'database_integrity',
  name: 'Database Integrity',
  description: '',
  status: 'error',
  message: 'Check could not run',
  severity: 'critical',
} satisfies PrerequisiteCheck;

const optionalCheck = {
  id: 'optional',
  name: 'Optional Tweak',
  description: '',
  status: 'failed',
  message: 'Not required',
  severity: 'warning',
} satisfies PrerequisiteCheck;

const severalBlocked = {
  ...blockedPrerequisites,
  checks: [diskSpace, optionalCheck, recordCount, databaseIntegrity],
  critical_failures: 2,
} satisfies PrerequisitesResponse;

const blockedWithoutFailedCheck = {
  ...blockedPrerequisites,
  checks: [recordCount],
  critical_failures: 0,
} satisfies PrerequisitesResponse;

const startKey = 'system.database.migration.actions.start';

/** Renders a key with its parameters, so a test sees which values reached the text. */
function render(key: string, params?: Record<string, unknown>): string {
  return params ? `${key} ${JSON.stringify(params)}` : key;
}
const originalT = vi.mocked(t).getMockImplementation();
const noop = async () => {};

interface ProgressOverrides {
  prerequisites: PrerequisitesResponse | null;
  loading: boolean;
  error: string | null;
  onStart?: () => void;
}

function renderProgress({ prerequisites, loading, error, onStart = () => {} }: ProgressOverrides) {
  return renderTyped(DatabaseMigrationProgress, {
    props: {
      status: idleStatus,
      legacyStats: null,
      v2Stats: null,
      prerequisites,
      prerequisitesLoading: loading,
      prerequisitesError: error,
      onStart,
      onPause: noop,
      onResume: noop,
      onRetryValidation: noop,
      onCancel: noop,
    },
  });
}

const allowedPrerequisites = {
  ...blockedPrerequisites,
  can_start_migration: true,
  all_passed: true,
} satisfies PrerequisitesResponse;

interface StartStateCase {
  name: string;
  prerequisites: PrerequisitesResponse | null;
  loading: boolean;
  error: string | null;
  helper: string;
  /** Header badge text; the idle label falls back to its title-cased status in this test. */
  badge: string;
  absent: string[];
  /** Text colour class the helper must carry. */
  tone: string;
}

const notMet = render('system.database.migration.progress.prerequisitesNotMet');

const cases: StartStateCase[] = [
  {
    name: 'loading',
    badge: 'system.database.migration.status.checking',
    prerequisites: null,
    loading: true,
    error: null,
    helper: render('system.database.migration.prerequisites.running'),
    absent: [notMet],
    tone: 'text-muted',
  },
  {
    name: 'loading with a stale blocked result',
    badge: 'system.database.migration.status.checking',
    prerequisites: blockedPrerequisites,
    loading: true,
    error: null,
    helper: render('system.database.migration.prerequisites.running'),
    absent: ['Disk Space'],
    tone: 'text-muted',
  },
  {
    name: 'an idle snapshot, nothing has run yet',
    badge: 'system.database.migration.status.checking',
    prerequisites: {
      ...allowedPrerequisites,
      can_start_migration: false,
      all_passed: false,
      checks: [],
      status: 'idle',
      run_id: 0,
    },
    loading: false,
    error: null,
    helper: render('system.database.migration.prerequisites.running'),
    absent: [notMet],
    tone: 'text-muted',
  },
  {
    name: 'an idle snapshot and a failed request to start the run',
    badge: 'system.database.migration.status.notReady',
    prerequisites: {
      ...allowedPrerequisites,
      can_start_migration: false,
      all_passed: false,
      checks: [],
      status: 'idle',
      run_id: 0,
    },
    loading: false,
    error: 'Request timeout.',
    helper: render('system.database.migration.progress.prerequisitesUnavailable'),
    absent: [notMet],
    tone: 'text-[var(--text-error)]',
  },
  {
    name: 'request error',
    badge: 'system.database.migration.status.notReady',
    prerequisites: null,
    loading: false,
    error: 'Request timeout.',
    helper: render('system.database.migration.progress.prerequisitesUnavailable'),
    absent: [notMet],
    tone: 'text-[var(--text-error)]',
  },
  {
    name: 'request error with a stale blocked result',
    badge: 'system.database.migration.status.notReady',
    prerequisites: blockedPrerequisites,
    loading: false,
    error: 'Request timeout.',
    helper: render('system.database.migration.progress.prerequisitesUnavailable'),
    absent: ['Disk Space'],
    tone: 'text-[var(--text-error)]',
  },
  {
    name: 'a failed critical check',
    badge: 'system.database.migration.status.notReady',
    prerequisites: blockedPrerequisites,
    loading: false,
    error: null,
    helper: render('system.database.migration.progress.prerequisitesFailedList', {
      checks: 'Disk Space',
    }),
    absent: [notMet, 'Record Count'],
    tone: 'text-[var(--text-error)]',
  },
  {
    name: 'several blocking checks among passed and non-critical ones',
    badge: 'system.database.migration.status.notReady',
    prerequisites: severalBlocked,
    loading: false,
    error: null,
    helper: render('system.database.migration.progress.prerequisitesFailedList', {
      checks: 'Disk Space and Database Integrity',
    }),
    absent: [notMet, 'Record Count', 'Optional Tweak'],
    tone: 'text-[var(--text-error)]',
  },
  ...(['running', 'failed'] as const).map((status): StartStateCase => ({
    name: `a result that allows the start but whose run is ${status}`,
    badge: 'system.database.migration.status.notReady',
    prerequisites: { ...allowedPrerequisites, checks: [], status },
    loading: false,
    error: null,
    helper: notMet,
    absent: [],
    tone: 'text-[var(--text-error)]',
  })),
  {
    name: 'blocked with no failing check listed',
    badge: 'system.database.migration.status.notReady',
    prerequisites: blockedWithoutFailedCheck,
    loading: false,
    error: null,
    helper: notMet,
    absent: [],
    tone: 'text-[var(--text-error)]',
  },
];

describe('DatabaseMigrationProgress start button', () => {
  beforeEach(() => {
    vi.mocked(t).mockImplementation(render);
  });

  afterEach(() => {
    cleanup();
    vi.mocked(t).mockReset();
    if (originalT) vi.mocked(t).mockImplementation(originalT);
    vi.mocked(getLocale).mockReturnValue('en');
  });

  it.each(cases)('is blocked with a visible reason while $name', c => {
    let started = false;
    renderProgress({
      prerequisites: c.prerequisites,
      loading: c.loading,
      error: c.error,
      onStart: () => (started = true),
    });

    const start = screen.getByRole('button', { name: t(startKey) });
    expect(start).toHaveAttribute('aria-disabled', 'true');
    const helper = screen.getByText(c.helper);
    // The panel is the one live region for the checks; the helper only describes the button
    expect(helper).not.toHaveAttribute('role');
    expect(helper).toHaveClass(c.tone);
    expect(screen.getByText(c.badge)).toBeInTheDocument();
    expect(start.getAttribute('aria-describedby')).toBe(helper.id);
    for (const text of c.absent) {
      expect(screen.queryByText(text, { exact: false })).not.toBeInTheDocument();
    }
    start.click();
    expect(started).toBe(false);
  });

  it('starts when the checks allow it', () => {
    let started = false;
    renderProgress({
      prerequisites: allowedPrerequisites,
      loading: false,
      error: null,
      onStart: () => (started = true),
    });

    const start = screen.getByRole('button', { name: t(startKey) });
    expect(start).not.toHaveAttribute('aria-disabled');
    expect(screen.getByText('Idle')).toBeInTheDocument();
    // The helper stays mounted, empty and hidden, so the button's description id stays valid
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    const helper = document.querySelector('p[id^="migration-start-help"]');
    expect(helper).toHaveClass('sr-only');
    expect(helper?.textContent.trim()).toBe('');
    start.click();
    expect(started).toBe(true);
  });

  it('is blocked on a result that allows the start while a new run is being requested', () => {
    renderProgress({ prerequisites: allowedPrerequisites, loading: true, error: null });

    expect(screen.getByRole('button', { name: t(startKey) })).toHaveAttribute(
      'aria-disabled',
      'true'
    );
  });

  it('stays enabled on a result that allows the start after a failed refresh', () => {
    renderProgress({
      prerequisites: allowedPrerequisites,
      loading: false,
      error: 'Request timeout.',
    });

    expect(screen.getByRole('button', { name: t(startKey) })).not.toHaveAttribute('aria-disabled');
  });

  it('joins the blocking check names with the active locale', () => {
    vi.mocked(getLocale).mockReturnValue('fi');
    renderProgress({ prerequisites: severalBlocked, loading: false, error: null });

    expect(
      screen.getByText(
        render('system.database.migration.progress.prerequisitesFailedList', {
          checks: 'Disk Space ja Database Integrity',
        })
      )
    ).toBeInTheDocument();
  });
  describe('failed migration', () => {
    const failedStatus = {
      ...idleStatus,
      state: 'failed',
      can_start: false,
      can_retry_validation: true,
      can_cancel: true,
      error_message: 'Validation found mismatches',
      dirty_id_count: 3,
    } satisfies MigrationStatus;

    function renderFailed(status: MigrationStatus) {
      return renderTyped(DatabaseMigrationProgress, {
        props: {
          status,
          legacyStats: null,
          v2Stats: null,
          prerequisites: null,
          prerequisitesLoading: false,
          prerequisitesError: null,
          onStart: noop,
          onPause: noop,
          onResume: noop,
          onRetryValidation: noop,
          onCancel: noop,
        },
      });
    }

    it('shows the failure with its server message and the dirty record count in one alert', () => {
      renderFailed(failedStatus);

      const alert = screen.getByRole('alert');
      expect(alert).toHaveTextContent(t('system.database.migration.status.failed'));
      expect(alert).toHaveTextContent('Validation found mismatches');
      expect(alert).toHaveTextContent(
        t('system.database.migration.progress.dirtyIds', { count: 3 })
      );
      expect(alert).not.toHaveAttribute('aria-live');
    });

    it('falls back to a generic message and hides the dirty count when there is none', () => {
      renderFailed({ ...failedStatus, error_message: undefined, dirty_id_count: 0 });

      const alert = screen.getByRole('alert');
      expect(alert).toHaveTextContent(t('system.database.migration.progress.fallbackError'));
      expect(alert).not.toHaveTextContent(
        t('system.database.migration.progress.dirtyIds', { count: 0 })
      );
    });
  });
});
