/**
 * Status fills never colour text. App-wide, in every non-test .svelte, .ts and .css file under src/.
 *
 * --color-success|warning|error|info are fills for backgrounds, borders and dots; on the base
 * surfaces in the light theme they fail AA as text. Text and icons use --text-success and its
 * siblings (text-[var(--text-error)]), or the plain utilities text-error and so on, which map to
 * the same tokens. Flagged, over the whole comment-free text so a wrapped CSS value is seen:
 * 1. a fill or its hover fill as an arbitrary text colour, behind any variant, with any opacity;
 * 2. a bare theme utility behind a variant (hover:text-error): Tailwind generates the fill for it;
 * 3. a bare theme utility with an opacity suffix or the important modifier (text-error/80,
 *    !text-error), which Tailwind also resolves to the fill;
 * 4. a `color` declaration whose value holds a fill, plain, with a fallback or in color-mix();
 * 5. `style:color` with a quoted value holding a fill;
 * 6. `style:color` with an expression that is not textColorFor(...).
 *
 * Deliberate fill uses the rules do not reach, so no exclusion is needed for them:
 * StatusPill.svelte (fill variables for tint, border and dot; its text is --text-${variant}),
 * d3/utils/theme.ts (chart series), StorageCard.svelte (bar colour through style:background),
 * SystemHealth.svelte (fills routed through textColorFor), noveltyCategoryColorVar on
 * style:border-left-color, and the -content tokens.
 *
 * EXCLUSIONS lists the only exceptions. An entry is a file plus the hit text and consumes one hit,
 * so a second identical use in the same file fails; an entry whose hit is gone fails as stale and
 * must be deleted.
 */
import { describe, it, expect } from 'vitest';
import { readdirSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join, sep } from 'node:path';
import { stripComments } from './source-comments';

const srcDir = join(dirname(fileURLToPath(import.meta.url)), '..');

const STATUS = '(?:success|warning|error|info)';

const RULES: RegExp[] = [
  new RegExp(String.raw`(?<![\w-])text-\[var\(--color-${STATUS}(?:-hover)?\)\]`, 'g'),
  new RegExp(
    String.raw`(?<![\w:-])(?!class:!?text-${STATUS}(?![\w/!-]))(?:[^\s:"'\`{}]+:)+!?text-${STATUS}(?![\w-])`,
    'g'
  ),
  new RegExp(String.raw`(?<![\w:-])(?:!text-${STATUS}(?![\w-])|text-${STATUS}(?:\/\d+|!))`, 'g'),
  new RegExp(
    String.raw`(?<![\w-])color:\s*[^;{}"'\`]*?var\(--color-${STATUS}(?:-hover)?\s*[,)][^;{}"'\`]*`,
    'g'
  ),
  new RegExp(
    String.raw`style:color=(["'])(?:(?!\1).)*?var\(--color-${STATUS}(?:-hover)?\s*[,)](?:(?!\1).)*\1`,
    'g'
  ),
  /style:color=\{(?!textColorFor\()[^}]*\}?/g,
];

interface Hit {
  file: string;
  line: number;
  hit: string;
}

interface Exclusion {
  file: string;
  hit: string;
  reason: string;
}

const EXCLUSIONS: Exclusion[] = [
  {
    file: 'lib/desktop/features/dashboard/components/PlayOverlay.svelte',
    hit: 'color: color-mix(in srgb, var(--color-error) 40%, white)',
    reason:
      'dark-only override that reaches 6.20:1 on its 30% error tint over base-300, where --text-error reaches 3.85:1; the base rule already uses the token',
  },
];

/** Status fills used as text in one source, with whitespace in each hit collapsed. */
function findStatusFillText(source: string): { line: number; hit: string }[] {
  const text = stripComments(source);
  return RULES.flatMap(rule =>
    [...text.matchAll(rule)].map(match => ({
      line: text.slice(0, match.index).split('\n').length,
      hit: match[0].replace(/\s+/g, ' ').trim(),
    }))
  );
}

/** Each exclusion consumes one hit of its file and text; what is left over is an offender. */
function applyExclusions(
  hits: Hit[],
  exclusions: Exclusion[]
): { offenders: Hit[]; stale: Exclusion[] } {
  const unused = [...exclusions];
  const offenders = hits.filter(hit => {
    const index = unused.findIndex(entry => entry.file === hit.file && entry.hit === hit.hit);
    if (index === -1) return true;
    unused.splice(index, 1);
    return false;
  });
  return { offenders, stale: unused };
}

function isScanned(file: string): boolean {
  return (
    /\.(?:svelte|ts|css)$/.test(file) &&
    !/\.(?:test|spec)\.(?:ts|js|svelte)$/.test(file) &&
    !file.startsWith('test/') &&
    !file.includes('__tests__/')
  );
}

function scannedFiles(): string[] {
  return readdirSync(srcDir, { recursive: true, encoding: 'utf8' })
    .map(file => file.split(sep).join('/'))
    .filter(isScanned);
}

