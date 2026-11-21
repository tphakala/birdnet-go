/**
 * keyboard.ts
 *
 * Pure helpers for keyboard shortcuts: matching a key event against a binding, deciding
 * whether the event target owns the key (a text field, a slider, a button), and describing
 * a binding for `aria-keyshortcuts`, `title` text and the cheat sheet. Nothing here touches
 * the DOM beyond reading the event target, so the helpers are safe to call from any handler.
 */
import { t } from '$lib/i18n';

/** `KeyboardEvent.key` values the shortcut definitions use. Letters are written lowercase. */
export const KEYS = {
  arrowLeft: 'ArrowLeft',
  arrowRight: 'ArrowRight',
  space: ' ',
  questionMark: '?',
  comma: ',',
  period: '.',
} as const;

/** Named keys, which match Shift strictly. */
const NAMED_KEYS: ReadonlySet<string> = new Set([KEYS.arrowLeft, KEYS.arrowRight, KEYS.space]);

/** `KeyboardEvent.keyCode` the browsers report for a key press that an input method handles. */
export const IME_PROCESS_KEY_CODE = 229;

/** `KeyboardEvent.key` a key press handled by an input method reports. */
const IME_PROCESS_KEY = 'Process';

/** A key and an optional Shift requirement. Letters are lowercase. */
export interface ShortcutBinding {
  key: string;
  shift?: boolean;
}

/** How a binding is announced and shown. */
export interface ShortcutHint {
  /** `aria-keyshortcuts` value: the binding and its secondary binding, space separated. */
  aria: string;
  /** Display tokens of the primary binding, one per key cap. */
  keys: readonly string[];
  /** The display tokens joined for a `title` text. */
  display: string;
  /** The binding as spoken words, for assistive technology. */
  label: string;
}

/** Input types that do not take typed text. */
const NON_TEXT_INPUT_TYPES: ReadonlySet<string> = new Set([
  'checkbox',
  'radio',
  'range',
  'button',
  'submit',
  'reset',
  'image',
  'color',
  'file',
]);

const TEXT_ENTRY_SELECTOR =
  'textarea, select, [role="textbox"], [role="searchbox"], [role="combobox"], [role="spinbutton"]';
const CONTENT_EDITABLE_SELECTOR = '[contenteditable]';
const CONTENT_EDITABLE_OFF = 'false';
const ARROW_KEY_WIDGET_SELECTOR = [
  '[role="slider"]',
  '[role="radiogroup"]',
  '[role="listbox"]',
  '[role="menu"]',
  '[role="menubar"]',
  '[role="tablist"]',
  '[role="tree"]',
  '[role="grid"]',
  'input[type="range"]',
  'input[type="radio"]',
].join(', ');
const INTERACTIVE_SELECTOR = [
  'button',
  'a[href]',
  'input',
  'select',
  'textarea',
  'summary',
  '[contenteditable]',
  '[role="button"]',
  '[role="link"]',
  '[role="switch"]',
  '[role="checkbox"]',
  '[role="radio"]',
  '[role="slider"]',
  '[role="menuitem"]',
  '[role="menuitemcheckbox"]',
  '[role="menuitemradio"]',
  '[role="tab"]',
  '[role="option"]',
].join(', ');

/** Separator of the key tokens in a display string. */
const DISPLAY_JOINER = '+';
/** Separator of the keys in an `aria-keyshortcuts` modifier combination. */
const ARIA_JOINER = '+';
/** Separator of alternative bindings in an `aria-keyshortcuts` value. */
const ARIA_ALTERNATIVE_SEPARATOR = ' ';
const ARIA_SHIFT = 'Shift';
const ARIA_SPACE = 'Space';
const APPLE_SHIFT_GLYPH = '⇧';
const ARROW_LEFT_GLYPH = '←';
const ARROW_RIGHT_GLYPH = '→';
const APPLE_PLATFORM_PATTERN = /Mac|iPhone|iPad|iPod/;

/** A single cased character: a letter that has an upper and a lower case. */
function isLetterKey(key: string): boolean {
  return key.length === 1 && key.toLowerCase() !== key.toUpperCase();
}

/**
 * Whether a key event matches a binding. A letter matches its lowercase form and needs Shift
 * to equal the binding's (Caps Lock still works, Shift+C does not match `c`). Named keys
 * (arrows, Space) match Shift strictly. Punctuation ignores Shift, which is part of typing
 * the character on most layouts.
 */
export function matchesBinding(
  event: Pick<KeyboardEvent, 'key' | 'shiftKey'>,
  binding: ShortcutBinding
): boolean {
  const shiftWanted = Boolean(binding.shift);
  if (NAMED_KEYS.has(binding.key)) {
    return event.key === binding.key && event.shiftKey === shiftWanted;
  }
  if (isLetterKey(binding.key)) {
    return event.key.toLowerCase() === binding.key && event.shiftKey === shiftWanted;
  }
  return event.key === binding.key;
}

/** Whether Ctrl, Meta or Alt is held: those are browser, OS and AltGr keys, not page shortcuts. */
export function hasCommandModifier(
  event: Pick<KeyboardEvent, 'ctrlKey' | 'metaKey' | 'altKey'>
): boolean {
  return event.ctrlKey || event.metaKey || event.altKey;
}

