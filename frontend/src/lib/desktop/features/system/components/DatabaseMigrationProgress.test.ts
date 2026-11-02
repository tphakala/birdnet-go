import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { cleanup, screen } from '@testing-library/svelte';
import { t } from '$lib/i18n';
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

interface StartStateCase {
  name: string;
  prerequisites: PrerequisitesResponse | null;
  loading: boolean;
  error: string | null;
  helper: string;
  absent: string[];
}

const notMet = render('system.database.migration.progress.prerequisitesNotMet');

const cases: StartStateCase[] = [
  {
    name: 'loading',
    prerequisites: null,
    loading: true,
    error: null,
    helper: render('system.database.migration.prerequisites.running'),
    absent: [notMet],
  },
  {
    name: 'request error',
    prerequisites: null,
    loading: false,
    error: 'Request timeout.',
    helper: render('system.database.migration.progress.prerequisitesUnavailable'),
    absent: [notMet],
  },
  {
    name: 'a failed critical check',
    prerequisites: blockedPrerequisites,
    loading: false,
    error: null,
    helper: render('system.database.migration.progress.prerequisitesFailedList', {
      checks: 'Disk Space',
    }),
    absent: [notMet, 'Record Count'],
  },
  {
    name: 'blocked with no failing check listed',
    prerequisites: blockedWithoutFailedCheck,
    loading: false,
    error: null,
    helper: notMet,
    absent: [],
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
  });

  it.each(cases)('is blocked with a visible reason while $name', c => {
    let started = false;
    renderTyped(DatabaseMigrationProgress, {
      props: {
        status: idleStatus,
        legacyStats: null,
        v2Stats: null,
        prerequisites: c.prerequisites,
        prerequisitesLoading: c.loading,
        prerequisitesError: c.error,
        onStart: () => (started = true),
        onPause: noop,
        onResume: noop,
        onRetryValidation: noop,
        onCancel: noop,
      },
    });

    const start = screen.getByRole('button', { name: t(startKey) });
    expect(start).toHaveAttribute('aria-disabled', 'true');
    const helper = screen.getByText(c.helper);
    expect(start.getAttribute('aria-describedby')).toBe(helper.id);
    for (const text of c.absent) {
      expect(screen.queryByText(text, { exact: false })).not.toBeInTheDocument();
    }
    start.click();
    expect(started).toBe(false);
  });

  it('starts when the checks allow it', () => {
    let started = false;
    renderTyped(DatabaseMigrationProgress, {
      props: {
        status: idleStatus,
        legacyStats: null,
        v2Stats: null,
        prerequisites: { ...blockedPrerequisites, can_start_migration: true, all_passed: true },
        prerequisitesLoading: false,
        prerequisitesError: null,
        onStart: () => (started = true),
        onPause: noop,
        onResume: noop,
        onRetryValidation: noop,
        onCancel: noop,
      },
    });

    const start = screen.getByRole('button', { name: t(startKey) });
    expect(start).not.toHaveAttribute('aria-disabled');
    start.click();
    expect(started).toBe(true);
  });
});
