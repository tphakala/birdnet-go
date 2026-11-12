import { describe, it, expect, vi, afterEach } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import { t } from '$lib/i18n';
import SystemHealth from './SystemHealth.svelte';

const TOTAL_CHECKS = 5;

const mocks = vi.hoisted(() => ({ post: vi.fn() }));

vi.mock('$lib/utils/api', () => ({ api: { post: mocks.post } }));

function report(healthy: number) {
  return {
    id: 'report-1',
    status: healthy === TOTAL_CHECKS ? 'healthy' : 'warning',
    started_at: '2026-01-01T00:00:00Z',
    completed_at: '2026-01-01T00:00:01Z',
    duration_ms: 1000,
    total_checks: TOTAL_CHECKS,
    results: [],
    summary: {},
    count_by_status: { healthy, warning: TOTAL_CHECKS - healthy },
  };
}

describe('SystemHealth Healthy tile footer', () => {
  afterEach(() => {
    cleanup();
    mocks.post.mockReset();
    vi.mocked(t).mockClear();
  });

  it('translates the healthy count against the total', async () => {
    mocks.post.mockResolvedValue(report(3));
    render(SystemHealth);

    await screen.findByText('health.metricFooter.healthyOfTotal');
    expect(t).toHaveBeenCalledWith('health.metricFooter.healthyOfTotal', {
      healthy: 3,
      total: TOTAL_CHECKS,
    });
    expect(screen.queryByText('3 of 5')).not.toBeInTheDocument();
  });

  it('says all passing when every check is healthy', async () => {
    mocks.post.mockResolvedValue(report(TOTAL_CHECKS));
    render(SystemHealth);

    await screen.findByText('health.metricFooter.allPassing');
    expect(t).not.toHaveBeenCalledWith('health.metricFooter.healthyOfTotal', expect.anything());
  });
});
