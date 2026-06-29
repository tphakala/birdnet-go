import { beforeEach, describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/svelte';

vi.mock('$lib/i18n', () => ({ t: (k: string) => k }));
vi.mock('../registry/analyticsControls.svelte', () => ({
  analyticsControls: {
    params: {
      range: 'month',
      start: '',
      end: '',
      species: [],
      source: '',
      startDate: new Date(),
      endDate: new Date(),
    },
    availableSpecies: [],
    loadingSpecies: false,
    availableSources: [],
    loadingSources: false,
    speciesNames: new Map(),
    applyParams: vi.fn(),
    ensureSpecies: vi.fn(),
    ensureSources: vi.fn(),
    syncFromUrl: vi.fn(),
    init: vi.fn(() => () => {}),
  },
}));

// Stub body children to avoid dragging in chart/fetch stack under jsdom.
// Uses the same real Svelte stub components as Analytics.test.ts.
vi.mock('../components/ChartGrid.svelte', async () => ({
  default: (await import('../components/__tests__/StubChart.svelte')).default,
}));
vi.mock('../components/AnalyticsOverview.svelte', async () => ({
  default: (await import('../components/__tests__/StubOverview.svelte')).default,
}));

import SummaryPage from './SummaryPage.svelte';
import ActivityPage from './ActivityPage.svelte';
import TrendsPage from './TrendsPage.svelte';
import BiodiversityPage from './BiodiversityPage.svelte';
import ReviewPage from './ReviewPage.svelte';
import NocturnalPage from './NocturnalPage.svelte';
import WeatherPage from './WeatherPage.svelte';
import SoundscapePage from './SoundscapePage.svelte';

describe('analytics route pages render without throwing', () => {
  // Reset the shared analyticsControls mock so vi.fn() call history does not leak
  // between parametrized cases.
  beforeEach(() => vi.clearAllMocks());

  it.each([
    ['summary', SummaryPage, 'analytics.hub.tabs.summary'],
    ['activity', ActivityPage, 'analytics.hub.tabs.patterns'],
    ['trends', TrendsPage, 'analytics.hub.tabs.trends'],
    ['biodiversity', BiodiversityPage, 'analytics.hub.tabs.biodiversity'],
    ['review', ReviewPage, 'analytics.hub.tabs.quality'],
    ['nocturnal', NocturnalPage, 'analytics.hub.tabs.nocturnal'],
    ['weather', WeatherPage, 'analytics.hub.tabs.weather'],
    ['soundscape', SoundscapePage, 'analytics.hub.tabs.soundscape'],
  ])('%s page mounts with the correct title', (_name, Comp, expectedTitleKey) => {
    render(Comp as never);
    // The title is in the section's aria-label (the h1 was removed to avoid
    // a duplicate title with the global Header). The t() mock echoes keys verbatim.
    // Use getByRole('region') to verify both the ARIA role and the label together.
    expect(screen.getByRole('region', { name: expectedTitleKey })).toBeInTheDocument();
  });
});
