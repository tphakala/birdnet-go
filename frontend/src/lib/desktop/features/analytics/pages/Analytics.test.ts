import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';

// Replace the real registry (which mounts heavy D3 charts) with stub charts so
// the hub test focuses on tab/URL behavior and active-tab mounting.
vi.mock('../registry/charts', async () => {
  const StubChart = (await import('../components/__tests__/StubChart.svelte')).default;
  const make = (id: string, group: string) => ({
    id,
    group,
    titleKey: `title.${id}`,
    descKey: `desc.${id}`,
    emptyKey: `empty.${id}`,
    emptyHintKey: `emptyHint.${id}`,
    component: StubChart,
    fetch: vi.fn().mockResolvedValue([{ a: 1 }, { a: 2 }]),
    size: 'full' as const,
    supports: { species: group !== 'biodiversity', source: false },
  });
  const defs = [
    make('time-of-day-species', 'patterns'),
    make('daily-species-trend', 'trends'),
    make('species-diversity', 'biodiversity'),
  ];
  return {
    CHART_REGISTRY: defs,
    chartsForGroup: (g: string) => defs.filter(d => d.group === g),
    groupHasCharts: (g: string) => defs.some(d => d.group === g),
  };
});

// Stub the Overview panel: its data fetching / D3 are out of scope for the hub's
// tab and URL-state behavior. The hub renders it for the (default) Overview tab.
vi.mock('../components/AnalyticsOverview.svelte', async () => ({
  default: (await import('../components/__tests__/StubOverview.svelte')).default,
}));

import Analytics from './Analytics.svelte';

const PATH = '/ui/analytics';

// The shared setup mocks window.location with a plain (writable) object that
// does NOT reflect history.pushState. So we set location fields directly to
// simulate the address bar, and spy on history to assert what the hub writes.
function setLocation(search: string): void {
  window.location.pathname = PATH;
  window.location.search = search;
}

function mockFetch() {
  return vi.fn(async (url: string) => {
    if (typeof url === 'string' && url.includes('/analytics/species/summary')) {
      return {
        ok: true,
        status: 200,
        json: async () => [
          { scientific_name: 'Turdus merula', common_name: 'Common Blackbird', count: 120 },
          { scientific_name: 'Parus major', common_name: 'Great Tit', count: 80 },
        ],
      } as unknown as Response;
    }
    return { ok: true, status: 200, json: async () => ({}) } as unknown as Response;
  });
}

let pushSpy: ReturnType<typeof vi.spyOn>;
let replaceSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
  setLocation('');
  globalThis.fetch = mockFetch() as unknown as typeof fetch;
  pushSpy = vi.spyOn(window.history, 'pushState');
  replaceSpy = vi.spyOn(window.history, 'replaceState');
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  setLocation('');
});

const tab = (name: string) => screen.getByRole('tab', { name });
const pushedUrls = (): string[] => pushSpy.mock.calls.map((c: unknown[]) => String(c[2]));

describe('Analytics hub: tabs, URL state, active-tab mounting', () => {
  it('lands on the Overview tab by default and mounts the overview panel', async () => {
    render(Analytics);
    await tick();

    expect(tab('analytics.hub.tabs.overview')).toHaveAttribute('aria-selected', 'true');

    // Overview renders the panel; no chart tab is mounted.
    expect(screen.getByTestId('stub-overview')).toBeInTheDocument();
    expect(document.querySelector('#time-of-day-species')).not.toBeInTheDocument();
    expect(document.querySelector('#species-diversity')).not.toBeInTheDocument();
  });

  it('switches tabs, writes ?tab= to the URL, and swaps which content is mounted', async () => {
    render(Analytics);
    await tick();

    await fireEvent.click(tab('analytics.hub.tabs.trends'));
    await tick();

    expect(pushedUrls().some(u => u.includes('tab=trends'))).toBe(true);
    expect(document.querySelector('#daily-species-trend')).toBeInTheDocument();
    expect(screen.queryByTestId('stub-overview')).not.toBeInTheDocument();
  });

  it('restores the Overview tab on Back/Forward to the tab-less URL (popstate)', async () => {
    render(Analytics);
    await tick();

    await fireEvent.click(tab('analytics.hub.tabs.biodiversity'));
    await tick();
    expect(document.querySelector('#species-diversity')).toBeInTheDocument();

    // Simulate the browser Back button returning to the tab-less URL.
    setLocation('');
    window.dispatchEvent(new PopStateEvent('popstate'));
    await tick();

    expect(tab('analytics.hub.tabs.overview')).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByTestId('stub-overview')).toBeInTheDocument();
    expect(document.querySelector('#species-diversity')).not.toBeInTheDocument();
  });

  it('restores the active tab from the initial URL on load (reload)', async () => {
    setLocation('?tab=trends');
    render(Analytics);
    await tick();

    expect(tab('analytics.hub.tabs.trends')).toHaveAttribute('aria-selected', 'true');
    expect(document.querySelector('#daily-species-trend')).toBeInTheDocument();
  });

  it('shows a coming-soon placeholder for tabs without charts', async () => {
    render(Analytics);
    await tick();

    await fireEvent.click(tab('analytics.hub.tabs.quality'));
    await tick();

    await waitFor(() =>
      expect(screen.getByText('analytics.hub.comingSoon.description')).toBeInTheDocument()
    );
    expect(document.querySelector('#time-of-day-species')).not.toBeInTheDocument();
  });

  it('does not auto-select species on the Overview tab (keeps the URL clean)', async () => {
    render(Analytics);
    // Let the species summary resolve; the Overview tab must not write a species param.
    await waitFor(() => expect(globalThis.fetch).toHaveBeenCalled());
    await tick();
    expect(replaceSpy.mock.calls.some((c: unknown[]) => String(c[2]).includes('species='))).toBe(
      false
    );
  });

  it('auto-selects top species into the URL when a species-driven tab is opened', async () => {
    render(Analytics);
    await tick();

    await fireEvent.click(tab('analytics.hub.tabs.patterns'));

    // Auto-select writes species via replaceState once the summary resolves.
    await waitFor(() =>
      expect(replaceSpy.mock.calls.some((c: unknown[]) => String(c[2]).includes('species='))).toBe(
        true
      )
    );
  });
});
