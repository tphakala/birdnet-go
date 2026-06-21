import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { screen, waitFor, fireEvent, cleanup } from '@testing-library/svelte';
import { get } from 'svelte/store';
import { createComponentTestFactory } from '../../../../../../test/render-helpers';
import SpeciesDetailModal from './SpeciesDetailModal.svelte';
import { settingsStore, settingsActions } from '$lib/stores/settings';

// SpeciesComparison (rendered inside the modal) loads via $lib/utils/api; stub it
// so it mounts cleanly. It is intentionally not mocked — its real close button
// drives the parent onclose, which is the behavior under test.
vi.mock('$lib/utils/api', () => ({
  api: {
    get: vi.fn((url: string) => {
      if (url.includes('/similar')) {
        return Promise.resolve({ scientific_name: '', genus: '', similar: [] });
      }
      return Promise.resolve({
        scientific_name: '',
        common_name: '',
        description: 'A bird.',
        quality: 'full',
        features: { notes: false, enrichments: false, similar_species: true },
        source: { provider: '', url: '', license: '', license_url: '' },
        partial: false,
        cached_at: '',
      });
    }),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(message: string, status: number) {
      super(message);
      this.status = status;
    }
  },
}));

const modalTest = createComponentTestFactory(SpeciesDetailModal);

const COMPARISON_CLOSE = '[data-testid="species-comparison-close"]';
const REOPEN_KEY = 'analytics.species.similar.show';

const species = {
  common_name: 'House Sparrow',
  scientific_name: 'Passer domesticus',
  count: 42,
  avg_confidence: 0.85,
  max_confidence: 0.95,
  first_heard: '2024-01-15T10:30:00',
  last_heard: '2024-01-20T14:45:00',
};

// enableGuide turns on the guide + similar-species panel (notes off) by spreading
// the existing dashboard and overriding only speciesGuide.
function enableGuide(): void {
  const dashboard = get(settingsStore).formData.realtime?.dashboard;
  if (!dashboard) {
    throw new Error('default dashboard settings missing');
  }
  settingsActions.updateSection('realtime', {
    dashboard: {
      ...dashboard,
      speciesGuide: {
        enabled: true,
        provider: 'wikipedia',
        fallbackPolicy: 'all',
        showNotes: false,
        showEnrichments: true,
        showSimilarSpecies: true,
      },
    },
  });
}

describe('SpeciesDetailModal species guide panel', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
    settingsActions.resetAllSettings();
  });

  // The modal had the same close-with-no-reopen dead end as the detail view; this
  // guards the reopen affordance there too.
  it('reopens the comparison after it is closed', async () => {
    enableGuide();
    const { container } = modalTest.render({ props: { isOpen: true, species } });

    const closeBtn = await waitFor(
      () => {
        const b = container.querySelector(COMPARISON_CLOSE);
        if (!b) throw new Error('comparison not mounted yet');
        return b as HTMLElement;
      },
      { timeout: 5000 }
    );

    await fireEvent.click(closeBtn);
    const reopenBtn = await screen.findByText(REOPEN_KEY, {}, { timeout: 5000 });
    expect(container.querySelector(COMPARISON_CLOSE)).toBeNull();

    await fireEvent.click(reopenBtn);
    await waitFor(
      () => {
        expect(container.querySelector(COMPARISON_CLOSE)).not.toBeNull();
      },
      { timeout: 5000 }
    );
  });

  // Regression guard: the modal title already shows the species name, so the
  // embedded guide panel must NOT repeat it — it shows the generic guide heading
  // instead. Previously both showed the species name (two identical titles).
  it('labels the guide panel with the guide heading, not a duplicate species name', async () => {
    enableGuide();
    const { container } = modalTest.render({ props: { isOpen: true, species } });

    await waitFor(
      () => {
        if (!container.querySelector(COMPARISON_CLOSE)) throw new Error('not mounted yet');
      },
      { timeout: 5000 }
    );

    // The panel header now renders the guide-title key (t() returns keys in tests).
    expect(screen.getByText('analytics.species.guide.title')).toBeInTheDocument();
  });

  // Regression guard: reusing one modal instance for a different species must
  // refetch the guide. Without keying on the species, the onMount-only child
  // would keep showing the previous species' guide.
  it('refetches the guide when the modal is reused for a different species', async () => {
    enableGuide();
    const { container, rerender } = modalTest.render({ props: { isOpen: true, species } });

    await waitFor(
      () => {
        if (!container.querySelector(COMPARISON_CLOSE)) throw new Error('not mounted yet');
      },
      { timeout: 5000 }
    );

    const { api } = await import('$lib/utils/api');
    expect(vi.mocked(api.get)).toHaveBeenCalledWith(expect.stringContaining('Passer%20domesticus'));

    // Reuse the same modal instance for a different species (no close/reopen).
    const other = {
      ...species,
      scientific_name: 'Turdus merula',
      common_name: 'Common Blackbird',
    };
    await rerender({ isOpen: true, species: other });

    await waitFor(
      () => {
        expect(vi.mocked(api.get)).toHaveBeenCalledWith(expect.stringContaining('Turdus%20merula'));
      },
      { timeout: 5000 }
    );
  });
});
