/**
 * Image utility functions for UI components
 */

import { buildAppUrl } from '$lib/utils/urlHelpers';

/**
 * Handles bird thumbnail image load errors by replacing failed images with a bird placeholder
 * @param e - The error event from the bird thumbnail image element
 */
/** Static asset path of the bird-silhouette placeholder (before base-path resolution). */
const BIRD_PLACEHOLDER_PATH = '/ui/assets/bird-placeholder.svg';

export function handleBirdImageError(e: Event): void {
  const target = e.currentTarget as globalThis.HTMLImageElement;
  // Guard against an infinite onerror loop if the placeholder asset itself fails to
  // load. Match the stable path with includes() so a base path or any query/hash
  // suffix (dev server, CDN, cache-busting) can't defeat the check. Using this rather
  // than nulling target.onerror keeps error handling working for a reused <img> whose
  // bound src later changes to a new (potentially also-failing) thumbnail.
  if (target.src.includes(BIRD_PLACEHOLDER_PATH)) return;
  target.src = buildAppUrl(BIRD_PLACEHOLDER_PATH);
}
