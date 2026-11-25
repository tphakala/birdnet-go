/** Neutral text colour, also the fallback for a fill that has no text token. */
const NEUTRAL_TEXT_COLOR = 'var(--color-base-content)';

/** The text token for each status fill; the fills are for backgrounds, borders and dots. */
const TEXT_TOKEN_FOR_FILL: ReadonlyMap<string, string> = new Map([
  ['var(--color-error)', 'var(--text-error)'],
  ['var(--color-warning)', 'var(--text-warning)'],
  ['var(--color-success)', 'var(--text-success)'],
  ['var(--color-info)', 'var(--text-info)'],
]);

/**
 * Returns the text token that matches a status fill, so badge text keeps 4.5:1 while its tinted
 * background keeps the fill. A fill without a text token falls back to the neutral text colour.
 */
export function textColorFor(fill: string): string {
  return TEXT_TOKEN_FOR_FILL.get(fill) ?? NEUTRAL_TEXT_COLOR;
}
