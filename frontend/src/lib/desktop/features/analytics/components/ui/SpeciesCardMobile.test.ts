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

  // The desktop analytics grid switched from the non-interactive SpeciesCard to this
  // component's `card` variant, which is a <button> that opens the species detail
  // modal. The thumbnail behaviour above already covered what the retired component
  // was tested for; the click affordance it gained was not covered anywhere.
  describe('card variant (desktop analytics grid)', () => {
    it('renders as a button so the card is keyboard reachable', () => {
      const { getByRole } = render(SpeciesCardMobile, {
        props: { species: mockSpecies, variant: 'card', onClick: vi.fn() },
      });

      // getByRole throws when absent, so it both asserts and narrows to a non-null
      // element — no conditional guard, and no type assertion.
      expect(getByRole('button')).toBeInTheDocument();
    });

    it('invokes onClick with the species when activated', async () => {
      const onClick = vi.fn();
      const { getByRole } = render(SpeciesCardMobile, {
        props: { species: mockSpecies, variant: 'card', onClick },
      });

      await fireEvent.click(getByRole('button'));

      expect(onClick).toHaveBeenCalledTimes(1);
      expect(onClick).toHaveBeenCalledWith(mockSpecies);
    });

    it('stays inert when no onClick handler is supplied', async () => {
      // The grid always passes a handler, but the prop is optional; clicking without
      // one must not throw. fireEvent rejects on a listener error, so an unhandled
      // click surfaces as a failed assertion here.
      const { getByRole } = render(SpeciesCardMobile, {
        props: { species: mockSpecies, variant: 'card' },
      });

      await expect(fireEvent.click(getByRole('button'))).resolves.not.toThrow();
    });

    it('shows the species name and detection stats', () => {
      const { getByText } = render(SpeciesCardMobile, {
        props: { species: mockSpecies, variant: 'card', onClick: vi.fn() },
      });

      expect(getByText('House Sparrow')).toBeInTheDocument();
      expect(getByText('Passer domesticus')).toBeInTheDocument();
      expect(getByText('42')).toBeInTheDocument();
      expect(getByText('85.0%')).toBeInTheDocument();
    });
  });
});
