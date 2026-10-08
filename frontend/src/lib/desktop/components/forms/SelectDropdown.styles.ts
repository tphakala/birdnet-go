/**
 * Class strings for SelectDropdown options.
 *
 * Option text and indicators use the base-content color, which does not depend on the active
 * color scheme. The scheme's primary color appears only as a decorative background tint, so
 * contrast holds for every scheme, including the user-defined custom one.
 *
 * Class names stay literal strings so Tailwind can find them when it scans the source.
 */
import { cn } from '$lib/utils/cn';

/** Classes every option carries: base-content text and a visible keyboard focus outline. */
export const OPTION_BASE_CLASS =
  'w-full text-left flex items-center gap-2 rounded text-[var(--color-base-content)] focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-[var(--color-base-content)]';

/** Background of a selected option: decorative scheme tint; the check icon carries the state. */
export const OPTION_SELECTED_BG_CLASS =
  'bg-[color-mix(in_srgb,var(--color-primary)_10%,transparent)]';

/** Background of the keyboard-highlighted option when it is not selected. */
export const OPTION_HIGHLIGHT_BG_CLASS =
  'bg-[color-mix(in_srgb,var(--color-base-content)_10%,transparent)]';

/** Hover background of an unselected option. */
export const OPTION_HOVER_CLASS =
  'hover:bg-[color-mix(in_srgb,var(--color-base-content)_10%,transparent)]';

/** Outline of the keyboard-highlighted option, inset so the list's overflow does not clip it. */
export const OPTION_HIGHLIGHT_OUTLINE_CLASS =
  'outline-2 -outline-offset-2 outline-[var(--color-base-content)]';

/**
 * State classes of one option; exactly one plain background utility per combination.
 *
 * A selected option never gets a hover background, so the tint stays visible under the pointer.
 * `cn` only concatenates and does not merge conflicting Tailwind classes, so the background is
 * chosen here instead of stacking several `bg-*` classes.
 */
export function getOptionStateClasses(state: { selected: boolean; highlighted: boolean }): string {
  return cn(
    state.selected
      ? OPTION_SELECTED_BG_CLASS
      : [OPTION_HOVER_CLASS, state.highlighted && OPTION_HIGHLIGHT_BG_CLASS],
    state.highlighted && OPTION_HIGHLIGHT_OUTLINE_CLASS
  );
}
