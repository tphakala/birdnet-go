/**
 * The confidence circle paints its ring from the status fill and its number from the status text
 * token. The two are split by --confidence-ring: if the ring fell back to `currentcolor`, it would
 * take the text token and lose contrast against its track, and no other test would notice.
 */
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { stripComments } from './source-comments';

const srcDir = join(dirname(fileURLToPath(import.meta.url)), '..');
const read = (file: string) =>
  // eslint-disable-next-line security/detect-non-literal-fs-filename -- fixed paths under src/
  stripComments(readFileSync(join(srcDir, file), 'utf8'));

const customCss = read('styles/custom.css');
const levels = [
  { level: 'high', status: 'success' },
  { level: 'medium', status: 'warning' },
  { level: 'low', status: 'error' },
];

/** The declarations of the first rule with this exact selector. */
function ruleBody(css: string, selector: string): string {
  const start = css.indexOf(`${selector} {`);
  expect(start, `${selector} rule`).toBeGreaterThanOrEqual(0);
  return css.slice(start, css.indexOf('}', start));
}

describe('confidence circle ring colour', () => {
  it.each(levels)(
    'paints the $level ring with the fill and the number with the token',
    ({ level, status }) => {
      const body = ruleBody(customCss, `.confidence-${level}`);
      expect(body).toContain(`--confidence-ring: var(--color-${status});`);
      expect(body).toContain(`color: var(--text-${status});`);
    }
  );

  it.each(levels)('keeps the $level number on the token in the dark theme', ({ level }) => {
    const body = ruleBody(customCss, `[data-theme='dark'] .confidence-${level}`);
    expect(body).toContain('--confidence-ring: color-mix(');
    expect(body).not.toMatch(/(?<![\w-])color:/);
  });

  it.each([
    ['styles/custom.css', customCss],
    ['ConfidenceCircle.svelte', read('lib/desktop/components/data/ConfidenceCircle.svelte')],
  ])('reads the ring from --confidence-ring in %s', (_file, source) => {
    expect(source).toContain('conic-gradient(var(--confidence-ring, currentcolor) var(--progress)');
  });
});
