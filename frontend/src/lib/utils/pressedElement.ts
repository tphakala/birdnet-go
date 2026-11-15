/**
 * Tracks the control a user last pressed, for dialogs that return focus to the element they
 * were opened from. Safari and iPadOS do not focus a clicked button, so document.activeElement
 * is body when a dialog opens from one; the pressed control is then the opener.
 *
 * The press listeners live at module level so they are registered once per page, however many
 * dialogs import this module.
 */

/** CSS selector for native controls that may be keyboard focusable */
export const NATIVE_INTERACTIVE_SELECTOR =
  'button, a[href], area[href], input, select, textarea, summary';

/** CSS selector for elements that may be keyboard focusable, narrowed by each caller to what Tab reaches */
export const TABBABLE_CANDIDATE_SELECTOR = `${NATIVE_INTERACTIVE_SELECTOR}, [tabindex]`;

// A native control, or a custom one built on role="button"
const PRESSABLE_ANCESTOR_SELECTOR = `${NATIVE_INTERACTIVE_SELECTOR}, [role="button"]`;

// Controls a press cannot activate: disabled, or inside an inert or disabled subtree
const UNPRESSABLE_SELECTOR = ':disabled, [inert], fieldset[disabled]';

// MouseEvent.button of a primary press: left mouse button, touch contact, pen tip
const PRIMARY_POINTER_BUTTON = 0;

// The focusable element of the last press, while focus has not moved since, no key was pressed
// and no press landed on plain content after it
let pressedElement: HTMLElement | SVGElement | null = null;

function isElement(node: unknown): node is HTMLElement | SVGElement {
  return node instanceof HTMLElement || node instanceof SVGElement;
}

/**
 * The control a press landed on, or null for plain content, a disabled control or a negative
 * tabindex wrapper
 */
function pressedControl(target: unknown): HTMLElement | SVGElement | null {
  if (!isElement(target)) return null;
  const nearest = target.closest<HTMLElement | SVGElement>(TABBABLE_CANDIDATE_SELECTOR);
  if (!nearest) return null;
  // A tabindex=-1 element (an icon inside a button) belongs to the control around it, if any:
  // a native one, or a custom role="button" that is itself in the tab order
  let candidate: HTMLElement | SVGElement | null = nearest;
  if (!nearest.matches(NATIVE_INTERACTIVE_SELECTOR) && nearest.tabIndex < 0) {
    const owner = nearest.closest<HTMLElement>(PRESSABLE_ANCESTOR_SELECTOR);
    candidate =
      owner && (owner.matches(NATIVE_INTERACTIVE_SELECTOR) || owner.tabIndex >= 0) ? owner : null;
  }
  return candidate && !candidate.closest(UNPRESSABLE_SELECTOR) ? candidate : null;
}

/** Whether the browser still counts the user's last gesture as recent; true where the API is missing */
function userActivationIsActive(): boolean {
  return !('userActivation' in navigator) || navigator.userActivation.isActive;
}

if (typeof document !== 'undefined') {
  document.addEventListener(
    'pointerdown',
    event => {
      pressedElement =
        event.button === PRIMARY_POINTER_BUTTON ? pressedControl(event.target) : null;
    },
    true
  );
  // A scroll that starts on a button sends pointerdown, then pointercancel, and no click
  document.addEventListener(
    'pointercancel',
    () => {
      pressedElement = null;
    },
    true
  );
  document.addEventListener(
    'focusin',
    () => {
      pressedElement = null;
    },
    true
  );
  // A key press renews user activation, which would otherwise let an old press stand as the
  // opener of a dialog that a script opens much later
  document.addEventListener(
    'keydown',
    () => {
      pressedElement = null;
    },
    true
  );
}

/**
 * The element to return focus to when a dialog opens: the focused element, or the
 * pressed one when focus is on body or nowhere and the press is still recent, else null.
 * Undefined when focus is inside the dialog itself, which keeps the earlier opener.
 */
export function pickOpener(
  dialog: HTMLElement | undefined
): HTMLElement | SVGElement | null | undefined {
  const active = document.activeElement;
  if (isElement(active) && dialog?.contains(active)) return undefined;
  const nothingFocused =
    !isElement(active) || active === document.body || active === document.documentElement;
  if (!nothingFocused) return active;
  if (!userActivationIsActive()) return null;
  return pressedElement?.isConnected && !dialog?.contains(pressedElement) ? pressedElement : null;
}
