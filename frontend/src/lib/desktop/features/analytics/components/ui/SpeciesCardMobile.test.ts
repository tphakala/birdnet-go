import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, cleanup, fireEvent } from '@testing-library/svelte';
import SpeciesCardMobile from './SpeciesCardMobile.svelte';

// Mock the i18n module (mirrors SpeciesDetailModal.test.ts).
vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => {
    const translations: Record<string, string> = {
      'analytics.species.card.detections': 'Detections',
      'analytics.species.card.confidence': 'Confidence',
      'analytics.species.card.first': 'First',
    };
    // eslint-disable-next-line security/detect-object-injection -- Test mock with controlled translation data
    return translations[key] ?? key;
  }),
}));

const mockSpecies = {
  common_name: 'House Sparrow',
  scientific_name: 'Passer domesticus',
  count: 42,
  avg_confidence: 0.85,
  max_confidence: 0.95,
  first_heard: '2024-01-15T10:30:00',
  last_heard: '2024-01-20T14:45:00',
  thumbnail_url: '/api/v2/media/image/Passer%20domesticus',
};

describe('SpeciesCardMobile', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
  });

  // Under defer-to-proxy every species gets a media-proxy URL that can 404, so each
  // variant degrades to the shared bird-silhouette placeholder (handleBirdImageError)
  // on error, matching the dashboard and analytics overview. The <img> is kept (its
  // alt text is preserved) and its src is swapped to the placeholder asset.
  for (const variant of ['card', 'compact', 'list'] as const) {
    it(`renders the thumbnail image for the ${variant} variant`, () => {
      const { container } = render(SpeciesCardMobile, {
        props: { species: mockSpecies, variant },
      });

      const img = container.querySelector('img');
      expect(img).not.toBeNull();
      expect(img).toHaveAttribute('src', '/api/v2/media/image/Passer%20domesticus');
    });

    it(`swaps to the bird placeholder on load error for the ${variant} variant`, async () => {
      const { container } = render(SpeciesCardMobile, {
        props: { species: mockSpecies, variant },
      });

      const img = container.querySelector('img');
      expect(img).not.toBeNull();
      if (img) await fireEvent.error(img);

      // The img is kept (alt preserved) with its src swapped to the placeholder,
      // rather than being removed.
      const afterError = container.querySelector('img');
      expect(afterError).not.toBeNull();
      expect(afterError?.getAttribute('src')).toContain('bird-placeholder.svg');
    });

    it(`rebinds to the new thumbnail when the species prop changes for the ${variant} variant`, async () => {
      const { container, rerender } = render(SpeciesCardMobile, {
        props: { species: mockSpecies, variant },
      });

      const img = container.querySelector('img');
      expect(img).not.toBeNull();
      if (img) await fireEvent.error(img);
      expect(container.querySelector('img')?.getAttribute('src')).toContain('bird-placeholder.svg');

      // A reused instance showing a different species must display the new
      // thumbnail again, not stay on the previous placeholder.
      await rerender({
        species: {
          ...mockSpecies,
          scientific_name: 'Corvus brachyrhynchos',
          common_name: 'American Crow',
          thumbnail_url: '/api/v2/media/image/Corvus%20brachyrhynchos',
        },
      });

      const rebound = container.querySelector('img');
      expect(rebound).not.toBeNull();
      expect(rebound).toHaveAttribute('src', '/api/v2/media/image/Corvus%20brachyrhynchos');
    });
  }
});
