/**
 * species-display.css is imported app-wide and is unlayered, so any bare table element
 * selector in it restyles every table in the app and beats Tailwind utilities. Its table
 * rules must be scoped to the detections list table. jsdom has no layout, so this test
 * reads the stylesheet and the component source.
 */
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const srcDir = join(dirname(fileURLToPath(import.meta.url)), '..');
const css = readFileSync(join(srcDir, 'lib', 'styles', 'species-display.css'), 'utf8');

const TABLE_ELEMENT = /(^|[\s>+~,(])(table|thead|tbody|tr|td|th)(?![\w-])/;
const SCOPES = ['.detections-table', '.detection-header-list'];

/** Returns every selector in the stylesheet, with comments and declaration blocks removed. */
function selectors(source: string): string[] {
  const stripped = source.replace(/\/\*[\s\S]*?\*\//g, '');
  const result: string[] = [];
  // A prelude is the text before a `{` that is not an at-rule.
  for (const match of stripped.matchAll(/(?:^|[};{])\s*([^{};@]+)\{/g)) {
    for (const selector of match[1].split(',')) {
      const trimmed = selector.trim();
      if (trimmed) result.push(trimmed);
    }
  }
  return result;
}

describe('species-display.css', () => {
  it('has no unscoped table element selectors', () => {
    const unscoped = selectors(css).filter(
      selector => TABLE_ELEMENT.test(selector) && !SCOPES.some(scope => selector.includes(scope))
    );
    expect(unscoped).toEqual([]);
  });

  it('DetectionsList puts the detections-table class on its table', () => {
    const list = readFileSync(
      join(
        srcDir,
        'lib',
        'desktop',
        'features',
        'detections',
        'components',
        'DetectionsList.svelte'
      ),
      'utf8'
    );
    expect(list).toMatch(/<table class="[^"]*\bdetections-table\b[^"]*"/);
  });
});
