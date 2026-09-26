/**
 * useCloseSignal.svelte.ts
 *
 * Shared "force-close on external signal" behavior for popup-owning buttons
 * (AudibleBatsButton, AudioSettingsButton). A parent bumps a numeric
 * `closeSignal` prop to force a sibling popup closed (mutual exclusion: only
 * one of the two popups should ever be open at once). This composable detects
 * the bump and invokes the caller's close callback exactly once per change.
 */

/**
 * Watches a `closeSignal` prop for changes and invokes `onSignal` whenever it
 * changes, once per bump. Must be called during component initialization
 * (top-level of a component's `<script>`), like any other rune-based effect.
 *
 * @param getSignal Reads the current closeSignal prop value (called reactively).
 * @param onSignal Invoked when closeSignal changes; the caller decides whether
 *   the popup is actually open and closes it (and notifies its own onMenuClose).
 */
export function useCloseSignal(getSignal: () => number, onSignal: () => void): void {
  let previous = getSignal();
  $effect(() => {
    const current = getSignal();
    if (current !== previous) {
      previous = current;
      onSignal();
    }
  });
}
