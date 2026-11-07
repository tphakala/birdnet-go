import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, screen } from '@testing-library/svelte';
import { createComponentTestFactory } from '../../../test/render-helpers';
import type { DailySpeciesSummary } from '$lib/types/detection.types';
import PhoneSpeciesTable from './PhoneSpeciesTable.svelte';

function summary(overrides: Partial<DailySpeciesSummary> = {}): DailySpeciesSummary {
  return {
    scientific_name: 'Turdus merula',
    common_name: 'Blackbird',
    species_code: 'eurbla',
    count: 3,
    hourly_counts: Array.from({ length: 24 }, (_, h) => (h === 6 ? 3 : 0)),
    high_confidence: true,
    max_confidence: 0.9,
    first_heard: '06:00:00',
    latest_heard: '06:40:00',
    thumbnail_url: '',
    taxonomic_class: 'Aves',
    ...overrides,
  };
}

const rows = [
  summary(),
  summary({ scientific_name: 'Corvus corax', common_name: 'Raven', count: 8 }),
  summary({
    scientific_name: 'Pipistrellus pipistrellus',
    common_name: 'Pipistrelle',
    count: 1,
    taxonomic_class: 'Chiroptera',
  }),
];

const table = createComponentTestFactory(PhoneSpeciesTable);

function renderTable(data: DailySpeciesSummary[]) {
  return table.render({
    data,
    selectedDate: '2026-03-01',
    showThumbnails: false,
    sunriseHour: 6,
    sunsetHour: 20,
    maxHour: 23,
    speciesUrl: () => '/ui/detections',
    noveltyOf: () => null,
  });
}

const speciesRows = () =>
  screen.getAllByRole('button').filter(b => b.dataset.species !== undefined);

describe('PhoneSpeciesTable', () => {
  afterEach(() => {
    cleanup();
    localStorage.clear();
  });

  it('lists every species, busiest first, and re-sorts by name', async () => {
    renderTable(rows);
    expect(speciesRows().map(b => b.dataset.species)).toEqual([
      'Corvus corax',
      'Turdus merula',
      'Pipistrellus pipistrellus',
    ]);

    // Sort buttons in column order: name, confidence, count, last heard.
    await fireEvent.click(
      screen.getAllByRole('button', { name: 'dashboard.dailySummary.phone.sortBy' })[0]
    );
    expect(speciesRows().map(b => b.dataset.species)).toEqual([
      'Turdus merula',
      'Pipistrellus pipistrellus',
      'Corvus corax',
    ]);
  });

  it('filters by taxon group when the day holds more than one', async () => {
    renderTable(rows);
    await fireEvent.click(
      screen.getByRole('button', { name: 'dashboard.dailySummary.phone.taxon.bat' })
    );
    expect(speciesRows().map(b => b.dataset.species)).toEqual(['Pipistrellus pipistrellus']);
  });

  it('falls back to all species when the filtered group leaves the data', async () => {
    const { rerender } = renderTable(rows);
    await fireEvent.click(
      screen.getByRole('button', { name: 'dashboard.dailySummary.phone.taxon.bat' })
    );
    await rerender({ data: rows.slice(0, 2), selectedDate: '2026-03-02' });
    expect(speciesRows()).toHaveLength(2);
  });

  it('hides the taxon filter when every row is in one group', () => {
    renderTable(rows.slice(0, 2));
    expect(
      screen.queryByRole('button', { name: 'dashboard.dailySummary.phone.taxon.all' })
    ).toBeNull();
  });

  it('expands a row into its detail card and collapses it again', async () => {
    renderTable(rows);
    await fireEvent.click(speciesRows()[0]);
    expect(screen.getByRole('group', { name: 'Raven' })).toBeTruthy();

    await fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(screen.queryByRole('group', { name: 'Raven' })).toBeNull();
  });

  it('shows a live row whose class is not known yet under every filter', async () => {
    const live = summary({ scientific_name: 'Sitta europaea', common_name: 'Nuthatch' });
    delete live.taxonomic_class;
    renderTable([...rows, live]);
    await fireEvent.click(
      screen.getByRole('button', { name: 'dashboard.dailySummary.phone.taxon.bat' })
    );
    // Busiest first: the live row has 3 detections, the bat 1.
    expect(speciesRows().map(b => b.dataset.species)).toEqual([
      'Sitta europaea',
      'Pipistrellus pipistrellus',
    ]);
  });

  it('renders a species repeated in the payload once', () => {
    renderTable([...rows, summary()]);
    expect(speciesRows()).toHaveLength(3);
  });
});
