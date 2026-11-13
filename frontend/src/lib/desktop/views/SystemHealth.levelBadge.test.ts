import { describe, it, expect, vi, afterEach } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import SystemHealth from './SystemHealth.svelte';

const mocks = vi.hoisted(() => ({ post: vi.fn() }));

vi.mock('$lib/utils/api', () => ({ api: { post: mocks.post } }));

const CHECK_NAME = 'log_errors';

function report(level: string) {
  return {
    id: 'report-1',
    status: 'warning',
    started_at: '2026-01-01T00:00:00Z',
    completed_at: '2026-01-01T00:00:01Z',
    duration_ms: 1000,
    total_checks: 1,
    results: [
      {
        name: CHECK_NAME,
        category: 'logs',
        status: 'warning',
        message: 'errors found',
        duration_ms: 1,
        details: { top_errors: [{ component: 'audio', message: 'boom', count: 3, level }] },
      },
    ],
    summary: {},
    count_by_status: { warning: 1 },
  };
}

/** Opens the check row and returns the level badge of its first error group. */
async function openLevelBadge(level: string): Promise<HTMLElement> {
  mocks.post.mockResolvedValue(report(level));
  render(SystemHealth);
  fireEvent.click(await screen.findByRole('button', { name: /Log Errors/ }));
  return screen.findByText(level);
}

describe('SystemHealth log level badge', () => {
  afterEach(() => {
    cleanup();
    mocks.post.mockReset();
  });

  it.each([
    ['error', 'var(--text-error)', 'var(--color-error)'],
    ['warn', 'var(--text-warning)', 'var(--color-warning)'],
  ])(
    'colours a %s badge with its text token and tints it with the fill',
    async (level, text, fill) => {
      const badge = await openLevelBadge(level);

      expect(badge.style.color).toBe(text);
      expect(badge.style.background).toContain(fill);
    }
  );
});
