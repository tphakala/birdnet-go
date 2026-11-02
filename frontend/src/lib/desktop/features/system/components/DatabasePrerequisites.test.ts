import { describe, it, expect, afterEach } from 'vitest';
import { cleanup, fireEvent, screen, within } from '@testing-library/svelte';
import { t } from '$lib/i18n';
import { renderTyped, createComponentTestFactory } from '../../../../../test/render-helpers';
import DatabasePrerequisites from './DatabasePrerequisites.svelte';
import type {
  PrerequisiteCheck,
  PrerequisiteCheckStatus,
  PrerequisitesResponse,
} from '$lib/types/migration';

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

function response(
  checks: PrerequisiteCheck[],
  criticalFailures: number,
  warnings: number
): PrerequisitesResponse {
  return {
    all_passed: false,
    can_start_migration: false,
    critical_failures: criticalFailures,
    warnings,
    checked_at: '2026-10-28T12:00:00Z',
    checks,
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
        prerequisites: response(
          [
            check('passed_check', 'passed'),
            check('warning_check', 'warning'),
            check('skipped_check', 'skipped'),
            check('error_check', 'error'),
            check('failed_check', 'failed'),
          ],
          2,
          1
        ),
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
        prerequisites: response([check('failed_check', 'failed')], 1, 0),
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
      alerts: 0,
    },
    {
      name: 'a request error',
      props: { error: 'Request timeout.' },
      expected: 'Request timeout.',
      role: 'alert',
      alerts: 1,
    },
    {
      name: 'neither loading nor an error',
      props: {},
      expected: FETCH_FAILED_KEY,
      role: 'alert',
      alerts: 1,
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
    // Exactly one of the loading row and the alert
    expect(screen.queryAllByRole('alert')).toHaveLength(c.alerts);
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

  it('labels Retry as busy while a retry with stale data is loading', () => {
    renderTyped(DatabasePrerequisites, {
      props: {
        prerequisites: response([check('failed_check', 'failed')], 1, 0),
        error: 'Request timeout.',
        isLoading: true,
        onRefresh: async () => {},
      },
    });

    const retry = within(screen.getByRole('alert')).getByRole('button');
    expect(retry).toHaveAttribute('aria-disabled', 'true');
    expect(retry).toHaveTextContent(t('common.retrying'));
  });

  it('keeps keyboard focus in the panel when Retry removes the alert', async () => {
    // Like the dashboard: the request starts loading at once and the data stays null
    const noData = createComponentTestFactory(DatabasePrerequisites, {
      prerequisites: null,
      error: 'Request timeout.',
    });
    const view = noData.render({
      props: {
        onRefresh: () => {
          void view.rerender({ isLoading: true });
          return new Promise<void>(() => {});
        },
      },
    });

    const retry = within(screen.getByRole('alert')).getByRole('button', {
      name: t('common.retry'),
    });
    retry.focus();
    await fireEvent.click(retry);

    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveFocus();
  });

  it('leaves focus on Retry when the alert stays mounted', async () => {
    renderTyped(DatabasePrerequisites, {
      props: {
        prerequisites: response([check('failed_check', 'failed')], 1, 0),
        error: 'Request timeout.',
        onRefresh: () => new Promise<void>(() => {}),
      },
    });

    const retry = within(screen.getByRole('alert')).getByRole('button', {
      name: t('common.retry'),
    });
    retry.focus();
    await fireEvent.click(retry);

    expect(retry).toHaveFocus();
  });
});
