/**
 * Image utility functions for UI components
 */

/**
 * Handles bird thumbnail image load errors by replacing failed images with a bird placeholder
 * @param e - The error event from the bird thumbnail image element
 */
export function handleBirdImageError(e: Event): void {
  const target = e.currentTarget as globalThis.HTMLImageElement;
  target.src = '/ui/assets/bird-placeholder.svg';
}
