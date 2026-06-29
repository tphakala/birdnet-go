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
import { cleanup } from '@testing-library/svelte';
import { get } from 'svelte/store';
import { createComponentTestFactory } from '../../../../../test/render-helpers';
import type { PendingDetection } from '$lib/types/pending.types';
import { settingsStore, settingsActions } from '$lib/stores/settings';

// Stub the visitor dictionary store. localizeScientific feeds localizeSpeciesName.
// Returns a Finnish label only for the mapped scientific name; undefined elsewhere
// exercises the server-common-name fallback path.
const FI = new Map<string, string>([['Turdus migratorius', 'Punarinta']]);
vi.mock('$lib/stores/speciesDictionary.svelte', () => ({
  localizeScientific: vi.fn((scientificName: string) => FI.get(scientificName)),
}));

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
});

/**
 * Clicking a currently-heard species opens the species guide. The card is only
 * made interactive when the guide feature is enabled, so users without the guide
 * see no change (and never get an empty modal).
 */
describe('CurrentlyHearingCard guide click affordance', () => {
  function enableGuide(): void {
    const dashboard = get(settingsStore).formData.realtime?.dashboard;
    if (!dashboard) throw new Error('default dashboard settings missing');
    settingsActions.updateSection('realtime', {
      dashboard: {
        ...dashboard,
        speciesGuide: {
          enabled: true,
          enableWikipedia: false,
          showNotes: true,
          showEnrichments: true,
          showSimilarSpecies: true,
        },
      },
    });
  }

  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
    settingsActions.resetAllSettings();
  });

  it('renders each species as a button when the guide is enabled', () => {
    enableGuide();
    const { getByRole } = card.render({ props: { detections: [pending()] } });
    // The card is interactive: a button whose accessible name uses the
    // parameterized viewGuide key (the test i18n mock returns the bare key) and
    // whose visible content is the localized species name.
    const button = getByRole('button', { name: 'analytics.species.viewGuide' });
    expect(button).toHaveTextContent('Punarinta');
  });

  it('is not interactive when the guide is disabled (default)', () => {
    const { queryByRole } = card.render({ props: { detections: [pending()] } });
    // No species-guide button is rendered when the feature is off.
    expect(queryByRole('button', { name: 'analytics.species.viewGuide' })).toBeNull();
  });
});
