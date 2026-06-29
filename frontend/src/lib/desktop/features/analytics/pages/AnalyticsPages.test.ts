import { describe, it, expect, vi } from 'vitest';
import { render } from '@testing-library/svelte';

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

describe('analytics route pages render without throwing', () => {
  it.each([
    ['summary', SummaryPage],
    ['activity', ActivityPage],
    ['trends', TrendsPage],
    ['biodiversity', BiodiversityPage],
    ['review', ReviewPage],
  ])('%s page mounts', (_name, Comp) => {
    const { container } = render(Comp as never);
    expect(container.querySelector('#analytics-page-title')).toBeTruthy();
  });
});
