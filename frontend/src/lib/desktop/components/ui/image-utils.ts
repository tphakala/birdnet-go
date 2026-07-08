/**
 * Image utility functions for UI components
 */

import { buildAppUrl } from '$lib/utils/urlHelpers';

/**
 * Handles bird thumbnail image load errors by replacing failed images with a bird placeholder
 * @param e - The error event from the bird thumbnail image element
 */
export function handleBirdImageError(e: Event): void {
  const target = e.currentTarget as globalThis.HTMLImageElement;
  const placeholderSrc = buildAppUrl('/ui/assets/bird-placeholder.svg');
  // Guard against an infinite onerror loop if the placeholder asset itself fails to
  // load. Comparing against the current src (rather than nulling target.onerror)
  // keeps error handling working for a reused <img> whose bound src later changes to
  // a new (potentially also-failing) thumbnail.
  if (target.src.endsWith(placeholderSrc)) return;
  target.src = placeholderSrc;
}
