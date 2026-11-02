import { describe, it, expect, afterEach } from 'vitest';
import { cleanup, fireEvent, screen, within } from '@testing-library/svelte';
import { t } from '$lib/i18n';
import { renderTyped } from '../../../../../test/render-helpers';
import DatabasePrerequisites from './DatabasePrerequisites.svelte';
import type { PrerequisiteCheck, PrerequisiteCheckStatus } from '$lib/types/migration';

const CRITICAL_BADGE_KEY = 'system.database.migration.prerequisites.critical';
const RUNNING_KEY = 'system.database.migration.prerequisites.running';
const FETCH_FAILED_KEY = 'system.database.migration.prerequisites.errors.fetchFailed';
const NOT_MET_KEY = 'system.database.migration.progress.prerequisitesNotMet';

function check(id: string, status: PrerequisiteCheckStatus): PrerequisiteCheck {
  return {
    id,
    name: `Check ${id}`,
    description: '',
    status,
    message: `message ${id}`,
    severity: 'critical',
  };
}

function rowOf(id: string): HTMLElement {
  const row = screen.getByText(`Check ${id}:`).closest<HTMLElement>('div.flex');
  if (!row) throw new Error(`row for ${id} not found`);
  return row;
}

describe('DatabasePrerequisites', () => {
  afterEach(() => cleanup());

  it('shows every non-passing check without expanding', async () => {
    const { container } = renderTyped(DatabasePrerequisites, {
      props: {
        prerequisites: {
          all_passed: false,
          can_start_migration: false,
          critical_failures: 2,
          warnings: 1,
          checked_at: '2026-10-28T12:00:00Z',
          checks: [
            check('passed_check', 'passed'),
            check('warning_check', 'warning'),
            check('skipped_check', 'skipped'),
            check('error_check', 'error'),
            check('failed_check', 'failed'),
          ],
        },
        onRefresh: async () => {},
      },
    });

    // No click: failed, error and warning rows are there with name, message and status.
    // The mocked t() returns the key, so statusLabel falls back to the title-cased status
    for (const [id, status] of [
      ['failed_check', 'Failed'],
      ['error_check', 'Error'],
      ['warning_check', 'Warning'],
    ] as const) {
      const row = within(rowOf(id));
      expect(row.getByText(`message ${id}`)).toBeInTheDocument();
      expect(row.getByText(status)).toBeInTheDocument();
    }
    expect(within(rowOf('failed_check')).getByText(CRITICAL_BADGE_KEY)).toBeInTheDocument();
    expect(within(rowOf('error_check')).getByText(CRITICAL_BADGE_KEY)).toBeInTheDocument();
    expect(within(rowOf('warning_check')).queryByText(CRITICAL_BADGE_KEY)).not.toBeInTheDocument();

    // Failed and error come before the warning
    const order = Array.from(container.querySelectorAll('span.font-medium')).map(
      e => e.textContent
    );
    expect(order.indexOf('Check warning_check:')).toBeGreaterThan(
      order.indexOf('Check error_check:')
    );
    expect(order.indexOf('Check warning_check:')).toBeGreaterThan(
      order.indexOf('Check failed_check:')
    );

    // Passed and skipped stay behind the toggle
    expect(screen.queryByText('Check passed_check:')).not.toBeInTheDocument();
    expect(screen.queryByText('Check skipped_check:')).not.toBeInTheDocument();

    await fireEvent.click(
      screen.getByRole('button', { name: t('system.database.migration.prerequisites.showDetails') })
    );

    expect(within(rowOf('passed_check')).queryByText(CRITICAL_BADGE_KEY)).not.toBeInTheDocument();
    expect(within(rowOf('skipped_check')).queryByText(CRITICAL_BADGE_KEY)).not.toBeInTheDocument();
    // Non-passing rows are not duplicated by the expanded list
    expect(screen.getAllByText('Check failed_check:')).toHaveLength(1);
  });

  it('has no details toggle when every check is non-passing', () => {
    renderTyped(DatabasePrerequisites, {
      props: {
        prerequisites: {
          all_passed: false,
          can_start_migration: false,
          critical_failures: 1,
          warnings: 0,
          checked_at: '2026-10-28T12:00:00Z',
          checks: [check('failed_check', 'failed')],
        },
        onRefresh: async () => {},
      },
    });

    expect(screen.getByText('Check failed_check:')).toBeInTheDocument();
    expect(
      screen.queryByRole('button', {
        name: t('system.database.migration.prerequisites.showDetails'),
      })
    ).not.toBeInTheDocument();
  });

  it.each([
    {
      name: 'loading',
      props: { isLoading: true },
      expected: RUNNING_KEY,
      role: 'status',
    },
    {
      name: 'a request error',
      props: { error: 'Request timeout.' },
      expected: 'Request timeout.',
      role: 'alert',
    },
    {
      name: 'neither loading nor an error',
      props: {},
      expected: FETCH_FAILED_KEY,
      role: 'alert',
    },
  ])('never renders an empty body without data while $name', c => {
    const { container } = renderTyped(DatabasePrerequisites, {
      props: {
        prerequisites: null,
        onRefresh: async () => {},
        ...c.props,
      },
    });

    expect(screen.getByRole(c.role)).toHaveTextContent(c.expected);
    expect(container.textContent).not.toContain(NOT_MET_KEY);
    expect(
      container.querySelector('#prerequisites-details, [aria-controls="prerequisites-details"]')
    ).toBeNull();
  });

  it('retries from the error alert', async () => {
    let refreshed = 0;
    renderTyped(DatabasePrerequisites, {
      props: {
        prerequisites: null,
        error: 'Request timeout.',
        onRefresh: async () => {
          refreshed++;
        },
      },
    });

    await fireEvent.click(
      within(screen.getByRole('alert')).getByRole('button', { name: t('common.retry') })
    );

    expect(refreshed).toBe(1);
  });
});
