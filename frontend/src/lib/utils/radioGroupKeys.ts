import type { Action } from 'svelte/action';

const RADIO_SELECTOR = '[role="radio"]';
const UNAVAILABLE_SELECTOR = '[aria-disabled="true"], :disabled';
/** A radio that is, or sits inside, a hidden or inert element cannot take focus. */
const UNFOCUSABLE_SELECTOR = '[hidden], [inert]';

/** Arrow keys mapped to the step they take through the group. */
const KEY_STEP: ReadonlyMap<string, number> = new Map([
  ['ArrowRight', 1],
  ['ArrowDown', 1],
  ['ArrowLeft', -1],
  ['ArrowUp', -1],
]);

/**
 * Svelte action for a `role="radiogroup"` element whose `role="radio"` buttons
 * do not implement the keyboard pattern themselves.
 *
 * Arrow Right/Down and Left/Up select the next or previous radio (wrapping at
 * both ends. The action skips radios that are `aria-disabled="true"`, natively
 * `:disabled`, hidden or inert, and any radio that does not take focus. It calls
 * `focus()` on the candidate first and then `click()`, so focus moves before the
 * click handler updates `aria-checked` and `tabindex`. Selection goes through
 * `click()`, so a consumer that suppresses selection in its click handler must
 * also mark the radio `aria-disabled`, or the focus still moves. Arrows pressed
 * with Alt, Ctrl or Meta are left to the browser. When no other radio can be
 * selected the key does nothing.
 *
 * The consumer owns the roving tabindex: the checked radio has `tabindex="0"`
 * and the others `tabindex="-1"`, so the group is a single Tab stop.
 *
 * `RadioCardGroup` has its own implementation of this pattern; use this action
 * for groups of plain buttons.
 */
export const radioGroupKeys: Action<HTMLElement> = node => {
  function onKeydown(event: KeyboardEvent): void {
    const step = KEY_STEP.get(event.key);
    if (step === undefined || event.altKey || event.ctrlKey || event.metaKey) return;

    const current = event.target instanceof Element ? event.target.closest(RADIO_SELECTOR) : null;
    if (!current || !node.contains(current)) return;

    const radios = Array.from(node.querySelectorAll<HTMLElement>(RADIO_SELECTOR));
    const start = radios.findIndex(radio => radio === current);
    if (start < 0) return;

    event.preventDefault();
    for (let offset = 1; offset < radios.length; offset++) {
      // at() counts a negative index from the end, so the remainder wraps in both directions.
      const candidate = radios.at((start + step * offset) % radios.length);
      if (!candidate || candidate.matches(UNAVAILABLE_SELECTOR)) continue;
      if (candidate.closest(UNFOCUSABLE_SELECTOR)) continue;
      // Focus first: a radio hidden by CSS ignores focus(), and selecting it would leave focus behind.
      candidate.focus();
      // Compare with activeElement: :focus also needs system focus, which an unfocused window lacks.
      if (candidate.ownerDocument.activeElement !== candidate) continue;
      candidate.click();
      return;
    }
  }

  node.addEventListener('keydown', onKeydown);
  return {
    destroy() {
      node.removeEventListener('keydown', onKeydown);
    },
  };
};
