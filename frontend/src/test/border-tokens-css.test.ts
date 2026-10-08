/**
 * Every --border-NNN token used by a component must be defined in both theme blocks.
 *
 * An undefined custom property in `hover:border-[var(--border-300)]` makes the declaration
 * invalid at computed-value time, so the border falls back to currentColor (near-black on
 * the light theme, near-white on the dark one). jsdom does not resolve custom properties,
 * so this test reads the stylesheet and the component sources.
 */
import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const srcDir = join(dirname(fileURLToPath(import.meta.url)), '..');
const css = readFileSync(join(srcDir, 'styles', 'tailwind.css'), 'utf8');

/** Returns the declaration block that follows the exact selector line at the top level. */
function themeBlock(selectorLine: string): string {
  const start = css.indexOf(`\n${selectorLine} {`);
  expect(start, `missing ${selectorLine} block`).toBeGreaterThan(-1);
  const bodyStart = css.indexOf('{', start) + 1;
  return css.slice(bodyStart, css.indexOf('\n}', bodyStart));
}

const lightBlock = themeBlock(":root,\n[data-theme='light']");
const darkBlock = themeBlock("[data-theme='dark']");

function declaredTokens(block: string): Map<string, string> {
  const tokens = new Map<string, string>();
  for (const match of block.matchAll(/^\s*(--border-\d+):\s*(#[0-9a-fA-F]{6})\b/gm)) {
    tokens.set(match[1], match[2].toLowerCase());
  }
  return tokens;
}

function sourceFiles(dir: string): string[] {
  const files: string[] = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) files.push(...sourceFiles(path));
    else if (/\.(svelte|ts)$/.test(entry.name) && !/\.test\.ts$/.test(entry.name)) files.push(path);
  }
  return files;
}

const usedTokens = new Set<string>();
for (const file of sourceFiles(join(srcDir, 'lib'))) {
  for (const match of readFileSync(file, 'utf8').matchAll(/var\((--border-\d+)\)/g)) {
    usedTokens.add(match[1]);
  }
}

describe('--border-NNN tokens', () => {
  it('finds the tokens the components use', () => {
    expect(usedTokens).toContain('--border-200');
    expect(usedTokens).toContain('--border-300');
  });

  it.each([
    ['light', lightBlock],
    ['dark', darkBlock],
  ] as const)('defines every token the components use in the %s theme', (_theme, block) => {
    const declared = declaredTokens(block);
    for (const token of usedTokens) {
      expect(declared.has(token), `${token} is not defined`).toBe(true);
    }
  });

  it.each([
    ['light', lightBlock],
    ['dark', darkBlock],
  ] as const)('keeps --border-300 distinct from --border-200 in the %s theme', (_theme, block) => {
    const declared = declaredTokens(block);
    expect(declared.get('--border-300')).toBeDefined();
    expect(declared.get('--border-300')).not.toBe(declared.get('--border-200'));
  });
});
