import { describe, it, expect, afterEach } from 'vitest';
import { cleanup } from '@testing-library/svelte';
import { createComponentTestFactory } from '../../../../../test/render-helpers';
import { setBasePath, resetBasePath } from '$lib/utils/urlHelpers';
import type { Detection } from '$lib/types/detection.types';
import SpeciesInfoBar from './SpeciesInfoBar.svelte';

const baseDetection: Detection = {
  id: 1,
  date: '2026-04-27',
  time: '10:00',
  beginTime: '2026-04-27T10:00:00',
  endTime: '2026-04-27T10:00:03',
  speciesCode: 'cardpu',
  scientificName: 'Cardellina pusilla',
  commonName: "Wilson's Warbler",
  confidence: 0.92,
  verified: 'unverified',
  locked: false,
};

const speciesInfoBar = createComponentTestFactory(SpeciesInfoBar);

describe('SpeciesInfoBar', () => {
  afterEach(() => {
    cleanup();
    resetBasePath();
  });

  it('builds the species image URL without a base path by default', () => {
    const { container } = speciesInfoBar.render({ props: { detection: baseDetection } });

    const img = container.querySelector('img');
    if (!img) throw new Error('expected an <img> to be rendered');
    expect(img.getAttribute('src')).toBe('/api/v2/media/species-image?name=Cardellina%20pusilla');
  });

  it('prefixes the species image URL with the configured base path (regression)', () => {
    // Without buildAppUrl, this image 404s under reverse-proxy setups like
    // /birdnet or Home Assistant Ingress. See fix in fix/image-base-paths.
    setBasePath('/birdnet');

    const { container } = speciesInfoBar.render({ props: { detection: baseDetection } });

    const img = container.querySelector('img');
    if (!img) throw new Error('expected an <img> to be rendered');
    expect(img.getAttribute('src')).toBe(
      '/birdnet/api/v2/media/species-image?name=Cardellina%20pusilla'
    );
  });
});
