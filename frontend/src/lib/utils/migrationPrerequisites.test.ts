import { describe, it, expect } from 'vitest';
import { t } from '$lib/i18n';
import { elapsedSeconds, failedRunMessage } from './migrationPrerequisites';
import type { PrerequisitesResponse } from '$lib/types/migration';

const snapshot = {
  all_passed: false,
  can_start_migration: false,
  checks: [],
  critical_failures: 0,
  warnings: 0,
  status: 'completed',
  run_id: 1,
} satisfies PrerequisitesResponse;

describe('elapsedSeconds', () => {
  it.each([
    { ms: undefined, want: 0 },
    { ms: 0, want: 0 },
    { ms: 999, want: 0 },
    { ms: 1999, want: 1 },
    { ms: 125_000, want: 125 },
  ])('turns $ms ms into $want whole seconds', ({ ms, want }) => {
    expect(elapsedSeconds(ms)).toBe(want);
  });
});

describe('failedRunMessage', () => {
  it.each([
    { name: 'no snapshot', data: null },
    {
      name: 'a running snapshot',
      data: { ...snapshot, status: 'running' } satisfies PrerequisitesResponse,
    },
    { name: 'a completed snapshot', data: snapshot },
  ])('is null for $name', ({ data }) => {
    expect(failedRunMessage(data)).toBeNull();
  });

  it('names the reason of a failed run', () => {
    const failed = {
      ...snapshot,
      status: 'failed',
      error: 'took too long',
    } satisfies PrerequisitesResponse;
    expect(failedRunMessage(failed)).toBe(
      t('system.database.migration.prerequisites.runFailed', { error: 'took too long' })
    );
  });

  it('still reports a failed run that carries no reason', () => {
    const failed = { ...snapshot, status: 'failed' } satisfies PrerequisitesResponse;
    expect(failedRunMessage(failed)).toBe(
      t('system.database.migration.prerequisites.runFailed', { error: '' })
    );
  });
});
