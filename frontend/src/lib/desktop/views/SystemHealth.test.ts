import { describe, it, expect, vi, afterEach } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
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

describe('SystemHealth keyboard and run state', () => {
  afterEach(() => {
    cleanup();
    mocks.post.mockReset();
    localStorage.removeItem('health-eval-window');
  });

  /** Settles the mount run, then holds the next run pending so the page stays in the running state. */
  async function renderWithPendingRun() {
    mocks.post.mockResolvedValueOnce(report(TOTAL_CHECKS));
    render(SystemHealth);
    await screen.findByText('health.metricFooter.allPassing');
    let finish: (value: ReturnType<typeof report>) => void = () => {};
    mocks.post.mockReturnValueOnce(
      new Promise(resolve => {
        finish = resolve;
      })
    );
    return { finish };
  }

  const windowRadios = () => screen.getAllByRole('radio');

  /** The checked window radio and the one after it. */
  function checkedAndNextRadio(): [HTMLElement, HTMLElement] {
    const radios = windowRadios();
    const checkedIndex = radios.findIndex(r => r.getAttribute('aria-checked') === 'true');
    const checked = radios.at(checkedIndex);
    const next = radios.at(checkedIndex + 1);
    if (checkedIndex < 0 || !checked || !next) throw new Error('no checked radio with a successor');
    return [checked, next];
  }

  it('makes the checked window the only Tab stop of the group', async () => {
    mocks.post.mockResolvedValue(report(TOTAL_CHECKS));
    render(SystemHealth);
    await screen.findByText('health.metricFooter.allPassing');

    const stops = windowRadios().filter(r => r.getAttribute('tabindex') === '0');
    expect(stops).toHaveLength(1);
    expect(stops[0]).toHaveAttribute('aria-checked', 'true');
  });

  it('selects and focuses the next window on ArrowRight', async () => {
    mocks.post.mockResolvedValue(report(TOTAL_CHECKS));
    render(SystemHealth);
    await screen.findByText('health.metricFooter.allPassing');

    const [first, second] = checkedAndNextRadio();
    first.focus();
    await fireEvent.keyDown(first, { key: 'ArrowRight' });

    await waitFor(() => expect(second).toHaveAttribute('aria-checked', 'true'));
    expect(document.activeElement).toBe(second);
    expect(second).toHaveAttribute('tabindex', '0');
    expect(first).toHaveAttribute('tabindex', '-1');
  });

  it('keeps the radios and Refresh focusable and explains the wait while a run is active', async () => {
    const { finish } = await renderWithPendingRun();
    const refresh = screen.getByRole('button', { name: 'health.refresh' });
    refresh.focus();
    await fireEvent.click(refresh);

    await waitFor(() => expect(refresh).toHaveAttribute('aria-disabled', 'true'));
    const describedBy = refresh.getAttribute('aria-describedby') ?? '';
    const running = document.getElementById(describedBy);
    expect(running).toHaveTextContent('health.running');
    expect(refresh).not.toBeDisabled();
    expect(document.activeElement).toBe(refresh);
    for (const radio of windowRadios()) {
      expect(radio).not.toBeDisabled();
      expect(radio).toHaveAttribute('aria-disabled', 'true');
      expect(radio.closest('[role="radiogroup"]')).toHaveAttribute('aria-describedby', describedBy);
    }

    finish(report(TOTAL_CHECKS));
    await waitFor(() => expect(refresh).not.toHaveAttribute('aria-disabled'));
  });

  it('ignores a window click and a second Refresh click while a run is active', async () => {
    const { finish } = await renderWithPendingRun();
    const refresh = screen.getByRole('button', { name: 'health.refresh' });
    await fireEvent.click(refresh);
    await waitFor(() => expect(refresh).toHaveAttribute('aria-disabled', 'true'));
    const callsDuringRun = mocks.post.mock.calls.length;

    const [first, second] = checkedAndNextRadio();
    await fireEvent.click(second);
    await fireEvent.click(refresh);

    expect(first).toHaveAttribute('aria-checked', 'true');
    expect(second).toHaveAttribute('aria-checked', 'false');
    expect(localStorage.getItem('health-eval-window')).toBeNull();
    expect(mocks.post).toHaveBeenCalledTimes(callsDuringRun);

    finish(report(TOTAL_CHECKS));
  });
});
