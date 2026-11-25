/**
 * Status and primary fills never colour text. App-wide, in every non-test .svelte, .ts and .css
 * file under src/.
 *
 * --color-success|warning|error|info|primary are fills for backgrounds, borders and dots; on the
 * base surfaces they fail AA as text in several schemes. Text and icons use --text-success and its
 * siblings, or --text-primary (text-[var(--text-error)], text-[var(--text-primary)]), or the plain
 * utilities text-error, text-primary and so on, which map to the same tokens. The -hover, -active
 * and -focus fills count as the fill. Flagged, over the whole comment-free text so a wrapped CSS
 * value is seen:
 * 1. a fill or its state fill as an arbitrary text colour, in brackets or in the parenthesis
 *    shorthand (text-(--color-error), text-(color:--color-primary)), behind any variant, with any
 *    opacity;
 * 2. a bare theme utility behind a variant (hover:text-error, hover:text-primary): Tailwind
 *    generates the fill for it;
 * 3. a bare theme utility with an opacity suffix or the important modifier (text-error/80,
 *    text-primary/90, text-primary/(--x), !text-error), or a bare state utility (text-primary-hover),
 *    which Tailwind also resolves to the fill;
 * 4. a `color` declaration whose value holds a fill, plain, with a fallback or in color-mix();
 * 5. `style:color` with a quoted value holding a fill;
 * 6. `style:color` with an expression that is not textColorFor(...) or
 *    noveltyCategoryTextColor(...), which routes status fills through textColorFor.
 *
 * Deliberate fill uses the rules do not reach, so no exclusion is needed for them:
 * StatusPill.svelte (holds no fill variables: .badge-status-* sets --status-fill and the text
 * token, the pill draws its border and dot from --status-fill),
 * d3/utils/theme.ts and Sparkline.svelte (chart series colours), PlayOverlay.svelte (progress
 * ring stroke), focus rings and outlines, borders, StorageCard.svelte (bar colour through
 * style:background), SystemHealth.svelte (fills routed through textColorFor),
 * noveltyCategoryColorVar on style:border-left-color, and the -content tokens.
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

const FILL = '(?:success|warning|error|info|primary)';

/** State fills that count as the fill itself. */
const FILL_STATE = '(?:-hover|-active|-focus)?';

/** Fewer scanned files than this means the directory walk broke, not that the app shrank. */
const MIN_SCANNED_FILES = 400;

const RULES: RegExp[] = [
  new RegExp(
    String.raw`(?<![\w-])text-\[(?!color:)[^\]]*?var\(--color-${FILL}${FILL_STATE}\s*[,)][^\]]*\]`,
    'g'
  ),
  new RegExp(String.raw`(?<![\w-])text-\((?:color:)?--color-${FILL}${FILL_STATE}\)`, 'g'),
  new RegExp(
    String.raw`(?<![\w:-])(?!class:!?text-${FILL}(?![\w/!-]))(?:[^\s:"'\`{}]+:)+!?text-${FILL}${FILL_STATE}(?![\w-])`,
    'g'
  ),
  new RegExp(
    String.raw`(?<![\w:-])!text-${FILL}${FILL_STATE}(?![\w-])|(?<![\w:!-])text-${FILL}${FILL_STATE}(?:\/(?:\d+|\[|\()|!)|(?<![\w:!-])text-${FILL}(?:-hover|-active|-focus)(?![\w/!-])|(?<=class:)!text-${FILL}${FILL_STATE}(?![\w/!-])`,
    'g'
  ),
  new RegExp(
    String.raw`(?<![\w-])color:\s*[^;{}"'\`]*?var\(--color-${FILL}${FILL_STATE}\s*[,)][^;{}"'\`]*`,
    'g'
  ),
  new RegExp(
    String.raw`style:color=(["'])(?:(?!\1).)*?var\(--color-${FILL}${FILL_STATE}\s*[,)](?:(?!\1).)*\1`,
    'g'
  ),
  /style:color=\{(?!(?:textColorFor|noveltyCategoryTextColor)\()[^}]*\}?/g,
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

/** Status and primary fills used as text in one source, with whitespace in each hit collapsed. */
function findFillText(source: string): { line: number; hit: string }[] {
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

describe('text-tokens', () => {
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
    ['an arbitrary opacity', 'class="text-error/[0.8]"'],
    ['a class: directive with the important modifier', 'class:!text-error={x}'],
    ['a class: directive with the important modifier and an opacity', 'class:!text-error/80={x}'],
    ['a fill with a fallback as the text colour', 'class="text-[var(--color-error,#f00)]"'],
    [
      'a fill inside color-mix as the text colour',
      'class="text-[color-mix(in_srgb,var(--color-error)_80%,white)]"',
    ],
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
    ['the parenthesis shorthand', 'class="text-(--color-error)"'],
    ['the parenthesis shorthand with a type hint', 'class="text-(color:--color-warning)"'],
    ['the parenthesis shorthand behind a variant', 'class="hover:text-(--color-success-hover)/80"'],
    ['a variable opacity', 'class="text-error/(--alpha)"'],
    ['a quoted style:color', 'style:color="var(--color-success)"'],
    [
      'a quoted style:color with color-mix',
      'style:color="color-mix(in srgb, var(--color-info) 80%, white)"',
    ],
    ['a style:color expression', 'style:color={levelColor(x)}'],
    ['the primary fill class', 'class="text-[var(--color-primary)]"'],
    ['the primary active fill', 'class="text-[var(--color-primary-active)]"'],
    ['the primary focus fill behind hover:', 'class="hover:text-[var(--color-primary-focus)]"'],
    ['a bare primary state utility', 'class="text-primary-hover"'],
    ['a bare primary state utility behind a variant', 'class="hover:text-primary-active"'],
    ['a bare primary state utility with an opacity', 'class="text-primary-hover/80"'],
  ])('flags %s', (_name, source) => {
    expect(findFillText(source)).toHaveLength(1);
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
    ['noveltyCategoryTextColor', 'style:color={noveltyCategoryTextColor(c)}'],
    [
      'the token in the parenthesis shorthand',
      'class="text-(--text-error) text-(color:--text-info)"',
    ],
    ['a -content token in the parenthesis shorthand', 'class="text-(--color-error-content)"'],
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
    [
      'plain text-primary and the primary -content forms',
      'class="text-primary text-primary-content hover:text-primary-content text-primary-content/80 text-[var(--color-primary-content)]"',
    ],
    ['a string literal returning a fill', "return 'var(--color-success)';"],
  ])('passes %s', (_name, source) => {
    expect(findFillText(source)).toEqual([]);
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
    expect(scannedFiles().length).toBeGreaterThan(MIN_SCANNED_FILES);
  });

  it('colours no text with a status or primary fill outside the listed exceptions', () => {
    const hits = scannedFiles().flatMap(file =>
      // eslint-disable-next-line security/detect-non-literal-fs-filename -- paths come from listing src/
      findFillText(readFileSync(join(srcDir, file), 'utf8')).map(found => ({
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
