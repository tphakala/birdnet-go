import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import SimilarSpeciesPanel from './SimilarSpeciesPanel.svelte';
import type { SimilarSpeciesEntry, SpeciesGuideData } from '$lib/types/species';

// Locally mock the api so we control per-species guide responses.
vi.mock('$lib/utils/api', () => ({
  api: { get: vi.fn() },
  ApiError: class ApiError extends Error {
    status: number;
    response?: Response;
    constructor(message: string, status: number, response?: Response) {
      super(message);
      this.status = status;
      this.response = response;
    }
  },
}));

import { api, ApiError } from '$lib/utils/api';

function makeGuide(overrides: Partial<SpeciesGuideData> = {}): SpeciesGuideData {
  return {
    scientific_name: 'Corvus corax',
    common_name: 'Common Raven',
    description: 'A large, heavy-billed corvid.\n\n## Voice\nA deep croaking gronk.',
    quality: 'full',
    features: { notes: true, enrichments: true, similar_species: true },
    source: { provider: 'wikipedia', url: '', license: '', license_url: '' },
    partial: false,
    cached_at: '2026-01-01T00:00:00Z',
    ...overrides,
  };
}

function entry(overrides: Partial<SimilarSpeciesEntry> = {}): SimilarSpeciesEntry {
  return {
    scientific_name: 'Corvus corax',
    common_name: 'Common Raven',
    relationship: 'same_genus',
    has_guide: true,
    ...overrides,
  };
}

