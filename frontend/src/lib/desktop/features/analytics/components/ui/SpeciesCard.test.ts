import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, cleanup, fireEvent } from '@testing-library/svelte';
import SpeciesCard from './SpeciesCard.svelte';

// Mock the i18n module (mirrors SpeciesCardMobile.test.ts).
vi.mock('$lib/i18n', () => ({
  getLocale: vi.fn(() => 'en'),
  t: vi.fn((key: string) => {
    const translations: Record<string, string> = {
      'analytics.species.card.detections': 'Detections',
      'analytics.species.card.confidence': 'Confidence',
      'analytics.species.card.first': 'First',
      'analytics.species.openAllAboutBirds': 'View this species on All About Birds',
      'analytics.species.openWikipedia': 'View this species on Wikipedia',
      'analytics.species.viewDetections': 'View all detections of {species}',
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

describe('SpeciesCard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
  });

  // Under defer-to-proxy every species gets a media-proxy URL that can 404, so the
  // card degrades to the shared bird-silhouette placeholder (handleBirdImageError) on
  // error, matching the dashboard and analytics overview. The img is kept (alt text
  // preserved) with its src swapped to the placeholder asset.
  it('renders the thumbnail image when thumbnail_url is set', () => {
    const { container } = render(SpeciesCard, { props: { species: mockSpecies } });

    const img = container.querySelector('img');
    expect(img).not.toBeNull();
    expect(img).toHaveAttribute('src', '/api/v2/media/image/Passer%20domesticus');
  });

  it('renders localized reference links for the species', () => {
    const { container } = render(SpeciesCard, { props: { species: mockSpecies } });

    // Reference-site links open in a new tab; the two detections-list links
    // (image + name/scientific-name) are same-tab SPA navigation and are
    // covered by the test below.
    const links = Array.from(container.querySelectorAll('a[target="_blank"]'));
    expect(links.map(link => link.getAttribute('href'))).toEqual([
      'https://www.allaboutbirds.org/guide/House_Sparrow/id',
      'https://en.wikipedia.org/wiki/House_Sparrow',
    ]);
  });

  it('links the thumbnail and name/scientific-name to the species detection list', () => {
    const { container } = render(SpeciesCard, { props: { species: mockSpecies } });

    const detectionLinks = Array.from(container.querySelectorAll('a')).filter(
      link => link.getAttribute('target') !== '_blank'
    );
    expect(detectionLinks).toHaveLength(2);
    for (const link of detectionLinks) {
      const href = link.getAttribute('href') ?? '';
      expect(href).toContain('/ui/detections?');
      expect(href).toContain('queryType=search');
      expect(href).toContain('species=Passer+domesticus');
    }
  });

  it('swaps to the bird placeholder on load error', async () => {
    const { container } = render(SpeciesCard, { props: { species: mockSpecies } });

    const img = container.querySelector('img');
    expect(img).not.toBeNull();
    if (img) await fireEvent.error(img);

    const afterError = container.querySelector('img');
    expect(afterError).not.toBeNull();
    expect(afterError?.getAttribute('src')).toContain('bird-placeholder.svg');
  });

  it('rebinds to the new thumbnail when the species prop changes', async () => {
    const { container, rerender } = render(SpeciesCard, { props: { species: mockSpecies } });

    const img = container.querySelector('img');
    expect(img).not.toBeNull();
    if (img) await fireEvent.error(img);
    expect(container.querySelector('img')?.getAttribute('src')).toContain('bird-placeholder.svg');

    // A reused instance showing a different species must display the new thumbnail
    // again, not stay on the previous placeholder.
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
});
