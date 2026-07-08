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

  // Regression for Forgejo #1311: under defer-to-proxy every species gets a media-proxy
  // URL that can 404, so each variant must remove the img on error (the wrapper's
  // bg-base-300 shows as the placeholder) rather than leaving a broken image. Before the
  // fix the compact and list variants set imageLoadFailed but never read it in the guard.
  for (const variant of ['card', 'compact', 'list'] as const) {
    it(`renders the thumbnail image for the ${variant} variant`, () => {
      const { container } = render(SpeciesCardMobile, {
        props: { species: mockSpecies, variant },
      });

      const img = container.querySelector('img');
      expect(img).not.toBeNull();
      expect(img).toHaveAttribute('src', '/api/v2/media/image/Passer%20domesticus');
    });

    it(`removes the img on load error for the ${variant} variant`, async () => {
      const { container } = render(SpeciesCardMobile, {
        props: { species: mockSpecies, variant },
      });

      const img = container.querySelector('img');
      expect(img).not.toBeNull();
      if (img) await fireEvent.error(img);

      expect(container.querySelector('img')).toBeNull();
    });

    it(`resets the failed-load flag when the species prop changes for the ${variant} variant`, async () => {
      const { container, rerender } = render(SpeciesCardMobile, {
        props: { species: mockSpecies, variant },
      });

      const img = container.querySelector('img');
      expect(img).not.toBeNull();
      if (img) await fireEvent.error(img);
      expect(container.querySelector('img')).toBeNull();

      // A reused instance showing a different species must retry its thumbnail.
      await rerender({
        species: {
          ...mockSpecies,
          scientific_name: 'Corvus brachyrhynchos',
          common_name: 'American Crow',
          thumbnail_url: '/api/v2/media/image/Corvus%20brachyrhynchos',
        },
      });

      expect(container.querySelector('img')).not.toBeNull();
    });
  }
});
