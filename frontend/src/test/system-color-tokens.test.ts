/**
 * The System pages take their colors from theme tokens only.
 *
 * A hex literal or a Tailwind palette class (text-slate-500, bg-amber-400/15) has one value
 * for both themes, so it can fail contrast in one of them, and no other test would notice it
 * coming back. This scan covers the files already moved to tokens; add each System file
 * here when it moves.
 */
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const srcDir = join(dirname(fileURLToPath(import.meta.url)), '..');

const MIGRATED_FILES = ['lib/desktop/features/system/components/SystemProcessTable.svelte'];

const PALETTE =
  'slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose';

const HEX_COLOR = /#[0-9a-fA-F]{3,8}\b/g;
// A utility prefix, optionally behind variants (dark:, hover:), then a palette color and shade
const PALETTE_CLASS = new RegExp(
  `(?<![\\w-])(?:[\\w-]+:)*(?:text|bg|border|ring|fill|stroke)-(?:${PALETTE})-\\d{2,3}\\b`,
  'g'
);

/** Colors written outside the tokens, with their line numbers. */
function findRawColors(source: string): string[] {
  const hits: string[] = [];
  source.split('\n').forEach((line, index) => {
    for (const match of line.matchAll(HEX_COLOR)) hits.push(`${index + 1}: ${match[0]}`);
    for (const match of line.matchAll(PALETTE_CLASS)) hits.push(`${index + 1}: ${match[0]}`);
  });
  return hits;
}

describe('System page colors', () => {
  it.each([
    ['a hex color', 'style="color: #f59e0b"'],
    ['a palette class', 'class="px-2 bg-slate-500/15"'],
    ['a palette class behind a variant', 'class="dark:hover:text-amber-400"'],
  ])('flags %s', (_name, source) => {
    expect(findRawColors(source)).toHaveLength(1);
  });

  it.each([
    ['a token', 'class="text-[var(--text-warning)] bg-[var(--surface-100)]"'],
    ['a theme utility', 'class="text-muted bg-primary border-base-300"'],
    ['a size utility', 'class="text-xs border-2 ring-1"'],
  ])('passes %s', (_name, source) => {
    expect(findRawColors(source)).toEqual([]);
  });

  it.each(MIGRATED_FILES)('%s uses theme tokens only', file => {
    // eslint-disable-next-line security/detect-non-literal-fs-filename -- paths come from the fixed list above
    expect(findRawColors(readFileSync(join(srcDir, file), 'utf8'))).toEqual([]);
  });
});
