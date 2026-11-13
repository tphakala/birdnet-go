/**
 * The System pages take their colors from theme tokens only.
 *
 * A hex literal or a Tailwind palette color has one value for both themes, so it can fail
 * contrast in one of them, and no other test would notice it coming back. This scan covers
 * the files already moved to tokens; add each System file here when it moves.
 *
 * Text colors must also reach 4.5:1 in the light theme. Scanned, line by line, in every file of
 * MIGRATED_FILES:
 * 1. a `#` followed by 3, 4, 6 or 8 hex digits (not an HTML entity such as `&#160;`);
 * 2. any Tailwind utility, behind any variants, whose name ends in a palette name and a 2 or
 *    3 digit shade, with or without an opacity suffix (text-slate-500, border-t-red-500/50,
 *    dark:hover:ring-offset-amber-400, inset-shadow-red-500);
 * 3. a `--color-<palette>-<shade>` variable (text-[var(--color-red-500)]);
 * 4. a status fill used as text (text-[var(--color-success|warning|error|info)]): the fills are
 *    for backgrounds, borders and dots, the text tokens are --text-success and its siblings;
 * 5. faded body text: text-[var(--color-base-content)]/NN, text-base-content/NN or a plain
 *    opacity-NN with NN under FADED_TEXT_MIN_PERCENT, which drops muted text below 4.5:1 (use
 *    text-muted). Variant forms such as disabled:opacity-50 pass: they style a disabled
 *    control, which WCAG exempts;
 * 6. a status fill reaching the text through a style: `style:color` that is not a text token, base
 *    content or textColorFor(...), and `color: var(--color-success|warning|error|info)`.
 *
 * Not scanned: white, black, transparent, colour functions (rgb(), hsl(), oklch()), CSS
 * named colours, and any file not in the list.
 */
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const srcDir = join(dirname(fileURLToPath(import.meta.url)), '..');

const MIGRATED_FILES = [
  'lib/desktop/features/system/components/SystemProcessTable.svelte',
  'lib/desktop/views/SystemHealth.svelte',
  'lib/desktop/features/system/components/DatabaseSqliteDetails.svelte',
  'lib/desktop/features/import-export/pages/ImportExportPage.svelte',
  'lib/desktop/features/import-export/components/ImportActivityCard.svelte',
];

/** Body text at or above this opacity (percent) still reaches 4.5:1 on the base surfaces. */
const FADED_TEXT_MIN_PERCENT = 70;

/** Every palette Tailwind ships; a test below reads the installed theme to keep this complete. */
const PALETTE = [
  'slate',
  'gray',
  'zinc',
  'neutral',
  'stone',
  'mauve',
  'olive',
  'mist',
  'taupe',
  'red',
  'orange',
  'amber',
  'yellow',
  'lime',
  'green',
  'emerald',
  'teal',
  'cyan',
  'sky',
  'blue',
  'indigo',
  'violet',
  'purple',
  'fuchsia',
  'pink',
  'rose',
];
const PALETTE_ALTERNATION = PALETTE.join('|');

