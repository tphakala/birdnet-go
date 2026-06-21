/* eslint-disable security/detect-non-literal-fs-filename -- test-only: scans this repo's own src/ tree for .svelte files; paths are derived from the source layout, not user input */
import { describe, it, expect } from 'vitest';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join, relative } from 'node:path';

/**
 * Guard: every static `pattern="..."` attribute in a Svelte component must be a
 * regular expression the browser can actually compile.
 *
 * Browsers compile the HTML form-control `pattern` attribute as
 * `new RegExp("^(?:" + pattern + ")$", "v")`. The `v` (unicodeSets) flag, now
 * the default in current Chromium/Safari, treats `( ) [ ] { } / - \ |` as
 * reserved inside a character class, so an unescaped `/` in `[^/:]` throws
 * "Invalid character in character class". The attribute then silently fails to
 * validate anything (and logs a console error). Escaping it (`[^\/:]`) is valid
 * in both the legacy and `v` engines.
 *
 * This test compiles each pattern exactly as the browser does so a bad pattern
 * fails here in CI instead of silently in the field.
 */

const SRC_DIR = join(dirname(fileURLToPath(import.meta.url)), '..');

/** Recursively collect all .svelte files under a directory. */
function collectSvelteFiles(dir: string): string[] {
  const found: string[] = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    const st = statSync(full);
    if (st.isDirectory()) {
      if (entry === 'node_modules' || entry === 'dist') continue;
      found.push(...collectSvelteFiles(full));
    } else if (entry.endsWith('.svelte')) {
      found.push(full);
    }
  }
  return found;
}

// Static pattern="..." / pattern='...' attributes. The backreference (\1) ties
// the closing quote to the opening one and yields the value in a single group.
// The negative lookbehind excludes hyphenated/word-prefixed attributes such as
// data-pattern= or aria-pattern=, which are not HTML form-control patterns and
// are never compiled by the browser. Optional whitespace around `=` tolerates
// hand-formatted markup, and the `s` flag lets a value span multiple lines so
// such an attribute is validated rather than silently skipped. Dynamic bindings
// (pattern={expr}) cannot be evaluated statically and are intentionally skipped.
const PATTERN_ATTR = /(?<![\w-])pattern\s*=\s*(["'])(.*?)\1/gs;

interface FoundPattern {
  file: string;
  value: string;
}

function extractPatterns(): FoundPattern[] {
  const results: FoundPattern[] = [];
  for (const file of collectSvelteFiles(SRC_DIR)) {
    // Strip <script> and <style> blocks so a JS/TS variable or CSS token named
    // `pattern` cannot be mistaken for an HTML form-control pattern attribute
    // (only the markup region carries real pattern="" attributes).
    const text = readFileSync(file, 'utf8')
      .replace(/<script\b[^>]*>[\s\S]*?<\/script>/gi, '')
      .replace(/<style\b[^>]*>[\s\S]*?<\/style>/gi, '');
    for (const m of text.matchAll(PATTERN_ATTR)) {
      results.push({ file: relative(SRC_DIR, file), value: m[2] });
    }
  }
  return results;
}

describe('Svelte pattern="" attributes compile under the browser v flag', () => {
  const patterns = extractPatterns();

  it('finds at least one pattern attribute to validate', () => {
    // Sanity check so a refactor that hides patterns from the scanner does not
    // turn this guard into a silent no-op.
    expect(patterns.length).toBeGreaterThan(0);
  });

  it.each(patterns.map(p => [p.file, p.value] as const))(
    '%s: pattern %j is a valid v-flag regex',
    (file, value) => {
      // Mirror exactly how the browser compiles a form-control pattern.
      expect(
        // eslint-disable-next-line security/detect-non-literal-regexp -- intentional: this test compiles each component's pattern attribute to verify it is a valid v-flag regex
        () => new RegExp(`^(?:${value})$`, 'v'),
        `Invalid pattern="${value}" in ${file}. The v flag reserves ( ) [ ] { } / - \\ | inside character classes; escape them (e.g. [^/:] -> [^\\/:]).`
      ).not.toThrow();
    }
  );
});
