import { describe, it, expect, vi, afterEach } from 'vitest';
import { cleanup, render, screen, waitFor } from '@testing-library/svelte';
import SystemHealth from './SystemHealth.svelte';

const TOTAL_CHECKS = 5;
const SKIPPED_LABEL_KEY = 'health.summary.skipped';
/** The dark theme's text-muted is nearly as bright as base content, so a zero count is dimmed by opacity instead. */
const ZERO_COUNT_CLASS = 'text-[var(--color-base-content)]/70';

const mocks = vi.hoisted(() => ({ post: vi.fn() }));

vi.mock('$lib/utils/api', () => ({ api: { post: mocks.post } }));

function report(skipped: number) {
  return {
    id: 'report-1',
    status: 'healthy',
    started_at: '2026-01-01T00:00:00Z',
    completed_at: '2026-01-01T00:00:01Z',
    duration_ms: 1000,
    total_checks: TOTAL_CHECKS,
    results: [],
    summary: {},
    count_by_status: { healthy: TOTAL_CHECKS - skipped, skipped },
  };
}

/** The numeral beside the Skipped label in the tile header, once the report has loaded. */
async function loadedSkippedNumeral(count: number): Promise<HTMLElement> {
  const label = await screen.findByText(SKIPPED_LABEL_KEY);
  const numeral = label.closest('.flex-wrap')?.querySelector<HTMLElement>('.font-mono');
  if (!numeral) throw new Error('Skipped numeral not found');
  await waitFor(() => expect(numeral).toHaveTextContent(String(count)));
  return numeral;
}

describe('SystemHealth Skipped tile numeral', () => {
  afterEach(() => {
    cleanup();
    mocks.post.mockReset();
  });

  it('dims a zero count with opacity, not with the muted token', async () => {
    mocks.post.mockResolvedValue(report(0));
    render(SystemHealth);

    const numeral = await loadedSkippedNumeral(0);
    expect(numeral).toHaveClass(ZERO_COUNT_CLASS);
    expect(numeral).not.toHaveClass('text-muted');
  });

  it('leaves a non-zero count at full strength', async () => {
    mocks.post.mockResolvedValue(report(2));
    render(SystemHealth);

    const numeral = await loadedSkippedNumeral(2);
    expect(numeral).not.toHaveClass(ZERO_COUNT_CLASS);
    expect(numeral).not.toHaveClass('text-muted');
  });
});