const HEX_COLOR = /(?<![&\w])#(?:[0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{4}|[0-9a-fA-F]{3})\b/g;
// Any utility name (text-, border-t-, ring-offset-, from-, inset-shadow-, ...), optionally
// behind variants (dark:, hover:, data-[state=open]:), ending in a palette color and shade
const PALETTE_CLASS = new RegExp(
  `(?<![\\w-])(?:[\\w-]+:)*[a-z][a-z-]*-(?:${PALETTE_ALTERNATION})-\\d{2,3}\\b`,
  'g'
);
const PALETTE_VAR = new RegExp(`--color-(?:${PALETTE_ALTERNATION})-\\d{2,3}\\b`, 'g');
// text-[var(--color-success)] and its warning, error and info siblings, also behind variants
const STATUS_FILL_TEXT = /(?<![\w-])text-\[var\(--color-(?:success|warning|error|info)\)\]/g;
// A Svelte style:color directive must be a text token, base content or the textColorFor helper;
// a function result such as levelColor(...) hands the status fill to the text
const STYLE_COLOR_NOT_TOKEN =
  /style:color=(?:\{(?!textColorFor\()|["'](?!var\(--(?:text-|color-base-content)))/g;
// color: var(--color-success) in a style attribute (background-color does not match)
const STYLE_COLOR_FILL = /(?<![\w-])color:\s*var\(--color-(?:success|warning|error|info)\)/g;
// Unprefixed faded text: base-content with an opacity suffix, or a plain opacity utility (also as
// a Svelte class: directive). A variant prefix (disabled:, hover:) does not match.
const FADED_BASE_CONTENT =
  /(?<![\w:-])text-(?:\[var\(--color-base-content\)\]|base-content)\/(\d{1,3})(?![\d%])/g;
const FADED_OPACITY = /(?:(?<![\w:-])|(?<=\bclass:))opacity-(\d{1,3})(?![\d%])/g;

/** Colors written outside the tokens, with their line numbers. */
function findRawColors(source: string): string[] {
  const hits: string[] = [];
  source.split('\n').forEach((line, index) => {
    for (const match of line.matchAll(HEX_COLOR)) hits.push(`${index + 1}: ${match[0]}`);
    for (const match of line.matchAll(PALETTE_CLASS)) hits.push(`${index + 1}: ${match[0]}`);
    for (const match of line.matchAll(PALETTE_VAR)) hits.push(`${index + 1}: ${match[0]}`);
    for (const match of line.matchAll(STATUS_FILL_TEXT)) hits.push(`${index + 1}: ${match[0]}`);
    for (const match of line.matchAll(STYLE_COLOR_NOT_TOKEN))
      hits.push(`${index + 1}: ${match[0]}`);
    for (const match of line.matchAll(STYLE_COLOR_FILL)) hits.push(`${index + 1}: ${match[0]}`);
    for (const match of [...line.matchAll(FADED_BASE_CONTENT), ...line.matchAll(FADED_OPACITY)]) {
      const percent = Number(match[1]);
      if (percent > 0 && percent < FADED_TEXT_MIN_PERCENT) hits.push(`${index + 1}: ${match[0]}`);
    }
  });
  return hits;
}

describe('System page colors', () => {
  it.each([
    ['a hex color', 'style="color: #f59e0b"'],
    ['a short hex color', 'style="color: #fa0"'],
    ['a 4 digit hex color', 'style="color: #fa0f"'],
    ['an 8 digit hex color', 'style="color: #f59e0bcc"'],
    ['a palette class', 'class="px-2 bg-slate-500/15"'],
    ['a palette class behind a variant', 'class="dark:hover:text-amber-400"'],
    ['a palette class behind a bracket variant', 'class="[&>svg]:text-red-500"'],
    ['a border side', 'class="border-t-red-500"'],
    ['a border axis with opacity', 'class="border-x-red-500/50"'],
    ['a ring offset', 'class="ring-offset-amber-400"'],
    ['an outline', 'class="outline-sky-500"'],
    ['a divide', 'class="divide-gray-200"'],
    ['a gradient from', 'class="from-sky-500"'],
    ['a gradient via', 'class="via-rose-300"'],
    ['a gradient to', 'class="to-teal-700"'],
    ['a shadow color', 'class="shadow-slate-900/20"'],
    ['an accent', 'class="accent-pink-500"'],
    ['a decoration', 'class="decoration-indigo-400"'],
    ['a placeholder', 'class="placeholder-zinc-400"'],
    ['a caret', 'class="caret-lime-600"'],
    ['an inset shadow', 'class="inset-shadow-red-500"'],
    ['a palette Tailwind added later', 'class="bg-mauve-500"'],
    ['a palette variable in an arbitrary value', 'class="text-[var(--color-red-500)]"'],
    ['a palette variable in a style', 'style="color: var(--color-amber-400)"'],
    ['a style color from a fill helper', 'style:color={levelColor(group.level)}'],
    ['a style color set to a fill', 'style:color="var(--color-success)"'],
    ['a style attribute color set to a fill', 'style="color: var(--color-warning)"'],
    ['a success fill used as text', 'class="text-[var(--color-success)]"'],
    ['a warning fill used as text', 'class="text-xs text-[var(--color-warning)]"'],
    ['an error fill used as text behind a variant', 'class="hover:text-[var(--color-error)]"'],
    ['an info fill used as text with opacity', 'class="text-[var(--color-info)]/80"'],
    ['base content faded by an opacity suffix', 'class="text-[var(--color-base-content)]/60"'],
    ['base content theme utility faded', 'class="text-base-content/60"'],
    ['faded text by opacity utility', 'class="text-xs opacity-60 truncate"'],
    ['a faded Svelte class directive', 'class:opacity-40={count === 0}'],
  ])('flags %s', (_name, source) => {
    expect(findRawColors(source)).toHaveLength(1);
  });

  it.each([
    ['a token', 'class="text-[var(--text-warning)] bg-[var(--surface-100)]"'],
    ['a theme utility', 'class="text-muted bg-primary border-base-300"'],
    ['a size utility', 'class="text-xs border-2 ring-1"'],
    [
      'size utilities of the wider prefixes',
      'class="ring-offset-2 border-t-2 outline-2 divide-y shadow-sm"',
    ],
    ['gradient positions', 'class="from-10% via-30% to-90%"'],
    ['a theme variable', 'style="color: var(--color-base-300)"'],
    ['an HTML entity', 'title="a&#160;b"'],
    [
      'a status fill as a background',
      'class="bg-[var(--color-success)]/10 border-[var(--color-error)]"',
    ],
    ['a style color from the text token helper', 'style:color={textColorFor(levelColor(level))}'],
    ['a style color set to a text token', 'style:color="var(--text-error)"'],
    [
      'a fill in a style background',
      'style:background="color-mix(in srgb, {fill} 10%, transparent)"',
    ],
    ['a fill in a background-color style', 'style="background-color: var(--color-success)"'],
    ['a status text token', 'class="text-[var(--text-success)] text-[var(--text-warning)]"'],
    ['text at the faded limit', 'class="text-[var(--color-base-content)]/70 opacity-70"'],
    ['a variant opacity', 'class="disabled:opacity-50 hover:opacity-80 opacity-100"'],
    ['a hidden element', 'class="opacity-0"'],
  ])('passes %s', (_name, source) => {
    expect(findRawColors(source)).toEqual([]);
  });

  it('PALETTE lists every palette Tailwind ships', () => {
    const theme = readFileSync(
      join(srcDir, '..', 'node_modules', 'tailwindcss', 'theme.css'),
      'utf8'
    );
    const shipped = new Set(
      [...theme.matchAll(/--color-([a-z]+)-\d{2,3}:/g)].map(match => match[1])
    );
    expect(shipped.size).toBeGreaterThan(0);
    expect([...shipped].filter(name => !PALETTE.includes(name))).toEqual([]);
  });

  it.each(MIGRATED_FILES)('%s uses theme tokens only', file => {
    // eslint-disable-next-line security/detect-non-literal-fs-filename -- paths come from the fixed list above
    expect(findRawColors(readFileSync(join(srcDir, file), 'utf8'))).toEqual([]);
  });
});
