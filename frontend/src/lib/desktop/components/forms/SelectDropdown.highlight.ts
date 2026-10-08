/**
 * Pure helpers for the keyboard-highlighted option of SelectDropdown: the id of an option and
 * the id the trigger and search box name as their active descendant. Kept free of Svelte so
 * the bounds rules can be unit tested.
 */

/** DOM id of the option at `index` (position in rendered order) for the dropdown `fieldId`. */
export function optionId(fieldId: string, index: number): string {
  return `${fieldId}-option-${index}`;
}

/**
 * Id to expose as aria-activedescendant: the highlighted option's id, or undefined when
 * nothing is highlighted or the highlight lies outside the rendered options (for example after
 * the list shrank), so the attribute never names a missing element.
 */
export function activeOptionId(
  fieldId: string,
  highlightedIndex: number,
  optionCount: number
): string | undefined {
  return highlightedIndex >= 0 && highlightedIndex < optionCount
    ? optionId(fieldId, highlightedIndex)
    : undefined;
}

/**
 * Whether the option at `flatIndex` carries the keyboard highlight. A negative index means the
 * option has no position in the rendered list and never matches, even when the highlight is
 * also -1 (nothing highlighted).
 */
export function isOptionHighlighted(flatIndex: number, highlightedIndex: number): boolean {
  return flatIndex >= 0 && flatIndex === highlightedIndex;
}
