import { describe, it, expect, afterEach } from 'vitest';
import { handleBirdImageError } from './image-utils';
import { setBasePath, resetBasePath } from '$lib/utils/urlHelpers';

describe('handleBirdImageError', () => {
  afterEach(() => {
    resetBasePath();
  });

  it('rewrites src to the placeholder asset', () => {
    const img = document.createElement('img');
    handleBirdImageError({ currentTarget: img } as unknown as Event);

    expect(img.src).toContain('/ui/assets/bird-placeholder.svg');
  });

  it('includes the configured base path when one is set (regression)', () => {
    // Simulates BirdNET-Go running behind a reverse proxy at /birdnet.
    // Without buildAppUrl, the placeholder request 404s under such setups.
    setBasePath('/birdnet');

    const img = document.createElement('img');
    handleBirdImageError({ currentTarget: img } as unknown as Event);

    expect(img.src).toContain('/birdnet/ui/assets/bird-placeholder.svg');
  });
});
