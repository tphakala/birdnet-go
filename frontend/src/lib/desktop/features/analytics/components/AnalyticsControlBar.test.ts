import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte';

import AnalyticsControlBar from './AnalyticsControlBar.svelte';
import { parseAnalyticsParams } from '../registry/analyticsParams';
import { createSpeciesId } from '$lib/types/species';
import type { Species } from '$lib/types/species';
import type { AnalyticsParams, AudioSourceOption } from '../registry/types';

afterEach(() => cleanup());

const sources: AudioSourceOption[] = [
  { id: '7', name: 'camera-7', count: 42 },
  { id: '3', name: 'audio-source-3', count: 9 },
];

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
    ...parseAnalyticsParams('', { now: NOW }),
    ...overrides,
  };
}

describe('AnalyticsControlBar', () => {
  it('disables the source filter with a reason when no chart on the tab uses source', () => {
    const onParamsChange = vi.fn();
    render(AnalyticsControlBar, {
      // sourceApplicable defaults to false: the control is disabled with the not-applicable reason.
      props: {
        params: makeParams(),
        availableSpecies: species,
        availableSources: sources,
        onParamsChange,
      },
    });

    expect(screen.getByText('analytics.hub.controls.source')).toBeInTheDocument();
    // The reason is surfaced as visible help text (wired to the control via aria-describedby).
    expect(screen.getByText('analytics.hub.controls.sourceNotApplicable')).toBeInTheDocument();
  });

  it('disables the source filter with a distinct reason when applicable but no sources exist', () => {
    const onParamsChange = vi.fn();
    render(AnalyticsControlBar, {
      props: {
        params: makeParams(),
        availableSpecies: species,
        availableSources: [],
        sourceApplicable: true,
        onParamsChange,
      },
    });

    expect(screen.getByText('analytics.hub.controls.sourceNone')).toBeInTheDocument();
  });

  it('disables the source filter with a loading reason while sources load', () => {
    const onParamsChange = vi.fn();
    render(AnalyticsControlBar, {
      props: {
        params: makeParams(),
        availableSpecies: species,
        availableSources: [],
        sourceApplicable: true,
        loadingSources: true,
        onParamsChange,
      },
    });

    expect(screen.getByText('analytics.hub.controls.sourceLoading')).toBeInTheDocument();
  });

  it('enables the source filter and reports a selection when a chart on the tab uses source', async () => {
    const onParamsChange = vi.fn();
    render(AnalyticsControlBar, {
      props: {
        params: makeParams(),
        availableSpecies: species,
        availableSources: sources,
        sourceApplicable: true,
        onParamsChange,
      },
    });

    // Enabled: no disabled-reason help text is present.
    expect(
      screen.queryByText('analytics.hub.controls.sourceNotApplicable')
    ).not.toBeInTheDocument();

    // Open the source dropdown (its trigger shows the "All sources" label) and pick a source.
    const trigger = screen.getByText('analytics.hub.controls.sourceAll').closest('button');
    expect(trigger).not.toBeNull();
    await fireEvent.click(trigger as HTMLButtonElement);

    const option = await screen.findByText('camera-7');
    await fireEvent.click(option);

    expect(onParamsChange).toHaveBeenCalledWith({ source: '7' });
  });

  it('keeps the source filter enabled to clear a stale selection even when no sources are returned', () => {
    const onParamsChange = vi.fn();
    render(AnalyticsControlBar, {
      props: {
        // A source is set (e.g. from a URL/bookmark) but the live list came back empty.
        params: makeParams({ source: '7' }),
        availableSpecies: species,
        availableSources: [],
        sourceApplicable: true,
        onParamsChange,
      },
    });

    // Enabled (no disabled reason) so the user can reset back to "All sources" and recover.
    expect(screen.queryByText('analytics.hub.controls.sourceNone')).not.toBeInTheDocument();
    expect(
      screen.queryByText('analytics.hub.controls.sourceNotApplicable')
    ).not.toBeInTheDocument();
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

  it('expands and collapses the species selector via the toggle', async () => {
    const onParamsChange = vi.fn();
    render(AnalyticsControlBar, {
      props: { params: makeParams(), availableSpecies: species, onParamsChange },
    });

    const toggle = document.getElementById('analyticsSpeciesToggle') as HTMLButtonElement;
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    // Collapsed by default: the species panel is not in the DOM.
    expect(document.querySelector('#analyticsSpeciesPanel')).not.toBeInTheDocument();

    await fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(document.querySelector('#analyticsSpeciesPanel')).toBeInTheDocument();

    await fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(document.querySelector('#analyticsSpeciesPanel')).not.toBeInTheDocument();
  });

  it('disables the species toggle (collapsed, aria-expanded false) when species filtering does not apply', () => {
    const onParamsChange = vi.fn();
    render(AnalyticsControlBar, {
      props: {
        params: makeParams(),
        availableSpecies: species,
        speciesApplicable: false,
        onParamsChange,
      },
    });

    const toggle = document.getElementById('analyticsSpeciesToggle') as HTMLButtonElement;
    expect(toggle).toBeDisabled();
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(toggle).not.toHaveAttribute('aria-controls');
    expect(document.querySelector('#analyticsSpeciesPanel')).not.toBeInTheDocument();
  });
});
