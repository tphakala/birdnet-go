/**
 * Build and test helper that inlines theme-resolver.js into index.html. Node only: app code
 * must never import it.
 */
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

/** Placeholder in index.html that is replaced with the resolver source. */
export const PREPAINT_THEME_PLACEHOLDER = '__PREPAINT_THEME_RESOLVER__';

// Not new URL(..., import.meta.url): Vitest rewrites that pattern into an asset URL
const RESOLVER_PATH = join(dirname(fileURLToPath(import.meta.url)), 'theme-resolver.js');

/**
 * Replaces the placeholder in `html` with the text of theme-resolver.js, minus its `export `
 * keywords, so the shipped pre-paint script is exactly the code the store imports.
 * @param {string} html The index.html content.
 * @returns {string} The html with the resolver inlined.
 */
export function injectPrePaintTheme(html) {
  if (!html.includes(PREPAINT_THEME_PLACEHOLDER)) {
    throw new Error(
      '[inline-prepaint-theme] Missing ' + PREPAINT_THEME_PLACEHOLDER + ' in index.html'
    );
  }
  const source = readFileSync(RESOLVER_PATH, 'utf8').replace(/^export /gm, '');
  if (/^\s*(import|export)\b/m.test(source) || /<\/script/i.test(source)) {
    throw new Error(
      '[inline-prepaint-theme] theme-resolver.js must stay an inlineable plain script'
    );
  }
  // Function replacer: `$` sequences in the source must not be interpreted
  return html.replace(PREPAINT_THEME_PLACEHOLDER, () => source);
}
