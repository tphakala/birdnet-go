import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import SpeciesComparison from './SpeciesComparison.svelte';
import type { SpeciesGuideData, SimilarSpeciesResponse } from '$lib/types/species';

// Locally mock the api so we control guide/similar responses.
vi.mock('$lib/utils/api', () => ({
  api: { get: vi.fn() },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(message: string, status: number) {
      super(message);
      this.status = status;
    }
  },
}));

import { api, ApiError } from '$lib/utils/api';

function makeGuide(overrides: Partial<SpeciesGuideData> = {}): SpeciesGuideData {
  return {
    scientific_name: 'Turdus merula',
    common_name: 'Common Blackbird',
    description: 'An introduction.\n\n## Voice\nThe male sings.',
    conservation_status: '',
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

  it('renders the guide description and similar species', async () => {
    const similar: SimilarSpeciesResponse = {
      scientific_name: 'Turdus merula',
      genus: 'Turdus',
      similar: [
        {
          scientific_name: 'Turdus pilaris',
          common_name: 'Fieldfare',
          relationship: 'same_genus',
          guide_summary: 'A large thrush.',
        },
      ],
    };
    vi.mocked(api.get).mockImplementation((url: string) => {
      if (url.includes('/guide')) return Promise.resolve(makeGuide() as never);
      return Promise.resolve(similar as never);
    });

    render(SpeciesComparison, {
      props: { scientificName: 'Turdus merula', commonName: 'Common Blackbird', onclose: vi.fn() },
    });

    expect(await screen.findByText('An introduction.', {}, { timeout: 5000 })).toBeInTheDocument();
    expect(screen.getByText('Fieldfare')).toBeInTheDocument();
    expect(screen.getByText('A large thrush.')).toBeInTheDocument();
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
