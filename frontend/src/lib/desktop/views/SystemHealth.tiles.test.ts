import { describe, it, expect, vi, afterEach } from 'vitest';
import { cleanup, render, screen, waitFor } from '@testing-library/svelte';
import SystemHealth from './SystemHealth.svelte';

const TOTAL_CHECKS = 5;
const SKIPPED_LABEL_KEY = 'health.summary.skipped';
/** The dark theme's text-muted is nearly as bright as base content, so a zero count is dimmed by opacity instead. */
const ZERO_COUNT_CLASS = 'text-[var(--color-base-content)]/70';

const mocks = vi.hoisted(() => ({ post: vi.fn() }));

vi.mock('$lib/utils/api', () => ({ api: { post: mocks.post } }));

function report(counts: Record<string, number>) {
  return {
    id: 'report-1',
    status: 'healthy',
    started_at: '2026-01-01T00:00:00Z',
    completed_at: '2026-01-01T00:00:01Z',
    duration_ms: 1000,
    total_checks: TOTAL_CHECKS,
    results: [],
    summary: {},
    count_by_status: counts,
  };
}

/** The numeral beside this label in the tile header, once the report has loaded. */
async function loadedNumeral(labelKey: string, count: number): Promise<HTMLElement> {
  const label = await screen.findByText(labelKey);
  const numeral = label.closest('.flex-wrap')?.querySelector<HTMLElement>('.font-mono');
  if (!numeral) throw new Error(`Numeral beside ${labelKey} not found`);
  await waitFor(() => expect(numeral).toHaveTextContent(String(count)));
  return numeral;
}

describe('SystemHealth tile numerals', () => {
  afterEach(() => {
    cleanup();
    mocks.post.mockReset();
  });

  it('dims a zero Skipped count with opacity, not with the muted token', async () => {
    mocks.post.mockResolvedValue(report({ healthy: TOTAL_CHECKS, skipped: 0 }));
    render(SystemHealth);

    const numeral = await loadedNumeral(SKIPPED_LABEL_KEY, 0);
    expect(numeral).toHaveClass(ZERO_COUNT_CLASS);
    expect(numeral).not.toHaveClass('text-muted');
  });

  it('leaves a non-zero Skipped count at full strength', async () => {
    mocks.post.mockResolvedValue(report({ healthy: TOTAL_CHECKS - 2, skipped: 2 }));
    render(SystemHealth);

    const numeral = await loadedNumeral(SKIPPED_LABEL_KEY, 2);
    expect(numeral).not.toHaveClass(ZERO_COUNT_CLASS);
    expect(numeral).not.toHaveClass('text-muted');
  });

  it.each([
    ['health.summary.warnings', 'warning', 'text-[var(--text-warning)]'],
    ['health.summary.critical', 'critical', 'text-[var(--text-error)]'],
  ])(
    'colours the %s numeral with its text token and mutes a zero count',
    async (labelKey, status, token) => {
      mocks.post.mockResolvedValue(report({ healthy: TOTAL_CHECKS - 3, [status]: 3 }));
      const { unmount } = render(SystemHealth);
      const counted = await loadedNumeral(labelKey, 3);
      expect(counted).toHaveClass(token);
      expect(counted).not.toHaveClass('text-muted');
      unmount();

      mocks.post.mockResolvedValue(report({ healthy: TOTAL_CHECKS, [status]: 0 }));
      render(SystemHealth);
      const zero = await loadedNumeral(labelKey, 0);
      expect(zero).toHaveClass('text-muted');
      expect(zero).not.toHaveClass(token);
    }
  );
});
