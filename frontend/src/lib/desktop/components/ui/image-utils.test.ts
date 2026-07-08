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

  it('does not re-swap when the src is already the placeholder (avoids an onerror loop)', () => {
    const img = document.createElement('img');
    // First failure swaps to the placeholder.
    handleBirdImageError({ currentTarget: img } as unknown as Event);
    const afterFirst = img.src;
    expect(afterFirst).toContain('/ui/assets/bird-placeholder.svg');

    // A subsequent error (e.g. the placeholder asset itself failing) is a no-op: the
    // guard returns early instead of re-assigning src, which would re-fire onerror.
    handleBirdImageError({ currentTarget: img } as unknown as Event);
    expect(img.src).toBe(afterFirst);
  });
});
