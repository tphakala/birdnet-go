import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import SimilarSpeciesPanel from './SimilarSpeciesPanel.svelte';
import type { SimilarSpeciesEntry, SpeciesGuideData } from '$lib/types/species';

// Locally mock the api so we control per-species guide responses.
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
          'A large, heavy-billed corvid.\n\n## Voice\nA deep croaking gronk.\n\n## Distribution and habitat\nMountains and coasts.',
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
    // The "vs main species" header is shown.
    expect(screen.getByText('analytics.species.similar.versus')).toBeInTheDocument();
  });

  it('disables species without a guide and explains why', () => {
    render(SimilarSpeciesPanel, {
      props: {
        mainName: 'American Crow',
        similar: [entry({ common_name: 'Mystery Bird', has_guide: false })],
      },
    });

    const button = screen.getByRole('button', { name: /Mystery Bird/ });
    expect(button).toBeDisabled();
    // No selectable species → the card prompts the user to pick one.
    expect(screen.getByText('analytics.species.similar.selectPrompt')).toBeInTheDocument();
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
});