describe('status-text-tokens', () => {
  it.each([
    ['the fill class', 'class="text-[var(--color-success)]"'],
    ['the fill behind hover:', 'class="hover:text-[var(--color-error)]"'],
    ['the fill as a class: directive', 'class:text-[var(--color-info)]={x}'],
    ['the fill with an opacity', 'class="text-[var(--color-warning)]/80"'],
    ['the hover fill', 'class="hover:text-[var(--color-error-hover)]"'],
    ['bare hover:text-error', 'class="hover:text-error"'],
    ['stacked dark:hover:text-success', 'class="dark:hover:text-success"'],
    ['a class: directive behind a variant', 'class:hover:text-error={x}'],
    ['text-warning/80', 'class="text-warning/80"'],
    ['!text-error', 'class="!text-error"'],
    ['text-error!', 'class="text-error!"'],
    ['a color declaration', 'color: var(--color-success);'],
    ['a color declaration with a fallback', 'color: var(--color-error, #f00);'],
    ['a color-mix declaration', 'color: color-mix(in srgb, var(--color-error) 40%, white);'],
    [
      'a color-mix declaration over several lines',
      'color: color-mix(\n    in srgb,\n    var(--color-error) 40%,\n    white\n  );',
    ],
    ['an inline style', 'style="color: var(--color-warning)"'],
    ['an arbitrary property', 'class="[color:var(--color-error)]"'],
    ['text-[color:var(...)]', 'class="text-[color:var(--color-info)]"'],
    ['a quoted style:color', 'style:color="var(--color-success)"'],
    [
      'a quoted style:color with color-mix',
      'style:color="color-mix(in srgb, var(--color-info) 80%, white)"',
    ],
    ['a style:color expression', 'style:color={levelColor(x)}'],
  ])('flags %s', (_name, source) => {
    expect(findStatusFillText(source)).toHaveLength(1);
  });

  it.each([
    ['the plain utilities', 'class="text-error text-success text-warning text-info"'],
    ['a plain class: directive', 'class:text-error={bad}'],
    [
      'the token with and without a variant',
      'class="text-[var(--text-error)] hover:text-[var(--text-success)]"',
    ],
    [
      'fills as backgrounds and borders',
      'class="bg-[var(--color-error)]/10 border-[var(--color-error)] hover:bg-[var(--color-error-hover)]"',
    ],
    ['background-color', 'background-color: var(--color-success);'],
    ['border-color', 'border-color: var(--color-error);'],
    ['border-left-color', 'border-left-color: var(--color-info);'],
    ['accent-color', 'accent-color: var(--color-error);'],
    ['a ring colour', '--tw-ring-color: var(--color-error);'],
    ['a -content token declaration', 'color: var(--color-error-content);'],
    [
      'the -content tokens as classes',
      'class="text-[var(--color-error-content)] hover:text-info-content text-error-content/80"',
    ],
    ['textColorFor', 'style:color={textColorFor(fill)}'],
    ['base content', 'style:color="var(--color-base-content)"'],
    [
      'base content in color-mix',
      'style:color="color-mix(in srgb, var(--color-base-content) 50%, transparent)"',
    ],
    ['a fill on a border', 'style:border-left-color={noveltyCategoryColorVar(c)}'],
    ['an HTML comment', '<!-- was text-[var(--color-error)] -->'],
    ['a block comment', '/* color: var(--color-error) */'],
    ['a line comment', '// hover:text-error'],
    [
      'a token rule followed by a fill background',
      'color: var(--text-error);\n  background-color: color-mix(in srgb, var(--color-error) 10%, var(--color-base-100));',
    ],
    ['a string literal returning a fill', "return 'var(--color-success)';"],
  ])('passes %s', (_name, source) => {
    expect(findStatusFillText(source)).toEqual([]);
  });

  const entry = { file: 'a.css', hit: 'color: var(--color-error)', reason: 'test' };
  const hit = { file: 'a.css', line: 1, hit: entry.hit };
  it.each([
    ['a hit with an entry is allowed', [hit], [entry], 0, 0],
    ['a second identical hit is an offender', [hit, { ...hit, line: 9 }], [entry], 1, 0],
    ['an entry without a hit is stale', [], [entry], 0, 1],
    ['the same text in another file is an offender', [{ ...hit, file: 'b.css' }], [entry], 1, 1],
  ])('matches exclusions one hit at a time: %s', (_name, hits, exclusions, offenders, stale) => {
    const result = applyExclusions(hits, exclusions);
    expect([result.offenders.length, result.stale.length]).toEqual([offenders, stale]);
  });

  it('scans the app sources', () => {
    expect(scannedFiles().length).toBeGreaterThan(400);
  });

  it('colours no text with a status fill outside the listed exceptions', () => {
    const hits = scannedFiles().flatMap(file =>
      // eslint-disable-next-line security/detect-non-literal-fs-filename -- paths come from listing src/
      findStatusFillText(readFileSync(join(srcDir, file), 'utf8')).map(found => ({
        file,
        ...found,
      }))
    );
    const { offenders, stale } = applyExclusions(hits, EXCLUSIONS);
    expect({
      offenders: offenders.map(({ file, line, hit }) => `${file}:${line}: ${hit}`),
      stale: stale.map(({ file, hit }) => `${file}: ${hit}`),
    }).toEqual({ offenders: [], stale: [] });
  });
});
