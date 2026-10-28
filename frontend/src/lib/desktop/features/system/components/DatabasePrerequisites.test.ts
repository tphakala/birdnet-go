import { describe, it, expect, afterEach } from 'vitest';
import { cleanup, fireEvent } from '@testing-library/svelte';
import { renderTyped } from '../../../../../test/render-helpers';
import DatabasePrerequisites from './DatabasePrerequisites.svelte';
import type { PrerequisiteCheck, PrerequisiteCheckStatus } from '$lib/types/migration';

const CRITICAL_BADGE_KEY = 'system.database.migration.prerequisites.critical';

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

describe('DatabasePrerequisites critical badge', () => {
  afterEach(() => cleanup());

  it('labels only failed and error critical checks as Critical', async () => {
    const { container } = renderTyped(DatabasePrerequisites, {
      props: {
        prerequisites: {
          all_passed: false,
          can_start_migration: false,
          critical_failures: 2,
          warnings: 1,
          checked_at: '2026-10-28T12:00:00Z',
          checks: [
            check('failed_check', 'failed'),
            check('error_check', 'error'),
            check('warning_check', 'warning'),
            check('skipped_check', 'skipped'),
            check('passed_check', 'passed'),
          ],
        },
        onRefresh: async () => {},
      },
    });

    const toggle = container.querySelector('[aria-controls="prerequisites-details"]');
    if (!toggle) throw new Error('details toggle not found');
    await fireEvent.click(toggle);

    const hasBadge = (id: string): boolean => {
      const row = Array.from(container.querySelectorAll('#prerequisites-details > div')).find(r =>
        r.textContent.includes(`Check ${id}`)
      );
      if (!row) throw new Error(`row for ${id} not found`);
      return row.textContent.includes(CRITICAL_BADGE_KEY);
    };

    expect(hasBadge('failed_check')).toBe(true);
    expect(hasBadge('error_check')).toBe(true);
    expect(hasBadge('warning_check')).toBe(false);
    expect(hasBadge('skipped_check')).toBe(false);
    expect(hasBadge('passed_check')).toBe(false);
  });
});
