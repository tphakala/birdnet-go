/**
 * Regression test for Sentry BIRDNET-GO-2HP: the dashboard must not white-screen on duplicate
 * {#each} keys. The daily-summary payload can carry duplicate scientific_name
 * rows, so keying visibleHighlights by a bare scientific_name throws
 * each_key_duplicate (Svelte 5) and, with no error boundary, takes the whole
 * dashboard down. highlights is now deduped by scientific_name and keyed by it, so
 * a duplicate row collapses to one tile instead of crashing or rendering twice.
 */

import { describe, it, expect, afterEach } from 'vitest';
import { cleanup } from '@testing-library/svelte';
import { createComponentTestFactory } from '../../../../../test/render-helpers';
import type { DailySpeciesSummary } from '$lib/types/detection.types';
import NewSpeciesHighlightsCard from './NewSpeciesHighlightsCard.svelte';

function summary(overrides: Partial<DailySpeciesSummary> = {}): DailySpeciesSummary {
  return {
    scientific_name: 'Turdus merula',
    common_name: 'Common Blackbird',
    species_code: 'eurbla',
    count: 3,
    hourly_counts: new Array(24).fill(0),
    high_confidence: true,
    max_confidence: 0.9,
    first_heard: '06:00:00',
    latest_heard: '07:00:00',
    thumbnail_url: '',
    // is_new_species makes resolveNoveltyCategory return 'lifetime' with no store
    // dependency, so the row reaches visibleHighlights regardless of tracking config.
    is_new_species: true,
    ...overrides,
  };
}

const card = createComponentTestFactory(NewSpeciesHighlightsCard);

describe('NewSpeciesHighlightsCard', () => {
  afterEach(() => {
    cleanup();
  });

  it('dedupes duplicate scientific_name rows to one tile instead of crashing (Sentry BIRDNET-GO-2HP)', () => {
    // Two rows with the same scientific_name both qualify as new species. A bare
    // scientific_name key would collide (each_key_duplicate); the highlights list now
    // dedupes by scientific_name, so it renders one tile, not two, and does not throw.
    const { getAllByText } = card.render({
      props: { data: [summary(), summary()], selectedDate: '2026-09-07', isToday: true },
    });

    expect(getAllByText('Common Blackbird')).toHaveLength(1);
  });
});
