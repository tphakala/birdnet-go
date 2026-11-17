import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render } from '@testing-library/svelte';
import ModelsCard from './ModelsCard.svelte';

describe('ModelsCard hit times', () => {
  afterEach(() => cleanup());

  // The server can write hit times in another offset than the detection's; the card shows
  // them on the detection's, so they agree with the detection time.
  it('shows hit times on the detection offset', () => {
    const { container } = render(ModelsCard, {
      props: {
        detectedAt: '2026-11-14T02:57:03+02:00',
        contributions: [
          {
            modelName: 'BirdNET',
            modelVersion: '2.4',
            hitCount: 2,
            minConfidence: 0.7,
            maxConfidence: 0.9,
            firstHitAt: '2026-11-14T00:57:01Z',
            lastHitAt: '2026-11-14T00:57:04Z',
          },
        ],
      },
    });

    expect(container.querySelector('.dd-model-times')?.textContent).toContain('02:57:01-02:57:04');
  });
});
