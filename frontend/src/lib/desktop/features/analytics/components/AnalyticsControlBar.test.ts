import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte';

import AnalyticsControlBar from './AnalyticsControlBar.svelte';
import { parseAnalyticsParams } from '../registry/analyticsParams';
import { createSpeciesId } from '$lib/types/species';
import type { Species } from '$lib/types/species';
import type { AnalyticsParams } from '../registry/types';

afterEach(() => cleanup());

const NOW = new Date(2026, 5, 19, 12, 0, 0);

const species: Species[] = [
  {
    id: createSpeciesId('Turdus merula'),
    commonName: 'Common Blackbird',
    scientificName: 'Turdus merula',
    count: 120,
  },
  {
    id: createSpeciesId('Parus major'),
    commonName: 'Great Tit',
    scientificName: 'Parus major',
    count: 80,
  },
];

function makeParams(overrides: Partial<AnalyticsParams> = {}): AnalyticsParams {
  return {
    ...parseAnalyticsParams('', { defaultTab: 'patterns', now: NOW }),
    ...overrides,
  };
}

describe('AnalyticsControlBar', () => {
  it('renders the source filter as inert with an explanation', () => {
    const onParamsChange = vi.fn();
    render(AnalyticsControlBar, {
      props: { params: makeParams(), availableSpecies: species, onParamsChange },
    });

    expect(screen.getByText('analytics.hub.controls.source')).toBeInTheDocument();
    expect(screen.getByText('analytics.hub.controls.sourceComingSoon')).toBeInTheDocument();
  });

  it('explains when species filtering does not apply to the active tab', () => {
    const onParamsChange = vi.fn();
    render(AnalyticsControlBar, {
      props: {
        params: makeParams(),
        availableSpecies: species,
        speciesApplicable: false,
        onParamsChange,
      },
    });

    expect(
      screen.getAllByText('analytics.hub.controls.speciesNotApplicable').length
    ).toBeGreaterThan(0);
  });

  it('reports custom date edits through onParamsChange', async () => {
    const onParamsChange = vi.fn();
    render(AnalyticsControlBar, {
      props: {
        params: makeParams({ range: 'custom', start: '2026-01-01', end: '2026-02-01' }),
        availableSpecies: species,
        onParamsChange,
      },
    });

    const startInput = screen.getByLabelText('analytics.advanced.filters.startDate');
    await fireEvent.change(startInput, { target: { value: '2026-01-15' } });

    expect(onParamsChange).toHaveBeenCalledWith(
      expect.objectContaining({ range: 'custom', start: '2026-01-15', end: '2026-02-01' })
    );
  });

  it('reports a preset range change through onParamsChange', async () => {
    const onParamsChange = vi.fn();
    render(AnalyticsControlBar, {
      props: { params: makeParams({ range: 'week' }), availableSpecies: species, onParamsChange },
    });

    // Open the date-range dropdown (trigger shows the current value label).
    const trigger = screen.getByText('analytics.advanced.dateRangeOptions.week').closest('button');
    expect(trigger).not.toBeNull();
    await fireEvent.click(trigger as HTMLButtonElement);

    // Pick the "month" option.
    const monthOption = await screen.findByText('analytics.advanced.dateRangeOptions.month');
    await fireEvent.click(monthOption);

    expect(onParamsChange).toHaveBeenCalledWith(expect.objectContaining({ range: 'month' }));
  });
});
