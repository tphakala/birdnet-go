import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';

// Mock the navigation store so applyParams writes are observable and do not touch jsdom history.
const navState = {
  currentPath: '/ui/analytics/activity',
  last: null as null | { url: string; mode: string },
};
vi.mock('$lib/stores/navigation.svelte', () => ({
  navigation: {
    get currentPath() {
      return navState.currentPath;
    },
    navigate: (url: string) => {
      navState.last = { url, mode: 'push' };
    },
    redirect: (url: string) => {
      navState.last = { url, mode: 'replace' };
    },
  },
}));

import { createAnalyticsControls } from './analyticsControls.svelte';

// JSDOM does not propagate window.history.replaceState() to window.location, so
// we set location via Object.defineProperty where the test needs a specific search string.
function setLocation(search: string): void {
  Object.defineProperty(window, 'location', {
    value: { ...window.location, search, pathname: '/ui/analytics/activity' },
    writable: true,
    configurable: true,
  });
}

describe('analyticsControls', () => {
  beforeEach(() => {
    navState.currentPath = '/ui/analytics/activity';
    navState.last = null;
    setLocation('');
  });
  afterEach(() => vi.restoreAllMocks());

  // Fix L: split into two assertions so each tested path is isolated.

  it('constructor seeds params directly from window.location.search (no syncFromUrl needed)', () => {
    setLocation('?range=week&species=A,B');
    const c = createAnalyticsControls();
    // Do NOT call syncFromUrl - the constructor reads window.location.search directly.
    expect(c.params.range).toBe('week');
    expect(c.params.species).toEqual(['A', 'B']);
  });

  it('syncFromUrl updates params when the location changes after construction', () => {
    setLocation('?range=quarter');
    const c = createAnalyticsControls();
    expect(c.params.range).toBe('quarter'); // seeded by constructor from initial location

    // Change location after construction and call syncFromUrl to pick up the new value.
    setLocation('?range=year&species=X');
    c.syncFromUrl();
    expect(c.params.range).toBe('year');
    expect(c.params.species).toEqual(['X']);
  });

  it('applyParams writes the current pathname + serialized query (push)', () => {
    const c = createAnalyticsControls();
    c.applyParams({ range: 'week' }, 'push');
    expect(c.params.range).toBe('week');
    expect(navState.last).toEqual({ url: '/ui/analytics/activity?range=week', mode: 'push' });
  });

  it('applyParams replace mode uses navigation.redirect', () => {
    const c = createAnalyticsControls();
    c.applyParams({ species: ['A'] }, 'replace');
    expect(navState.last?.mode).toBe('replace');
    expect(navState.last?.url).toContain('species=A');
  });

  it('queryString reflects current params', () => {
    const c = createAnalyticsControls();
    c.applyParams({ range: 'year' }, 'push');
    expect(c.queryString).toContain('range=year');
  });

  describe('init() popstate listener', () => {
    let cleanups: Array<() => void>;

    beforeEach(() => {
      cleanups = [];
    });

    afterEach(() => {
      cleanups.forEach(fn => fn());
    });

    it('popstate event after init() calls syncFromUrl and updates params', () => {
      setLocation('?range=day');
      const c = createAnalyticsControls();
      cleanups.push(c.init());
      setLocation('?range=month');
      window.dispatchEvent(new Event('popstate'));
      expect(c.params.range).toBe('month');
    });

    it('init() is idempotent: a second call does not add a second popstate listener', () => {
      const addSpy = vi.spyOn(window, 'addEventListener');
      const c = createAnalyticsControls();
      cleanups.push(c.init()); // first call: adds listener
      cleanups.push(c.init()); // second call: no-op, returns empty cleanup
      const popstateCalls = addSpy.mock.calls.filter(call => call[0] === 'popstate');
      expect(popstateCalls).toHaveLength(1);
    });

    it('cleanup returned by init() removes the popstate listener', () => {
      setLocation('?range=day');
      const c = createAnalyticsControls();
      const cleanup = c.init();
      setLocation('?range=week');
      window.dispatchEvent(new Event('popstate'));
      expect(c.params.range).toBe('week');
      cleanup();
      setLocation('?range=year');
      window.dispatchEvent(new Event('popstate'));
      expect(c.params.range).toBe('week'); // unchanged after listener removed
    });
  });

  // Fix K: maybeAutoSelectSpecies coverage (previously in Analytics.test.ts, now deleted).
  // fetchAvailableSpecies calls maybeAutoSelectSpecies after a successful fetch.
  describe('maybeAutoSelectSpecies via ensureSpecies()', () => {
    const speciesData = [
      { scientific_name: 'Turdus merula', common_name: 'Common Blackbird', count: 10 },
      { scientific_name: 'Parus major', common_name: 'Great Tit', count: 7 },
      { scientific_name: 'Erithacus rubecula', common_name: 'European Robin', count: 3 },
    ];

    function stubFetch(): void {
      vi.stubGlobal(
        'fetch',
        vi.fn().mockResolvedValue({
          ok: true,
          json: (): Promise<typeof speciesData> => Promise.resolve(speciesData),
        })
      );
    }

    afterEach(() => {
      vi.unstubAllGlobals();
    });

    it('auto-selects the top species with a replace write when no species are in params', async () => {
      setLocation('');
      const c = createAnalyticsControls();
      stubFetch();
      c.ensureSpecies();
      // fetchAvailableSpecies awaits fetch() then .json() - drain both ticks via setTimeout so
      // all queued microtasks complete before the assertion.
      await new Promise<void>(resolve => setTimeout(resolve, 0));
      expect(navState.last?.mode).toBe('replace');
      expect(navState.last?.url).toContain('species=');
    });

    it('does not auto-select when species are already present in params', async () => {
      // Seed params.species via the URL so maybeAutoSelectSpecies returns early.
      setLocation('?species=A');
      const c = createAnalyticsControls();
      stubFetch();
      c.ensureSpecies();
      await new Promise<void>(resolve => setTimeout(resolve, 0));
      // No replace write should have occurred - navState.last stays null.
      expect(navState.last).toBeNull();
    });
  });
});
