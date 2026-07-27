import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import {
  handleBirdImageError,
  THUMBNAIL_RETRY_DELAYS_MS,
  BIRD_PLACEHOLDER_PATH,
} from './image-utils';
import { setBasePath, resetBasePath } from '$lib/utils/urlHelpers';

/**
 * Builds an <img> attached to the document, because the retry only restores the src of
 * an element still in the DOM: a detached element means the row was destroyed and its
 * thumbnail no longer matters.
 */
function mountImage(src: string): globalThis.HTMLImageElement {
  const img = document.createElement('img');
  img.src = src;
  document.body.appendChild(img);
  return img;
}

describe('handleBirdImageError', () => {
  afterEach(() => {
    resetBasePath();
    document.body.innerHTML = '';
  });

  it('rewrites src to the placeholder asset', () => {
    const img = document.createElement('img');
    handleBirdImageError({ currentTarget: img } as unknown as Event);

    expect(img.src).toContain(BIRD_PLACEHOLDER_PATH);
  });

  it('includes the configured base path when one is set (regression)', () => {
    // Simulates BirdNET-Go running behind a reverse proxy at /birdnet.
    // Without buildAppUrl, the placeholder request 404s under such setups.
    setBasePath('/birdnet');

    const img = document.createElement('img');
    handleBirdImageError({ currentTarget: img } as unknown as Event);

    expect(img.src).toContain(`/birdnet${BIRD_PLACEHOLDER_PATH}`);
  });

  it('does not re-swap when the src is already the placeholder (avoids an onerror loop)', () => {
    const img = document.createElement('img');
    // First failure swaps to the placeholder.
    handleBirdImageError({ currentTarget: img } as unknown as Event);
    const afterFirst = img.src;
    expect(afterFirst).toContain(BIRD_PLACEHOLDER_PATH);

    // A subsequent error (e.g. the placeholder asset itself failing) is a no-op: the
    // guard returns early instead of re-assigning src, which would re-fire onerror.
    handleBirdImageError({ currentTarget: img } as unknown as Event);
    expect(img.src).toBe(afterFirst);
  });
});

/**
 * Retry behaviour.
 *
 * The media proxy no longer resolves an image on the request path, so a species that is
 * not cached yet answers "not yet" rather than eventually returning bytes. Without a
 * retry the placeholder is permanent until a hard refresh, which on a cold cache means
 * silhouettes across the whole page.
 */
describe('handleBirdImageError retries', () => {
  const THUMBNAIL_URL = 'http://localhost/api/v2/media/image/Turdus%20merula';

  /** Probe images created by the retry timer, newest last. */
  let probes: MockProbe[];

  /**
   * Stands in for the detached Image() the retry uses to test the URL before touching
   * the visible element. jsdom never loads anything, so the test drives the outcome.
   */
  class MockProbe {
    onload: (() => void) | null = null;
    onerror: (() => void) | null = null;
    #src = '';

    constructor() {
      probes.push(this);
    }

    get src(): string {
      return this.#src;
    }

    set src(value: string) {
      this.#src = value;
    }
  }

  beforeEach(() => {
    vi.useFakeTimers();
    probes = [];
    vi.stubGlobal('Image', MockProbe);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    document.body.innerHTML = '';
    resetBasePath();
  });

  it('reports that a retry is pending so callers do not blacklist the URL', () => {
    const img = mountImage(THUMBNAIL_URL);
    expect(handleBirdImageError({ currentTarget: img } as unknown as Event)).toBe(true);
  });

  it('restores the original src only after a probe confirms the image loads', () => {
    const img = mountImage(THUMBNAIL_URL);
    handleBirdImageError({ currentTarget: img } as unknown as Event);
    expect(img.src).toContain(BIRD_PLACEHOLDER_PATH);

    vi.advanceTimersByTime((THUMBNAIL_RETRY_DELAYS_MS.at(0) ?? 0) * 2);

    // The probe is loading; the visible element must still show the placeholder. This
    // is the anti-flicker property: assigning the visible src speculatively would
    // blink a broken image on every attempt for a species that has no image at all.
    expect(probes).toHaveLength(1);
    expect(probes.at(0)?.src).toBe(THUMBNAIL_URL);
    expect(img.src).toContain(BIRD_PLACEHOLDER_PATH);

    probes.at(0)?.onload?.();
    expect(img.src).toBe(THUMBNAIL_URL);
  });

  it('backs off across every configured attempt and then gives up', () => {
    const img = mountImage(THUMBNAIL_URL);
    handleBirdImageError({ currentTarget: img } as unknown as Event);

    for (const [index, delay] of THUMBNAIL_RETRY_DELAYS_MS.entries()) {
      // The schedule is jittered, so advance past the upper bound of the window.
      vi.advanceTimersByTime(delay * 2);
      expect(probes).toHaveLength(index + 1);
      probes.at(index)?.onerror?.();
    }

    // Retries exhausted: no further timer is armed, so the species settles on the
    // placeholder instead of polling forever.
    vi.advanceTimersByTime(600000);
    expect(probes).toHaveLength(THUMBNAIL_RETRY_DELAYS_MS.length);
    expect(img.src).toContain(BIRD_PLACEHOLDER_PATH);
  });

  it('reports no pending retry once the attempts are spent', () => {
    const img = mountImage(THUMBNAIL_URL);
    handleBirdImageError({ currentTarget: img } as unknown as Event);

    for (const delay of THUMBNAIL_RETRY_DELAYS_MS) {
      vi.advanceTimersByTime(delay * 2);
      probes.at(-1)?.onerror?.();
    }

    // A further error on the same URL must report false so DetectionRow can finally
    // record the failure and stop rendering the element.
    img.src = THUMBNAIL_URL;
    expect(handleBirdImageError({ currentTarget: img } as unknown as Event)).toBe(false);
  });

  it('does not touch an element that was reused for a different species', () => {
    const img = mountImage(THUMBNAIL_URL);
    handleBirdImageError({ currentTarget: img } as unknown as Event);

    // The row is recycled before the retry fires, which is routine in a scrolling list.
    const otherUrl = 'http://localhost/api/v2/media/image/Parus%20major';
    img.src = otherUrl;

    vi.advanceTimersByTime((THUMBNAIL_RETRY_DELAYS_MS.at(0) ?? 0) * 2);

    expect(probes).toHaveLength(0);
    expect(img.src).toBe(otherUrl);
  });

  it('does not restore the src of an element removed from the document', () => {
    const img = mountImage(THUMBNAIL_URL);
    handleBirdImageError({ currentTarget: img } as unknown as Event);
    img.remove();

    vi.advanceTimersByTime((THUMBNAIL_RETRY_DELAYS_MS.at(0) ?? 0) * 2);

    expect(probes).toHaveLength(0);
  });
});