describe('SimilarSpeciesPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('shows the empty state when there are no similar species', () => {
    render(SimilarSpeciesPanel, { props: { mainName: 'American Crow', similar: [] } });
    expect(screen.getByText('analytics.species.similar.empty')).toBeInTheDocument();
    expect(api.get).not.toHaveBeenCalled();
  });

  it('auto-selects the first species with a guide and surfaces its sections', async () => {
    vi.mocked(api.get).mockResolvedValue(
      makeGuide({
        description:
          'A large, heavy-billed corvid.\n\n## Voice\nA deep croaking gronk.\n\n## Distribution and habitat\nMountains and coasts.\n\n## Behaviour\nForms large roosts.',
      }) as never
    );

    render(SimilarSpeciesPanel, {
      props: { mainName: 'American Crow', similar: [entry()] },
    });

    // Appearance falls back to the article lead when there is no Description section.
    expect(
      await screen.findByText('A large, heavy-billed corvid.', {}, { timeout: 5000 })
    ).toBeInTheDocument();
    expect(screen.getByText('A deep croaking gronk.')).toBeInTheDocument();
    expect(screen.getByText('Mountains and coasts.')).toBeInTheDocument();
    // Behaviour is surfaced as the fourth comparison row.
    expect(screen.getByText('Forms large roosts.')).toBeInTheDocument();
    // The "vs main species" header is shown.
    expect(screen.getByText('analytics.species.similar.versus')).toBeInTheDocument();
  });

  it('clamps a long comparison row behind a Read more toggle and expands on click', async () => {
    const longVoice = 'Song phrase. '.repeat(40); // > 240 chars → clampable
    vi.mocked(api.get).mockResolvedValue(
      makeGuide({
        description: `Short appearance.\n\n## Voice\n${longVoice}`,
      }) as never
    );

    render(SimilarSpeciesPanel, { props: { mainName: 'American Crow', similar: [entry()] } });

    // Show more appears for the long voice row (reuses the shared common.ui key)...
    const toggle = await screen.findByRole(
      'button',
      { name: 'common.ui.showMore' },
      { timeout: 5000 }
    );
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    // ...but the short appearance row gets no toggle (only one toggle total).
    expect(screen.getAllByText('common.ui.showMore')).toHaveLength(1);

    // Expanding flips the label and aria-expanded; the full prose is present the
    // whole time (clamp is CSS-only, so nothing is truncated from the DOM).
    await fireEvent.click(toggle);
    expect(await screen.findByRole('button', { name: 'common.ui.showLess' })).toHaveAttribute(
      'aria-expanded',
      'true'
    );
    expect(screen.getByText(longVoice.trim(), { exact: false })).toBeInTheDocument();
  });

  it('shows resource links for a description-less species and keeps it clickable', async () => {
    render(SimilarSpeciesPanel, {
      props: {
        mainName: 'American Crow',
        similar: [
          entry({
            scientific_name: 'Columba albinucha',
            common_name: 'White-naped Pigeon',
            has_guide: false,
            external_links: [
              { name: 'Wikipedia', url: 'https://en.wikipedia.org/wiki/Columba_albinucha' },
              { name: 'eBird', url: 'https://ebird.org/species/whnpig1' },
            ],
          }),
        ],
      },
    });

    // Auto-selected: the explore-resources heading and the link pills render
    // instead of an empty comparison.
    expect(
      await screen.findByText('analytics.species.similar.exploreResources', {}, { timeout: 5000 })
    ).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /Wikipedia/ })).toHaveAttribute(
      'href',
      'https://en.wikipedia.org/wiki/Columba_albinucha'
    );
    expect(screen.getByRole('link', { name: /eBird/ })).toBeInTheDocument();

    // The row stays clickable (no longer disabled) and links need no guide fetch.
    const button = screen.getByRole('button', { name: /White-naped Pigeon/ });
    expect(button).not.toHaveAttribute('aria-disabled');
    expect(api.get).not.toHaveBeenCalled();
  });

  it('switches the diff card when another species is selected', async () => {
    vi.mocked(api.get).mockImplementation((url: string) => {
      if (url.includes('corax'))
        return Promise.resolve(makeGuide({ description: 'Raven appearance text.' }) as never);
      return Promise.resolve(
        makeGuide({
          scientific_name: 'Corvus ossifragus',
          common_name: 'Fish Crow',
          description: 'Fish Crow appearance text.',
        }) as never
      );
    });

    render(SimilarSpeciesPanel, {
      props: {
        mainName: 'American Crow',
        similar: [
          entry(),
          entry({ scientific_name: 'Corvus ossifragus', common_name: 'Fish Crow' }),
        ],
      },
    });

    expect(
      await screen.findByText('Raven appearance text.', {}, { timeout: 5000 })
    ).toBeInTheDocument();

    await fireEvent.click(screen.getByRole('button', { name: /Fish Crow/ }));

    expect(
      await screen.findByText('Fish Crow appearance text.', {}, { timeout: 5000 })
    ).toBeInTheDocument();
  });

  it('shows a soft message when the selected species has no guide (404)', async () => {
    vi.mocked(api.get).mockRejectedValue(
      new ApiError('not found', 404, new Response(null, { status: 404 }))
    );

    render(SimilarSpeciesPanel, {
      props: { mainName: 'American Crow', similar: [entry()] },
    });

    expect(
      await screen.findByText('analytics.species.similar.cardNoGuide', {}, { timeout: 5000 })
    ).toBeInTheDocument();
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('shows an error alert when the guide fetch fails', async () => {
    vi.mocked(api.get).mockRejectedValue(
      new ApiError('boom', 500, new Response(null, { status: 500 }))
    );

    render(SimilarSpeciesPanel, {
      props: { mainName: 'American Crow', similar: [entry()] },
    });

    expect(await screen.findByRole('alert', {}, { timeout: 5000 })).toHaveTextContent(
      'analytics.species.similar.cardError'
    );
  });

  it('does not refetch a species whose guide 404s when it is re-selected', async () => {
    vi.mocked(api.get).mockRejectedValue(
      new ApiError('not found', 404, new Response(null, { status: 404 }))
    );

    render(SimilarSpeciesPanel, {
      props: { mainName: 'American Crow', similar: [entry()] },
    });

    // Auto-selected on mount → one fetch → 404 soft message.
    expect(
      await screen.findByText('analytics.species.similar.cardNoGuide', {}, { timeout: 5000 })
    ).toBeInTheDocument();
    expect(api.get).toHaveBeenCalledTimes(1);

    // Re-selecting the same (already-resolved 404) species must not re-hit the
    // rate-limited guide endpoint.
    await fireEvent.click(screen.getByRole('button', { name: /Common Raven/ }));
    expect(api.get).toHaveBeenCalledTimes(1);
  });

  it('re-auto-selects when the similar list changes to a new focal species', async () => {
    vi.mocked(api.get).mockImplementation((url: string) => {
      if (url.includes('corax'))
        return Promise.resolve(makeGuide({ description: 'Raven appearance text.' }) as never);
      return Promise.resolve(
        makeGuide({
          scientific_name: 'Sturnus vulgaris',
          common_name: 'European Starling',
          description: 'Starling appearance text.',
        }) as never
      );
    });

    const { rerender } = render(SimilarSpeciesPanel, {
      props: { mainName: 'American Crow', similar: [entry()] },
    });

    expect(
      await screen.findByText('Raven appearance text.', {}, { timeout: 5000 })
    ).toBeInTheDocument();

    // New focal species → an entirely different similar list. The previously
    // selected 'Corvus corax' is gone, so the panel must auto-select the new
    // first guide rather than leaving a stale/blank selection.
    await rerender({
      mainName: 'House Sparrow',
      similar: [entry({ scientific_name: 'Sturnus vulgaris', common_name: 'European Starling' })],
    });

    expect(
      await screen.findByText('Starling appearance text.', {}, { timeout: 5000 })
    ).toBeInTheDocument();
  });

  it('prunes cached guides when the focal species changes (bounded cache)', async () => {
    const calls: string[] = [];
    vi.mocked(api.get).mockImplementation((url: string) => {
      calls.push(url);
      if (url.includes('corax'))
        return Promise.resolve(makeGuide({ description: 'Raven appearance text.' }) as never);
      return Promise.resolve(
        makeGuide({
          scientific_name: 'Sturnus vulgaris',
          common_name: 'European Starling',
          description: 'Starling appearance text.',
        }) as never
      );
    });

    const { rerender } = render(SimilarSpeciesPanel, {
      props: { mainName: 'American Crow', similar: [entry()] },
    });
    expect(
      await screen.findByText('Raven appearance text.', {}, { timeout: 5000 })
    ).toBeInTheDocument();

    // Switch focal species → Raven leaves the list and is pruned from the cache.
    await rerender({
      mainName: 'House Sparrow',
      similar: [entry({ scientific_name: 'Sturnus vulgaris', common_name: 'European Starling' })],
    });
    expect(
      await screen.findByText('Starling appearance text.', {}, { timeout: 5000 })
    ).toBeInTheDocument();

    // Switch back → because Raven was pruned (not retained in an unbounded
    // cache), its guide is fetched again rather than served stale.
    await rerender({ mainName: 'American Crow', similar: [entry()] });
    expect(
      await screen.findByText('Raven appearance text.', {}, { timeout: 5000 })
    ).toBeInTheDocument();

    expect(calls.filter(u => u.includes('corax')).length).toBe(2);
  });
});
