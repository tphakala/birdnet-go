import { cubicOut } from 'svelte/easing';
import type { TransitionConfig } from 'svelte/transition';

/**
 * Dropdown transition combining fade + scale + slide.
 * Inspired by Radix UI / tw-animate-css dropdown animations.
 *
 * Usage:
 * ```svelte
 * <script>
 *   import { dropdown } from '$lib/utils/transitions';
 * </script>
 *
 * {#if open}
 *   <div in:dropdown out:dropdown={{ duration: 100 }}>
 *     ...menu content
 *   </div>
 * {/if}
 * ```
 */

interface DropdownParams {
  /** Vertical offset in pixels (default: -8, slides down from above) */
  y?: number;
  /** Horizontal offset in pixels (default: 0) */
  x?: number;
  /** Starting scale factor (default: 0.95) */
  start?: number;
  /** Duration in milliseconds (default: 150) */
  duration?: number;
}

export function dropdown(
  _node: Element,
  { y = -8, x = 0, start = 0.95, duration = 150 }: DropdownParams = {}
): TransitionConfig {
  const scaleRange = 1 - start;

  return {
    duration,
    easing: cubicOut,
    css: (t: number) => {
      const ty = (1 - t) * y;
      const tx = (1 - t) * x;
      const s = start + scaleRange * t;
      return `opacity: ${t}; transform: scale(${s}) translate(${tx}px, ${ty}px)`;
    },
  };
}

/**
 * Flyout transition for sidebar menus (slides from left).
 *
 * Usage:
 * ```svelte
 * <div in:flyout out:flyout={{ duration: 100 }}>
 * ```
 */
export function flyout(
  node: Element,
  params: Omit<DropdownParams, 'x' | 'y'> & { x?: number } = {}
): TransitionConfig {
  return dropdown(node, { x: params.x ?? -8, y: 0, ...params });
}
