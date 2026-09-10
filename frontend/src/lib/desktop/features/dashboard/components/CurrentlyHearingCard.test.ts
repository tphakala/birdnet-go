/**
 * Regression tests for CurrentlyHearingCard species-name localization.
 *
 * The "currently hearing" card is fed by the SSE `pending` event, which carries
 * both the server-locale common name (`species`) and the `scientificName`. The
 * card MUST display the name through localizeSpeciesName so it matches the rest
 * of the dashboard. These tests pin that wiring: the card no longer references
 * `detection.species` for display, it routes through localizeSpeciesName.
 *
 * Case 1 mocks the dictionary to a populated state (the visitor-localization
 * feature enabled) and asserts the localized name reaches the DOM, which would
 * fail against the old raw-`species` render. Case 2 leaves the dictionary empty
 * (the current gated-off default, PER_VISITOR_SPECIES_LOCALE_ENABLED=false) and
 * asserts the server common name still renders, so this fix is a no-op while the
 * feature stays off.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { flushSync } from 'svelte';
import { cleanup } from '@testing-library/svelte';
import { createComponentTestFactory } from '../../../../../test/render-helpers';
import type { PendingDetection } from '$lib/types/pending.types';

// Stub the visitor dictionary store. localizeScientific feeds localizeSpeciesName.
// Returns a Finnish label only for the mapped scientific name; undefined elsewhere
// exercises the server-common-name fallback path.
const FI = new Map<string, string>([['Turdus migratorius', 'Punarinta']]);
vi.mock('$lib/stores/speciesDictionary.svelte', () => ({
  localizeScientific: vi.fn((scientificName: string) => FI.get(scientificName)),
}));

// jsdom lacks the Web Animations API that svelte/transition's fade drives via
// element.animate() when chips mount/unmount on a rerender. Stub fade to a zero-duration,
// css-less transition so outros complete synchronously (removed chips actually leave the
// DOM) without touching element.animate.
vi.mock('svelte/transition', async importOriginal => {
  const actual = await importOriginal<typeof import('svelte/transition')>();
  return { ...actual, fade: () => ({ duration: 0 }) };
});

import CurrentlyHearingCard from './CurrentlyHearingCard.svelte';

function pending(overrides: Partial<PendingDetection> = {}): PendingDetection {
  return {
    species: 'American Robin',
    scientificName: 'Turdus migratorius',
    thumbnail: '',
    status: 'active',
    // Fixed timestamp for determinism; the card's elapsed-time text is not asserted.
    firstDetected: 1_700_000_000,
    source: 'mic-1',
    sourceID: 'mic-1',
    ...overrides,
  };
}

const card = createComponentTestFactory(CurrentlyHearingCard);

describe('CurrentlyHearingCard species-name localization', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
  });

  it('renders the visitor-locale name from the dictionary, not the raw server name', () => {
    const { getByText, queryByText } = card.render({ props: { detections: [pending()] } });

    expect(getByText('Punarinta')).toBeInTheDocument();
    // The raw server-locale common name must not leak through.
    expect(queryByText('American Robin')).toBeNull();
  });

  it('falls back to the server-provided common name when the dictionary has no entry', () => {
    const { getByText } = card.render({
      props: {
        detections: [
          pending({
            species: 'Eurasian Wren',
            scientificName: 'Troglodytes troglodytes',
            source: 'mic-2',
            sourceID: 'mic-2',
          }),
        ],
      },
    });

    expect(getByText('Eurasian Wren')).toBeInTheDocument();
  });

  // Regression for Sentry BIRDNET-GO-2HP: two concurrent pending detections of the same species on
  // the same source used to collide on a bare `${source}_${scientificName}` key,
  // throwing each_key_duplicate and (no error boundary) white-screening the dashboard.
  // displayDetections now dedupes by a stable source+species+firstDetected key.
  it('dedupes identical pending detections to one chip instead of crashing (Sentry BIRDNET-GO-2HP)', () => {
    // Same source, species, and firstDetected (pending() default) => one render key.
    const { getAllByText } = card.render({
      props: {
        detections: [
          pending({
            species: 'Eurasian Wren',
            scientificName: 'Troglodytes troglodytes',
            source: 'mic-9',
            sourceID: 'mic-9',
          }),
          pending({
            species: 'Eurasian Wren',
            scientificName: 'Troglodytes troglodytes',
            source: 'mic-9',
            sourceID: 'mic-9',
          }),
        ],
      },
    });
    expect(getAllByText('Eurasian Wren')).toHaveLength(1);
  });

  it('keeps two same-species detections that differ only in start time (Sentry BIRDNET-GO-2HP)', () => {
    // Distinct firstDetected => distinct render keys => both chips survive dedupe.
    const { getAllByText } = card.render({
      props: {
        detections: [
          pending({
            species: 'Eurasian Wren',
            scientificName: 'Troglodytes troglodytes',
            source: 'mic-9',
            sourceID: 'mic-9',
            firstDetected: 1_700_000_000,
          }),
          pending({
            species: 'Eurasian Wren',
            scientificName: 'Troglodytes troglodytes',
            source: 'mic-9',
            sourceID: 'mic-9',
            firstDetected: 1_700_000_005,
          }),
        ],
      },
    });
    expect(getAllByText('Eurasian Wren')).toHaveLength(2);
  });

  // Regression: the terminal-detection retention layer used to key by
  // source+species (detectionKey) while rendering keyed by source+species+firstDetected
  // (renderKey). Two concurrent same-source same-species detections then shared one
  // retention key, so when one completed and dropped from the incoming SSE snapshot while
  // the other was still active, the completed one was evicted instantly (its key was still
  // "incoming" via the active twin) instead of being held for TERMINAL_RETENTION_MS.
  it('retains a completed detection while a concurrent same-species detection is still active', async () => {
    const base = {
      species: 'Eurasian Wren',
      scientificName: 'Troglodytes troglodytes',
      source: 'mic-9',
      sourceID: 'mic-9',
    } as const;
    // A completed (approved), B still active; same source and species, distinct start times.
    const a = pending({ ...base, status: 'approved', firstDetected: 1_700_000_000 });
    const b = pending({ ...base, status: 'active', firstDetected: 1_700_000_005 });

    const { rerender, getAllByText } = card.render({ props: { detections: [a, b] } });
    // Let the retention $effect record A's terminal state before the snapshot changes.
    flushSync();

    // Backend stops sending A (it completed) but keeps sending the still-active B.
    await rerender({ detections: [b] });
    flushSync();

    // A must remain retained (held, not evicted) so both chips are still shown. With the
    // old detectionKey the active B masked A as "still incoming" and A vanished immediately.
    expect(getAllByText('Eurasian Wren')).toHaveLength(2);
  });
});
