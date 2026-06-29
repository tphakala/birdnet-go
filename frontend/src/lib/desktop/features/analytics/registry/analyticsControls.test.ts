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

  it('seeds params from the current URL search', () => {
    setLocation('?range=week&species=A,B');
    const c = createAnalyticsControls();
    c.syncFromUrl();
    expect(c.params.range).toBe('week');
    expect(c.params.species).toEqual(['A', 'B']);
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
});
