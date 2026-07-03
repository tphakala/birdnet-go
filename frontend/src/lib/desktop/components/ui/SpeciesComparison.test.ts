import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import SpeciesComparison from './SpeciesComparison.svelte';
import type { SpeciesGuideData, SimilarSpeciesResponse } from '$lib/types/species';

// Locally mock the api so we control guide/similar responses.
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
    scientific_name: 'Turdus merula',
    common_name: 'Common Blackbird',
    description: 'An introduction.\n\n## Voice\nThe male sings.',
    quality: 'full',
    features: { notes: true, enrichments: true, similar_species: true },
    source: { provider: 'wikipedia', url: '', license: '', license_url: '' },
    partial: false,
    cached_at: '2026-01-01T00:00:00Z',
    ...overrides,
  };
}

describe('SpeciesComparison', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders the guide description and the similar-species comparison panel', async () => {
    const similar: SimilarSpeciesResponse = {
      scientific_name: 'Turdus merula',
      genus: 'Turdus',
      similar: [
        {
          scientific_name: 'Turdus pilaris',
          common_name: 'Fieldfare',
          relationship: 'same_genus',
          has_guide: true,
          guide_summary: 'A large thrush.',
        },
      ],
    };
    vi.mocked(api.get).mockImplementation((url: string) => {
      if (url.includes('/similar')) return Promise.resolve(similar as never);
      // The first species with a guide is auto-selected, so its /guide is
      // fetched; return a distinct description to assert the panel surfaces it.
      if (url.includes('pilaris'))
        return Promise.resolve(
          makeGuide({
            scientific_name: 'Turdus pilaris',
            common_name: 'Fieldfare',
            description: 'A large grey-headed thrush.\n\n## Voice\nA harsh chatter.',
          }) as never
        );
      return Promise.resolve(makeGuide() as never);
    });

    render(SpeciesComparison, {
      props: { scientificName: 'Turdus merula', commonName: 'Common Blackbird', onclose: vi.fn() },
    });

    expect(await screen.findByText('An introduction.', {}, { timeout: 5000 })).toBeInTheDocument();
    // The similar species appears in the picker rail (and again as the selected
    // card header once auto-selected), so there is at least one match.
    expect(screen.getAllByText('Fieldfare').length).toBeGreaterThan(0);
    // ...and auto-selecting it surfaces its appearance section in the diff card.
    expect(
      await screen.findByText('A large grey-headed thrush.', {}, { timeout: 5000 })
    ).toBeInTheDocument();
  });

  it('renders enrichment badges and external links when enrichments are enabled', async () => {
    vi.mocked(api.get).mockImplementation((url: string) => {
      if (url.includes('/guide'))
        return Promise.resolve(
          makeGuide({
            expectedness: 'expected',
            current_season: 'summer',
            external_links: [
              { name: 'Wikipedia', url: 'https://de.wikipedia.org/wiki/Turdus_merula' },
              { name: 'iNaturalist', url: 'https://www.inaturalist.org/taxa/12716?locale=de' },
            ],
          }) as never
        );
      return Promise.resolve({ scientific_name: 'x', genus: '', similar: [] } as never);
    });

    render(SpeciesComparison, {
      props: { scientificName: 'Turdus merula', commonName: 'Common Blackbird', onclose: vi.fn() },
    });

    await screen.findByText('An introduction.', {}, { timeout: 5000 });
    expect(screen.getByText('analytics.species.guide.expectedness.expected')).toBeInTheDocument();
    expect(
      screen.getByText('analytics.species.guide.season.summer', { exact: false })
    ).toBeInTheDocument();
    const wiki = screen.getByText('Wikipedia');
    expect(wiki).toBeInTheDocument();
    expect(wiki).toHaveAttribute('href', 'https://de.wikipedia.org/wiki/Turdus_merula');
    // The renderer is source-agnostic: a new source (iNaturalist) renders with no
    // per-source code, proving the generic external-link card handles any source.
    const inat = screen.getByText('iNaturalist');
    expect(inat).toBeInTheDocument();
    expect(inat).toHaveAttribute('href', 'https://www.inaturalist.org/taxa/12716?locale=de');
  });

  it('hides enrichment badges when the enrichments feature is disabled', async () => {
    vi.mocked(api.get).mockImplementation((url: string) => {
      if (url.includes('/guide'))
        return Promise.resolve(
          makeGuide({
            features: { notes: true, enrichments: false, similar_species: true },
            expectedness: 'expected',
            current_season: 'summer',
          }) as never
        );
      return Promise.resolve({ scientific_name: 'x', genus: '', similar: [] } as never);
    });

    render(SpeciesComparison, {
      props: { scientificName: 'Turdus merula', commonName: 'Common Blackbird', onclose: vi.fn() },
    });

    await screen.findByText('An introduction.', {}, { timeout: 5000 });
    expect(
      screen.queryByText('analytics.species.guide.expectedness.expected')
    ).not.toBeInTheDocument();
  });

  it('shows the empty state when there are no similar species', async () => {
    vi.mocked(api.get).mockImplementation((url: string) => {
      if (url.includes('/guide'))
        return Promise.resolve(makeGuide({ description: 'Just an intro.' }) as never);
      return Promise.resolve({ scientific_name: 'x', genus: '', similar: [] } as never);
    });

    render(SpeciesComparison, {
      props: { scientificName: 'Turdus merula', commonName: 'Common Blackbird', onclose: vi.fn() },
    });

    expect(
      await screen.findByText('analytics.species.similar.empty', {}, { timeout: 5000 })
    ).toBeInTheDocument();
  });

  it('shows an unavailable message on a 503 response', async () => {
    vi.mocked(api.get).mockRejectedValue(
      new ApiError('unavailable', 503, new Response(null, { status: 503 }))
    );

    render(SpeciesComparison, {
      props: { scientificName: 'Turdus merula', commonName: 'Common Blackbird', onclose: vi.fn() },
    });

    expect(await screen.findByRole('alert', {}, { timeout: 5000 })).toBeInTheDocument();
  });

  it('shows a soft no-guide message (not a red error) on a 404 response', async () => {
    vi.mocked(api.get).mockRejectedValue(
      new ApiError('not found', 404, new Response(null, { status: 404 }))
    );

    render(SpeciesComparison, {
      props: { scientificName: 'Turdus merula', commonName: 'Common Blackbird', onclose: vi.fn() },
    });

    expect(
      await screen.findByText('analytics.species.guide.noGuide', {}, { timeout: 5000 })
    ).toBeInTheDocument();
    // A benign 404 must not be surfaced as an error alert.
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('still renders the similar-species list when the guide 404s (independent endpoints)', async () => {
    const similar: SimilarSpeciesResponse = {
      scientific_name: 'Turdus merula',
      genus: 'Turdus',
      similar: [
        {
          scientific_name: 'Turdus pilaris',
          common_name: 'Fieldfare',
          relationship: 'same_genus',
          has_guide: false,
        },
      ],
    };
    vi.mocked(api.get).mockImplementation((url: string) => {
      if (url.includes('/similar')) return Promise.resolve(similar as never);
      return Promise.reject(new ApiError('not found', 404, new Response(null, { status: 404 })));
    });

    render(SpeciesComparison, {
      props: { scientificName: 'Turdus merula', commonName: 'Common Blackbird', onclose: vi.fn() },
    });

    // The soft no-guide message shows for the missing guide content...
    expect(
      await screen.findByText('analytics.species.guide.noGuide', {}, { timeout: 5000 })
    ).toBeInTheDocument();
    // ...but the successfully fetched similar list is not discarded.
    expect(screen.getAllByText('Fieldfare').length).toBeGreaterThan(0);
  });

  it('hides the similar-species section when the guide reports similar_species=false', async () => {
    vi.mocked(api.get).mockImplementation((url: string) => {
      if (url.includes('/guide'))
        return Promise.resolve(
          makeGuide({
            features: { notes: true, enrichments: true, similar_species: false },
          }) as never
        );
      return Promise.resolve({ scientific_name: 'x', genus: '', similar: [] } as never);
    });

    render(SpeciesComparison, {
      props: { scientificName: 'Turdus merula', commonName: 'Common Blackbird', onclose: vi.fn() },
    });

    // Description still renders (guide feature itself is on)...
    await screen.findByText('An introduction.', {}, { timeout: 5000 });
    // ...but the similar-species section is gated off by the server flag.
    expect(screen.queryByText('analytics.species.similar.title')).not.toBeInTheDocument();
  });

  it('skips the similar fetch entirely when showSimilarSpecies=false', async () => {
    vi.mocked(api.get).mockImplementation((url: string) => {
      if (url.includes('/guide'))
        return Promise.resolve(
          makeGuide({
            features: { notes: true, enrichments: true, similar_species: false },
          }) as never
        );
      return Promise.resolve({ scientific_name: 'x', genus: '', similar: [] } as never);
    });

    render(SpeciesComparison, {
      props: {
        scientificName: 'Turdus merula',
        commonName: 'Common Blackbird',
        showSimilarSpecies: false,
        onclose: vi.fn(),
      },
    });

    await screen.findByText('An introduction.', {}, { timeout: 5000 });
    const urls = vi.mocked(api.get).mock.calls.map(c => String(c[0]));
    expect(urls.some(u => u.includes('/similar'))).toBe(false);
  });

  it('invokes onclose when the close button is clicked', async () => {
    const onclose = vi.fn();
    vi.mocked(api.get).mockResolvedValue(makeGuide() as never);

    render(SpeciesComparison, {
      props: { scientificName: 'Turdus merula', commonName: 'Common Blackbird', onclose },
    });

    const closeButton = await screen.findByTestId(
      'species-comparison-close',
      {},
      { timeout: 5000 }
    );
    closeButton.click();
    expect(onclose).toHaveBeenCalledOnce();
  });
});
