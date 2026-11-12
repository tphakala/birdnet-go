/** The build step that inlines the theme resolver refuses source that cannot ship in a script. */
import { afterEach, describe, expect, it, vi } from 'vitest';

const PLACEHOLDER = '__PREPAINT_THEME_RESOLVER__';

/** Loads a fresh theme-prepaint.js whose resolver file reads as `source`. */
async function injectorFor(source: string) {
  vi.resetModules();
  vi.doMock('node:fs', () => ({ readFileSync: () => source }));
  return (await import('../lib/utils/theme-prepaint.js')).injectPrePaintTheme;
}

describe('injectPrePaintTheme', () => {
  afterEach(() => {
    vi.doUnmock('node:fs');
    vi.resetModules();
  });

  it('inlines the resolver without its export keywords', async () => {
    const inject = await injectorFor('export const A = "$&";\n');
    expect(inject(`<script>${PLACEHOLDER};</script>`)).toBe('<script>const A = "$&";\n;</script>');
  });

  it('throws when the placeholder is missing', async () => {
    const inject = await injectorFor('const A = 1;\n');
    expect(() => inject('<script></script>')).toThrow(PLACEHOLDER);
  });

  it.each([
    ['an import line', 'import x from "y";\n'],
    ['a script end tag in any letter case', 'const a = "</ScRiPt>";\n'],
  ])('throws when the resolver contains %s', async (_name, source) => {
    const inject = await injectorFor(source);
    expect(() => inject(PLACEHOLDER)).toThrow('inlineable');
  });
});
