/**
 * Reads the inline pre-paint scripts of index.html, which run before any bundle loads and so
 * cannot import app code. Tests run a block with `new Function(...)()` to check it against the
 * stores it mirrors.
 */
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const INDEX_HTML = join(dirname(fileURLToPath(import.meta.url)), '..', '..', 'index.html');
const BLOCK_END = '})();';

/**
 * Returns one IIFE block of the inline script in index.html: from the comment `marker` to the
 * first `})();` after it. Empty when either is missing, so a test can fail on it.
 */
export function indexHtmlScriptBlock(marker: string): string {
  const html = readFileSync(INDEX_HTML, 'utf8');
  const start = html.indexOf(marker);
  const end = html.indexOf(BLOCK_END, start);
  return start === -1 || end === -1 ? '' : html.slice(start, end + BLOCK_END.length);
}
