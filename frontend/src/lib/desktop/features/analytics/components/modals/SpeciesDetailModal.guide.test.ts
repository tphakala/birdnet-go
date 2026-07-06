import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { screen, waitFor, fireEvent, cleanup } from '@testing-library/svelte';
import { get } from 'svelte/store';
import { createComponentTestFactory } from '../../../../../../test/render-helpers';
import SpeciesDetailModal from './SpeciesDetailModal.svelte';
import { settingsStore, settingsActions } from '$lib/stores/settings';

// SpeciesComparison (rendered inside the modal) loads via $lib/utils/api; stub it
// so it mounts cleanly. It is intentionally not mocked — its real header toggle
// collapses the guide body in place, which is the behavior under test.
vi.mock('$lib/utils/api', () => ({
  api: {
    get: vi.fn((url: string) => {
      if (url.includes('/similar')) {
        return Promise.resolve({ scientific_name: '', genus: '', similar: [] });
      }
      if (url.includes('/taxonomy')) {
        return Promise.resolve({
          taxonomy: {
            kingdom: 'Animalia',
            phylum: 'Chordata',
            class: 'Aves',
            order: 'Passeriformes',
            family: 'Passeridae',
            genus: 'Passer',
            species: 'Passer domesticus',
          },
          subspecies: [
            { scientific_name: 'Passer domesticus domesticus', common_name: 'House Sparrow' },
          ],
        });
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

const COMPARISON_TOGGLE = '[data-testid="species-comparison-toggle"]';

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
// the existing dashboard and overriding only speciesGuide. `overrides` lets a test
// flip an individual show flag (e.g. showTaxonomy) without restating the rest.
function enableGuide(overrides: Record<string, unknown> = {}): void {
  const dashboard = get(settingsStore).formData.realtime?.dashboard;
  if (!dashboard) {
    throw new Error('default dashboard settings missing');
  }
  settingsActions.updateSection('realtime', {
    dashboard: {
      ...dashboard,
      speciesGuide: {
        enabled: true,
        enableWikipedia: false,
        showNotes: false,
        showEnrichments: true,
        showSimilarSpecies: true,
        showTaxonomy: true,
        ...overrides,
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

  // The guide panel collapses in place (header stays, body hides) like the sections
  // inside it, instead of closing to a separate reopen button. Guards that the
  // header toggle is never a dead end.
  it('collapses and re-expands the guide panel in place', async () => {
    enableGuide();
    const { container } = modalTest.render({ props: { isOpen: true, species } });

    const toggle = await waitFor(
      () => {
        const b = container.querySelector(COMPARISON_TOGGLE);
        if (!b) throw new Error('comparison not mounted yet');
        return b as HTMLElement;
      },
      { timeout: 5000 }
    );

    // Body visible once the guide loads.
    expect(await screen.findByText('A bird.', {}, { timeout: 5000 })).toBeInTheDocument();

    // Collapse in place: the header/toggle stays, the body hides.
    await fireEvent.click(toggle);
    await waitFor(() => {
      expect(screen.queryByText('A bird.')).toBeNull();
    });
    expect(container.querySelector(COMPARISON_TOGGLE)).not.toBeNull();

    // Expand again: the body returns.
    await fireEvent.click(toggle);
    expect(await screen.findByText('A bird.', {}, { timeout: 5000 })).toBeInTheDocument();
  });

  // The description is the primary thing users open the guide for, so the panel
  // re-expands on every open — even if it was collapsed when the modal last closed.
  it('re-expands the guide panel when reopened for the same species', async () => {
    enableGuide();
    const { container, rerender } = modalTest.render({ props: { isOpen: true, species } });

    const toggle = await waitFor(
      () => {
        const b = container.querySelector(COMPARISON_TOGGLE);
        if (!b) throw new Error('comparison not mounted yet');
        return b as HTMLElement;
      },
      { timeout: 5000 }
    );

    expect(await screen.findByText('A bird.', {}, { timeout: 5000 })).toBeInTheDocument();

    // Collapse the panel, then close the modal with it still collapsed.
    await fireEvent.click(toggle);
    await waitFor(() => {
      expect(screen.queryByText('A bird.')).toBeNull();
    });
    await rerender({ isOpen: false, species });

    // Reopen the same species: the panel re-expands so the description shows again.
    await rerender({ isOpen: true, species });
    expect(await screen.findByText('A bird.', {}, { timeout: 5000 })).toBeInTheDocument();
  });

  // Companion guard to the re-expand test: only the OUTER panel resets on reopen.
  // The inner section toggles must persist (the modal keeps SpeciesComparison mounted
  // for the same species). If a refactor ever remounts it on open, this catches the
  // silent regression. The description is always visible now, so this exercises the
  // similar-species section, which is still a collapsible inner section.
  it('keeps inner section state across a reopen while re-expanding the panel', async () => {
    enableGuide();
    const { container, rerender } = modalTest.render({ props: { isOpen: true, species } });

    await waitFor(
      () => {
        if (!container.querySelector(COMPARISON_TOGGLE)) throw new Error('comparison not mounted');
      },
      { timeout: 5000 }
    );

    // The description stays visible (no longer collapsible)...
    expect(await screen.findByText('A bird.', {}, { timeout: 5000 })).toBeInTheDocument();

    // ...while the similar-species section is open by default; collapse just that
    // section (not the whole panel), which hides its body but leaves the header.
    const similarHeader = await screen.findByText(
      'analytics.species.similar.title',
      {},
      { timeout: 5000 }
    );
    expect(screen.getByText('analytics.species.similar.empty')).toBeInTheDocument();
    await fireEvent.click(similarHeader);
    await waitFor(() => {
      expect(screen.queryByText('analytics.species.similar.empty')).toBeNull();
    });

    // Close and reopen the same species.
    await rerender({ isOpen: false, species });
    await rerender({ isOpen: true, species });

    // The outer panel re-expanded (its toggle shows, the description is visible)...
    expect(container.querySelector(COMPARISON_TOGGLE)).not.toBeNull();
    expect(screen.getByText('A bird.')).toBeInTheDocument();
    // ...but the inner similar section kept its collapsed state, so its body stays hidden.
    expect(screen.queryByText('analytics.species.similar.empty')).toBeNull();
  });

  // Regression guard: the modal title already shows the species name, so the
  // embedded guide panel must NOT repeat it — it shows the generic guide heading
  // instead. Previously both showed the species name (two identical titles).
  it('labels the guide panel with the guide heading, not a duplicate species name', async () => {
    enableGuide();
    const { container } = modalTest.render({ props: { isOpen: true, species } });

    await waitFor(
      () => {
        if (!container.querySelector(COMPARISON_TOGGLE)) throw new Error('not mounted yet');
      },
      { timeout: 5000 }
    );

    // The panel header renders the "Species guide" heading key, not the species
    // name (t() returns keys in tests).
    expect(screen.getByText('analytics.species.similar.speciesGuide')).toBeInTheDocument();
  });

  // Regression guard: reusing one modal instance for a different species must
  // refetch the guide. Without keying on the species, the onMount-only child
  // would keep showing the previous species' guide.
  it('refetches the guide when the modal is reused for a different species', async () => {
    enableGuide();
    const { container, rerender } = modalTest.render({ props: { isOpen: true, species } });

    await waitFor(
      () => {
        if (!container.querySelector(COMPARISON_TOGGLE)) throw new Error('not mounted yet');
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

  // Taxonomy is factual metadata sourced from the public /species/taxonomy endpoint;
  // it renders in the guide modal when the guide is on and showTaxonomy is not opted out.
  it('renders the taxonomy hierarchy and subspecies when showTaxonomy is on', async () => {
    enableGuide();
    modalTest.render({ props: { isOpen: true, species } });

    const { api } = await import('$lib/utils/api');
    await waitFor(
      () => {
        expect(vi.mocked(api.get)).toHaveBeenCalledWith(
          expect.stringContaining('/species/taxonomy')
        );
      },
      { timeout: 5000 }
    );

    // t() returns keys in tests, so the heading/labels render as their key strings.
    expect(
      await screen.findByText('species.taxonomy.hierarchy', {}, { timeout: 5000 })
    ).toBeInTheDocument();
    expect(screen.getByText('Aves')).toBeInTheDocument();
    expect(screen.getByText('Passeridae')).toBeInTheDocument();
    expect(screen.getByText('species.taxonomy.subspecies')).toBeInTheDocument();
    expect(screen.getByText('Passer domesticus domesticus')).toBeInTheDocument();
  });

  it('hides taxonomy and does not fetch it when showTaxonomy is opted out', async () => {
    enableGuide({ showTaxonomy: false });
    const { container } = modalTest.render({ props: { isOpen: true, species } });

    // The guide comparison still mounts, so wait on it to know the modal settled.
    await waitFor(
      () => {
        if (!container.querySelector(COMPARISON_TOGGLE)) throw new Error('not mounted yet');
      },
      { timeout: 5000 }
    );

    const { api } = await import('$lib/utils/api');
    expect(vi.mocked(api.get)).not.toHaveBeenCalledWith(
      expect.stringContaining('/species/taxonomy')
    );
    expect(screen.queryByText('species.taxonomy.hierarchy')).toBeNull();
  });
});