/** Whether the key press belongs to an input method composition. */
export function isImeComposition(event: KeyboardEvent): boolean {
  return (
    event.isComposing || event.key === IME_PROCESS_KEY || event.keyCode === IME_PROCESS_KEY_CODE
  );
}

/** The target as an Element, or null for `window`, `document` and a missing target. */
function asElement(target: EventTarget | null): Element | null {
  return typeof Element !== 'undefined' && target instanceof Element ? target : null;
}

/**
 * Whether the target takes typed text: a text-like input, textarea, select, an editable
 * element or a text-entry ARIA role. Checkboxes, switches, radios, range inputs and buttons
 * do not. `contenteditable="false"` is not editable (the attribute is read because jsdom
 * does not reflect `isContentEditable`).
 */
export function isTextEntryTarget(target: EventTarget | null): boolean {
  const element = asElement(target);
  if (!element) return false;
  if (element instanceof HTMLInputElement) return !NON_TEXT_INPUT_TYPES.has(element.type);
  if (element.closest(TEXT_ENTRY_SELECTOR)) return true;
  const editable = element.closest(CONTENT_EDITABLE_SELECTOR);
  return editable !== null && editable.getAttribute('contenteditable') !== CONTENT_EDITABLE_OFF;
}

/** Whether the target uses the arrow keys itself (a slider, radio group, menu, list and so on). */
export function isArrowKeyWidget(target: EventTarget | null): boolean {
  return asElement(target)?.closest(ARROW_KEY_WIDGET_SELECTOR) != null;
}

/** Whether Space on the target activates or edits it (a button, link, switch, field and so on). */
export function isInteractiveTarget(target: EventTarget | null): boolean {
  return asElement(target)?.closest(INTERACTIVE_SELECTOR) != null;
}

/** Whether the platform is Apple's, where Shift is shown as a glyph. */
export function isApplePlatform(
  nav: Navigator | undefined = typeof navigator === 'undefined' ? undefined : navigator
): boolean {
  if (!nav) return false;
  return APPLE_PLATFORM_PATTERN.test(nav.platform || nav.userAgent);
}

/** The word shown and spoken for a key: a localized name, or the key upper-cased. */
function keyWord(key: string): string {
  switch (key) {
    case KEYS.arrowLeft:
      return t('shortcuts.keys.arrowLeft');
    case KEYS.arrowRight:
      return t('shortcuts.keys.arrowRight');
    case KEYS.space:
      return t('shortcuts.keys.space');
    case KEYS.comma:
      return t('shortcuts.keys.comma');
    case KEYS.period:
      return t('shortcuts.keys.period');
    case KEYS.questionMark:
      return t('shortcuts.keys.questionMark');
    default:
      return key.toUpperCase();
  }
}

/** The glyph or word on a key cap. Arrows and punctuation show their glyph, Space its word. */
function keyToken(key: string): string {
  switch (key) {
    case KEYS.arrowLeft:
      return ARROW_LEFT_GLYPH;
    case KEYS.arrowRight:
      return ARROW_RIGHT_GLYPH;
    case KEYS.space:
      return t('shortcuts.keys.space');
    case KEYS.comma:
    case KEYS.period:
    case KEYS.questionMark:
      return key;
    default:
      return key.toUpperCase();
  }
}

/** Key name in `aria-keyshortcuts`: arrows keep their `key` value, letters are upper case. */
function ariaKeyName(key: string): string {
  return key === KEYS.arrowLeft || key === KEYS.arrowRight ? key : key.toUpperCase();
}

/** The `aria-keyshortcuts` form of a binding: `Shift+ArrowLeft`, `C`, `Space`. */
function ariaForm(binding: ShortcutBinding): string {
  const name = binding.key === KEYS.space ? ARIA_SPACE : ariaKeyName(binding.key);
  return binding.shift ? `${ARIA_SHIFT}${ARIA_JOINER}${name}` : name;
}

/**
 * Describe a binding for `aria-keyshortcuts`, key caps, `title` text and screen readers.
 * `secondary` is a second binding for the same action: it is added to `aria` only.
 */
export function describeBinding(
  binding: ShortcutBinding,
  apple: boolean,
  secondary?: ShortcutBinding
): ShortcutHint {
  const shiftToken = apple ? APPLE_SHIFT_GLYPH : t('shortcuts.keys.shift');
  const keys = binding.shift ? [shiftToken, keyToken(binding.key)] : [keyToken(binding.key)];
  const label = binding.shift
    ? t('shortcuts.keys.combination', {
        first: t('shortcuts.keys.shift'),
        second: keyWord(binding.key),
      })
    : keyWord(binding.key);
  const aria = [binding, ...(secondary ? [secondary] : [])]
    .map(ariaForm)
    .join(ARIA_ALTERNATIVE_SEPARATOR);
  return { aria, keys, display: keys.join(DISPLAY_JOINER), label };
}

/**
 * A `title` text that names the key, such as `Play (P)`, or undefined without a hint. Every
 * control that shows a shortcut hint formats its title through this, so they read alike.
 */
export function shortcutTitle(
  label: string,
  hint: ShortcutHint | null | undefined
): string | undefined {
  return hint ? t('shortcuts.withKey', { label, keys: hint.display }) : undefined;
}
