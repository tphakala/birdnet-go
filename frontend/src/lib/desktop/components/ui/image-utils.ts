/**
 * Image utility functions for UI components
 */

import { buildAppUrl } from '$lib/utils/urlHelpers';

/** Static asset path of the bird-silhouette placeholder (before base-path resolution). */
export const BIRD_PLACEHOLDER_PATH = '/ui/assets/bird-placeholder.svg';

/**
 * Delays, in milliseconds, before each retry of a failed thumbnail.
 *
 * The media proxy no longer fetches from an image provider on the request path, so a
 * species whose image is not cached yet answers "not yet" and only becomes available
 * once a background fetch completes, which can take several seconds behind the
 * provider's rate limiter. Without a retry the placeholder is permanent until a hard
 * refresh, so a cold cache renders silhouettes across the whole page.
 *
 * The schedule is finite on purpose: the point is to cover the window a background
 * fetch realistically lands in, not to poll indefinitely. It brackets the measured
 * cold-cache tail (30 dashboard thumbnails all resolved within about 80 seconds on a
 * 2-core QA VM), because a species whose fetch lands after the last attempt settles on
 * the placeholder with no further recovery short of a reload. The first delay is at or
 * beyond the server's advertised Retry-After.
 *
 * Exported for tests so a tuning change does not silently break them against a
 * hardcoded copy.
 */
export const THUMBNAIL_RETRY_DELAYS_MS = [5000, 15000, 40000, 60000];

/**
 * Jitter applied to each delay, as a fraction. Every thumbnail on a page fails within
 * the same few hundred milliseconds, so an unjittered schedule sends all of their
 * retries in one burst, competing with the very background fetches they are waiting
 * on.
 */
const THUMBNAIL_RETRY_JITTER = 0.3;

interface RetryState {
  /** The thumbnail URL that failed, restored on the next attempt. */
  url: string;
  /** How many retries have already been scheduled for this URL. */
  attempts: number;
  /** Pending timer, cancelled if the element is reused for a different image. */
  timer: ReturnType<typeof globalThis.setTimeout> | undefined;
}

/**
 * Per-element retry bookkeeping. A WeakMap keeps this from pinning detached <img>
 * elements alive: a row scrolled out of a virtualized list is collected along with
 * its entry.
 */
const retryStates = new WeakMap<globalThis.HTMLImageElement, RetryState>();

function isPlaceholder(src: string): boolean {
  // Match the stable path with includes() so a base path or any query/hash suffix
  // (dev server, CDN, cache-busting) can't defeat the check.
  return src.includes(BIRD_PLACEHOLDER_PATH);
}

/**
 * Schedules the next retry for a failed thumbnail, if any attempts remain.
 *
 * The retry loads the URL into a DETACHED probe image first and only assigns the
 * visible element's src once the probe reports success. Assigning the visible src
 * directly would swap the placeholder out on every attempt, so a species that
 * genuinely has no image would flash a broken image three times before settling.
 *
 * The probe re-requests the SAME URL, deliberately without a cache-busting parameter,
 * because the server distinguishes the two failure modes with cache headers: a species
 * with no image answers 404 with a long max-age, so the probe is served from the
 * browser's own HTTP cache with no network hop, while a species still being fetched
 * answers 503 with no-store, so the probe actually reaches the server. An <img> error
 * event exposes no status code, which is why the cache does that discrimination.
 *
 * @returns true when a retry was scheduled.
 */
function scheduleThumbnailRetry(target: globalThis.HTMLImageElement, failedUrl: string): boolean {
  const existing = retryStates.get(target);
  const attempts = existing?.url === failedUrl ? existing.attempts : 0;
  // .at() rather than an index expression: it is typed as possibly-undefined, so the
  // exhausted case has to be handled instead of silently yielding NaN.
  const baseDelay = THUMBNAIL_RETRY_DELAYS_MS.at(attempts);

  if (existing?.timer !== undefined) {
    globalThis.clearTimeout(existing.timer);
  }

  if (baseDelay === undefined) {
    // Retries exhausted. Keep the state so a later error on the same URL is not
    // treated as a fresh first failure.
    retryStates.set(target, { url: failedUrl, attempts, timer: undefined });
    return false;
  }

  const delay = baseDelay * (1 + (Math.random() * 2 - 1) * THUMBNAIL_RETRY_JITTER);

  const timer = globalThis.setTimeout(() => {
    const state = retryStates.get(target);
    if (state) state.timer = undefined;
    // Anything other than "still showing the placeholder for this URL" means the
    // element was reused for a different detection, and touching its src now would
    // swap in the wrong bird.
    if (!target.isConnected || !isPlaceholder(target.src)) return;

    // isPlaceholder alone cannot tell "the placeholder for THIS url" from "the
    // placeholder for some other species", because it is one shared asset path. Both
    // probe callbacks therefore re-check that this element's retry state still
    // belongs to the URL this probe was started for: a virtualized row reused for a
    // different detection would otherwise be painted with the previous bird, and the
    // dead chain would take over the new species' retry budget.
    const stillOurs = () => retryStates.get(target)?.url === failedUrl;

    const probe = new globalThis.Image();
    probe.onload = () => {
      if (!stillOurs() || !target.isConnected || !isPlaceholder(target.src)) return;
      target.src = failedUrl;
    };
    // Still failing: fall through to the next delay, or stop if this was the last.
    probe.onerror = () => {
      if (!stillOurs()) return;
      scheduleThumbnailRetry(target, failedUrl);
    };
    probe.src = failedUrl;
  }, delay);

  retryStates.set(target, { url: failedUrl, attempts: attempts + 1, timer });
  return true;
}

/**
 * Swaps a failed bird thumbnail for the placeholder and, for a bounded number of
 * attempts, schedules a retry of the original URL.
 *
 * @param e - The error event from the bird thumbnail image element
 * @returns true when a retry is pending, so callers that blacklist failed URLs can
 *          hold off until the retries are actually exhausted.
 */
export function handleBirdImageError(e: Event): boolean {
  const target = e.currentTarget as globalThis.HTMLImageElement;

  // Guard against an infinite onerror loop if the placeholder asset itself fails to
  // load. Using this rather than nulling target.onerror keeps error handling working
  // for a reused <img> whose bound src later changes to a new (potentially
  // also-failing) thumbnail.
  if (isPlaceholder(target.src)) return false;

  const failedUrl = target.src;
  target.src = buildAppUrl(BIRD_PLACEHOLDER_PATH);

  // An element that is not in the document has nothing to retry into: the timer would
  // fire, find it still detached, and do nothing. Skipping keeps real timers from
  // outliving an unmounted component.
  if (!target.isConnected) return false;

  return scheduleThumbnailRetry(target, failedUrl);
}
