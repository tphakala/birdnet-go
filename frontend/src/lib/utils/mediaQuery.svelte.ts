/**
 * Reactive media query matching.
 *
 * Exists so a component can *mount* only the layout that is actually on screen
 * instead of rendering every layout and hiding the others with CSS. A hidden
 * subtree is not free: its components still run their effects, still request
 * their images, and still create their audio elements, so a list that renders a
 * desktop table and a mobile card for every row does twice the work and issues
 * twice the requests, on every device.
 */
import { onMount } from 'svelte';

/**
 * Track whether `query` currently matches.
 *
 * Returns a getter rather than a value so callers read it reactively:
 *
 * ```ts
 * const isWide = useMediaQuery('(min-width: 768px)');
 * // in markup: {#if isWide.matches} ... {/if}
 * ```
 *
 * Server-side and before mount there is no viewport to measure, so `initial`
 * decides what renders first; it defaults to true because this UI targets
 * tablet and desktop.
 */
export function useMediaQuery(query: string, initial = true): { readonly matches: boolean } {
  let matches = $state(
    typeof window !== 'undefined' && typeof window.matchMedia === 'function'
      ? window.matchMedia(query).matches
      : initial
  );

  onMount(() => {
    // Guarded for jsdom and other environments without matchMedia, where the
    // initial value stands and the list simply keeps its default layout.
    if (typeof window.matchMedia !== 'function') return;
    const list = window.matchMedia(query);
    matches = list.matches;
    const onChange = (event: MediaQueryListEvent) => {
      matches = event.matches;
    };
    list.addEventListener('change', onChange);
    return () => list.removeEventListener('change', onChange);
  });

  return {
    get matches() {
      return matches;
    },
  };
}
