/**
 * Form state border colours must survive hover.
 *
 * The base hover rule (.input:hover:not(:disabled, :focus)) has specificity
 * (0,3,0), higher than a plain state class such as .input-error (0,1,0), so
 * without a matching hover rule an invalid field loses its coloured border as
 * soon as the pointer is over it. jsdom does not apply the :hover cascade, so
 * this test reads the stylesheet source; a browser check stays manual.
 */
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const css = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), '..', 'styles', 'tailwind.css'),
  'utf8'
);

const HOVER_GUARD = ':hover:not(:disabled, :focus)';

/** Returns the declaration block of the first rule with exactly this selector, and its offset. */
function findRule(selector: string): { body: string; index: number } | null {
  const marker = `\n  ${selector} {`;
  const index = css.indexOf(marker);
  if (index === -1) return null;
  const start = index + marker.length;
  return { body: css.slice(start, css.indexOf('}', start)), index };
}

const cases = [
  ['input', 'error'],
  ['input', 'success'],
  ['input', 'warning'],
  ['select', 'error'],
  ['select', 'success'],
  ['textarea', 'error'],
  ['textarea', 'success'],
] as const;

describe('form state borders on hover', () => {
  it.each(cases)('keeps the %s border of a .%s field on hover', (base, state) => {
    const hover = findRule(`.${base}-${state}${HOVER_GUARD}`);
    expect(hover, `missing .${base}-${state}${HOVER_GUARD} rule`).not.toBeNull();
    expect(hover?.body).toContain(`border-color: var(--color-${state});`);

    const baseHover = findRule(`.${base}${HOVER_GUARD}`);
    expect(baseHover, `missing base hover rule for .${base}`).not.toBeNull();
    expect(hover?.index).toBeGreaterThan(baseHover?.index ?? Infinity);
  });

  it('leaves the base hover rule unlowered so .input-ghost keeps its hover border', () => {
    expect(findRule(`.input${HOVER_GUARD}`)?.body).toContain('border-color: var(--border-200)');
  });
});
