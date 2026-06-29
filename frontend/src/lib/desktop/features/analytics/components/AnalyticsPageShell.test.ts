import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import AnalyticsPageShell from './AnalyticsPageShell.svelte';

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
    applyParams: vi.fn(),
    ensureSpecies: vi.fn(),
    ensureSources: vi.fn(),
    init: vi.fn(() => () => {}),
  },
}));

describe('AnalyticsPageShell', () => {
  it('renders the title key and the slotted body', () => {
    const body = createRawSnippet(() => ({ render: () => `<p data-testid="body">hi</p>` }));
    render(AnalyticsPageShell, {
      props: { titleKey: 'analytics.hub.tabs.trends', group: 'trends', children: body },
    });
    expect(screen.getByText('analytics.hub.tabs.trends')).toBeInTheDocument();
    expect(screen.getByTestId('body')).toBeInTheDocument();
  });
});
