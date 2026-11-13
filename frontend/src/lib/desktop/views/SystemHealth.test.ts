import { describe, it, expect, vi, afterEach } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { t } from '$lib/i18n';
import { runAxeTest } from '$lib/utils/axe-utils';
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
    for (const radio of windowRadios().filter(r => r.getAttribute('aria-checked') !== 'true')) {
      expect(radio).toHaveAttribute('tabindex', '-1');
    }
  });

  it('draws the focus ring inside each window radio so the clipped group cannot hide it', async () => {
    mocks.post.mockResolvedValue(report(TOTAL_CHECKS));
    render(SystemHealth);
    await screen.findByText('health.metricFooter.allPassing');

    // The group keeps overflow-hidden for its rounded corners; an outside ring would be cut off.
    // The ring has two bands, an outer outline in the text colour and an inner inset band in the
    // primary content colour, so one of them stands out on any neighbouring fill. It stays at full
    // strength while a run fades the control. Rendered contrast and clipping are browser-only checks.
    for (const radio of windowRadios()) {
      expect(radio).toHaveClass('focus-visible:outline-2');
      expect(radio).toHaveClass('focus-visible:-outline-offset-2');
      expect(radio).not.toHaveClass('focus-visible:outline-offset-2');
      expect(radio).toHaveClass('focus-visible:outline-[var(--color-base-content)]');
      expect(radio).toHaveClass(
        'focus-visible:shadow-[inset_0_0_0_4px_var(--color-primary-content)]'
      );
      expect(radio).toHaveClass('aria-disabled:focus-visible:opacity-100');
    }
  });

  it('keeps the Refresh focus ring at full strength while a run fades the button', async () => {
    mocks.post.mockResolvedValue(report(TOTAL_CHECKS));
    render(SystemHealth);
    await screen.findByText('health.metricFooter.allPassing');

    const refresh = screen.getByRole('button', { name: 'health.refresh' });
    expect(refresh).toHaveClass('focus-visible:outline-2');
    expect(refresh).toHaveClass('aria-disabled:focus-visible:opacity-100');
  });

  it('applies hover backgrounds only while no run is active', async () => {
    mocks.post.mockResolvedValue(report(TOTAL_CHECKS));
    render(SystemHealth);
    await screen.findByText('health.metricFooter.allPassing');

    // A disabled-looking control must not light up on hover. Rendered hover is a browser-only check.
    const refresh = screen.getByRole('button', { name: 'health.refresh' });
    expect(refresh).toHaveClass('not-aria-disabled:hover:bg-[var(--color-base-200)]');
    expect(refresh).not.toHaveClass('hover:bg-[var(--color-base-200)]');
    const [, next] = checkedAndNextRadio();
    expect(next).toHaveClass('not-aria-disabled:hover:bg-[var(--color-base-300)]');
    expect(next).not.toHaveClass('hover:bg-[var(--color-base-300)]');
  });

  it('keeps the Refresh button and the window radios out from under a dimmed ancestor', async () => {
    mocks.post.mockResolvedValue(report(TOTAL_CHECKS));
    render(SystemHealth);
    await screen.findByText('health.metricFooter.allPassing');

    // An ancestor opacity multiplies into the focus ring and cannot be lifted by the child.
    // The computed opacity chain and the ring contrast are browser-only checks.
    const controls = [screen.getByRole('button', { name: 'health.refresh' }), ...windowRadios()];
    for (const control of controls) {
      for (let node = control.parentElement; node; node = node.parentElement) {
        expect(node.className).not.toMatch(/(^|\s)opacity-\d+(\s|$)/);
      }
    }
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
      expect(radio).toHaveAttribute('aria-describedby', describedBy);
    }
    expect(screen.getByRole('radiogroup')).not.toHaveAttribute('aria-describedby');

    finish(report(TOTAL_CHECKS));
    await waitFor(() => expect(refresh).not.toHaveAttribute('aria-disabled'));
    expect(refresh).not.toHaveAttribute('aria-describedby');
    for (const radio of windowRadios()) {
      expect(radio).not.toHaveAttribute('aria-disabled');
      expect(radio).not.toHaveAttribute('aria-describedby');
    }
    expect(screen.getByRole('radiogroup')).not.toHaveAttribute('aria-describedby');
  });

  it('does not move selection or focus on ArrowRight while a run is active', async () => {
    const { finish } = await renderWithPendingRun();
    await fireEvent.click(screen.getByRole('button', { name: 'health.refresh' }));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'health.refresh' })).toHaveAttribute(
        'aria-disabled',
        'true'
      )
    );

    const [first, second] = checkedAndNextRadio();
    first.focus();
    await fireEvent.keyDown(first, { key: 'ArrowRight' });

    expect(first).toHaveAttribute('aria-checked', 'true');
    expect(second).toHaveAttribute('aria-checked', 'false');
    expect(document.activeElement).not.toBe(second);

    finish(report(TOTAL_CHECKS));
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

  it('has no Accessibility violations', async () => {
    mocks.post.mockResolvedValue(report(TOTAL_CHECKS));
    const { container } = render(SystemHealth);
    await screen.findByText('health.metricFooter.allPassing');

    expect((await runAxeTest(container)).violations).toEqual([]);
  });
});
