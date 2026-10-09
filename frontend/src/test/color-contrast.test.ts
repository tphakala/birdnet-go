/**
 * Color contrast testing for WCAG 2.1 Level AA compliance
 * Tests color combinations from the actual Tailwind v4 theme (src/styles/tailwind.css)
 */
import { describe, it, expect } from 'vitest';
import { readdirSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import {
  OPTION_SELECTED_BG_CLASS,
  OPTION_HIGHLIGHT_BG_CLASS,
  OPTION_HIGHLIGHT_OUTLINE_CLASS,
} from '../lib/desktop/components/forms/SelectDropdown.styles';
import {
  TOAST_ACTION_CLASS,
  TOAST_CLOSE_CLASS,
  TOAST_TYPE_CLASSES,
} from '../lib/desktop/components/ui/NotificationToast.styles';
import { confidenceColorClasses } from '../lib/desktop/features/dashboard/utils/confidenceColors';
import {
  PLACE_DISCLOSURE_CLASS,
  PLACE_DISCLOSURE_ICON_CLASS,
  PLACE_ERROR_CLASS,
  PLACE_MESSAGE_CLASS,
  PLACE_OPTION_CLASS,
  PLACE_OPTION_DETAIL_CLASS,
} from '../lib/desktop/components/forms/PlaceSearch.styles';

// WCAG 2.1 Level AA contrast ratios
const WCAG_AA_NORMAL = 4.5; // Normal text
const WCAG_AA_LARGE = 3.0; // Large text (18pt+ or 14pt+ bold)

/**
 * Convert hex color to RGB
 */
function hexToRgb(hex: string): { r: number; g: number; b: number } {
  const cleanHex = hex.replace('#', '');
  return {
    r: Number.parseInt(cleanHex.substring(0, 2), 16),
    g: Number.parseInt(cleanHex.substring(2, 4), 16),
    b: Number.parseInt(cleanHex.substring(4, 6), 16),
  };
}

/**
 * Calculate luminance of a color
 */
function getLuminance(rgb: { r: number; g: number; b: number }): number {
  const { r, g, b } = rgb;
  const [rs, gs, bs] = [r, g, b].map(c => {
    c = c / 255;
    return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
  });
  return 0.2126 * rs + 0.7152 * gs + 0.0722 * bs;
}

/**
 * Calculate contrast ratio between two colors
 */
function getContrastRatio(color1: string, color2: string): number {
  const lum1 = getLuminance(hexToRgb(color1));
  const lum2 = getLuminance(hexToRgb(color2));
  const lightest = Math.max(lum1, lum2);
  const darkest = Math.min(lum1, lum2);
  return (lightest + 0.05) / (darkest + 0.05);
}

/**
 * Apply opacity to color against a background
 */
function applyOpacity(baseColor: string, backgroundColor: string, opacity: number): string {
  const base = hexToRgb(baseColor);
  const bg = hexToRgb(backgroundColor);

  const r = Math.round(base.r * opacity + bg.r * (1 - opacity));
  const g = Math.round(base.g * opacity + bg.g * (1 - opacity));
  const b = Math.round(base.b * opacity + bg.b * (1 - opacity));

  return `#${r.toString(16).padStart(2, '0')}${g.toString(16).padStart(2, '0')}${b.toString(16).padStart(2, '0')}`;
}

/**
 * Body of the first block in `css` whose selector line starts with `selector`.
 */
function blockBody(css: string, selector: string): string {
  const start = css.indexOf(`${selector} {`);
  if (start < 0) {
    throw new Error(`block "${selector}" not found`);
  }
  const end = css.indexOf('\n}', start);
  return css.slice(start, end);
}

/** Hex value of custom property `name` in a block body, or null when it is not set to a hex. */
function findVar(body: string, name: string): string | null {
  // eslint-disable-next-line security/detect-non-literal-regexp -- name is one of the fixed custom property names used by the callers
  const match = new RegExp(`${name}:\\s*(#[0-9a-fA-F]{6})`).exec(body);
  return match?.[1] ?? null;
}

/** Hex value of custom property `name` in a block body; fails the test when it is missing. */
function readVar(body: string, name: string): string {
  const value = findVar(body, name);
  expect(value, `${name} is defined as a hex colour`).not.toBeNull();
  return value ?? '';
}

describe('Color Contrast Tests', () => {
  // Actual theme colors from src/styles/tailwind.css (light theme)
  const lightTheme = {
    background: '#ffffff', // --color-base-100
    backgroundAlt: '#f3f4f6', // --color-base-200
    surface100: '#ffffff', // --surface-100
    surface200: '#f8fafc', // --surface-200
    surface300: '#f1f5f9', // --surface-300
    text: '#1f2937', // --color-base-content
    primary: '#2563eb', // --color-primary
    primaryContent: '#ffffff', // --color-primary-content
    secondary: '#4b5563', // --color-secondary
    secondaryContent: '#ffffff', // --color-secondary-content
    accent: '#0284c7', // --color-accent
    neutral: '#1f2937', // --color-neutral
    info: '#0ea5e9', // --color-info
    infoContent: '#020617', // --color-info-content
    success: '#22c55e', // --color-success
    successContent: '#020617', // --color-success-content
    warning: '#f59e0b', // --color-warning
    warningContent: '#020617', // --color-warning-content
    error: '#dc2626', // --color-error
    errorContent: '#ffffff', // --color-error-content
  };

  // Actual theme colors from src/styles/tailwind.css (dark theme)
  const darkTheme = {
    background: '#020617', // --color-base-200 (page bg)
    backgroundAlt: '#0f172a', // --color-base-100 (cards/panels)
    surface100: '#0f172a', // --surface-100
    surface200: '#1e293b', // --surface-200
    surface300: '#334155', // --surface-300
    text: '#f1f5f9', // --color-base-content
    primary: '#3b82f6', // --color-primary
    primaryContent: '#020617', // --color-primary-content
    secondary: '#6b7280', // --color-secondary
    secondaryContent: '#ffffff', // --color-secondary-content
    accent: '#0369a1', // --color-accent
    neutral: '#d1d5db', // --color-neutral
    info: '#0284c7', // --color-info
    infoContent: '#020617', // --color-info-content
    success: '#16a34a', // --color-success
    successContent: '#020617', // --color-success-content
    warning: '#d97706', // --color-warning
    warningContent: '#020617', // --color-warning-content
    error: '#ef4444', // --color-error
    errorContent: '#020617', // --color-error-content
  };

  // Tailwind amber color used for warning text override in alert-warning
  const amber = {
    800: '#92400e',
  };

  // Tailwind slate palette colors used in system components
  const slate = {
    300: '#cbd5e1',
    400: '#94a3b8',
    500: '#64748b',
    600: '#475569',
    700: '#334155',
  };

  // Opacity for tinted alert backgrounds (color-mix at 15%)
  const ALERT_TINT_OPACITY = 0.15;

  describe('Light Theme Contrast', () => {
    it('should pass contrast test for normal text on white background', () => {
      const ratio = getContrastRatio(lightTheme.text, lightTheme.background);
      expect(ratio).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
    });

    it('should pass contrast test for primary color on white background', () => {
      const ratio = getContrastRatio(lightTheme.primary, lightTheme.background);
      expect(ratio).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
    });

    it('should test low opacity text combinations', () => {
      const opacity70 = applyOpacity(lightTheme.text, lightTheme.background, 0.7);
      const ratio70 = getContrastRatio(opacity70, lightTheme.background);

      const opacity60 = applyOpacity(lightTheme.text, lightTheme.background, 0.6);
      const ratio60 = getContrastRatio(opacity60, lightTheme.background);

      const opacity50 = applyOpacity(lightTheme.text, lightTheme.background, 0.5);
      const ratio50 = getContrastRatio(opacity50, lightTheme.background);

      console.log('Light theme opacity contrast ratios:');
      console.log(
        `70% opacity: ${ratio70.toFixed(2)} (${ratio70 >= WCAG_AA_NORMAL ? 'PASS' : 'FAIL'})`
      );
      console.log(
        `60% opacity: ${ratio60.toFixed(2)} (${ratio60 >= WCAG_AA_NORMAL ? 'PASS' : 'FAIL'})`
      );
      console.log(
        `50% opacity: ${ratio50.toFixed(2)} (${ratio50 >= WCAG_AA_NORMAL ? 'PASS' : 'FAIL'})`
      );

      // At least 70% opacity should pass for normal text
      expect(ratio70).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
    });
  });

  describe('Dark Theme Contrast', () => {
    it('should pass contrast test for normal text on dark background', () => {
      const ratio = getContrastRatio(darkTheme.text, darkTheme.background);
      expect(ratio).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
    });

    it('should test low opacity text combinations in dark theme', () => {
      const opacity70 = applyOpacity(darkTheme.text, darkTheme.surface100, 0.7);
      const ratio70 = getContrastRatio(opacity70, darkTheme.surface100);

      const opacity60 = applyOpacity(darkTheme.text, darkTheme.surface100, 0.6);
      const ratio60 = getContrastRatio(opacity60, darkTheme.surface100);

      const opacity50 = applyOpacity(darkTheme.text, darkTheme.surface100, 0.5);
      const ratio50 = getContrastRatio(opacity50, darkTheme.surface100);

      console.log('Dark theme opacity contrast ratios:');
      console.log(
        `70% opacity: ${ratio70.toFixed(2)} (${ratio70 >= WCAG_AA_NORMAL ? 'PASS' : 'FAIL'})`
      );
      console.log(
        `60% opacity: ${ratio60.toFixed(2)} (${ratio60 >= WCAG_AA_NORMAL ? 'PASS' : 'FAIL'})`
      );
      console.log(
        `50% opacity: ${ratio50.toFixed(2)} (${ratio50 >= WCAG_AA_NORMAL ? 'PASS' : 'FAIL'})`
      );

      // At least 70% opacity should pass for normal text
      expect(ratio70).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
    });

    it('should pass contrast test for primary color on dark background', () => {
      const ratio = getContrastRatio(darkTheme.primary, darkTheme.surface100);
      expect(ratio).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
    });
  });

  describe('Component-Specific Contrast Tests', () => {
    it('should test button states', () => {
      // Primary button (content text on primary background)
      const primaryButtonRatio = getContrastRatio(lightTheme.primaryContent, lightTheme.primary);
      expect(primaryButtonRatio).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);

      // Secondary button (content text on secondary background)
      const secondaryButtonRatio = getContrastRatio(
        lightTheme.secondaryContent,
        lightTheme.secondary
      );
      expect(secondaryButtonRatio).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
    });

    it('should test form field placeholders and help text', () => {
      // Help text typically uses 70% opacity
      const helpTextColor = applyOpacity(lightTheme.text, lightTheme.background, 0.7);
      const ratio = getContrastRatio(helpTextColor, lightTheme.background);

      // Help text can be slightly lower contrast but should still be readable
      expect(ratio).toBeGreaterThanOrEqual(WCAG_AA_LARGE); // 3.0 for large text standard
    });

    it('should test warning alert text contrast (amber-800 override)', () => {
      // Warning alert uses hardcoded #92400e (amber-800) for contrast
      const tintedBg = applyOpacity(lightTheme.warning, lightTheme.background, ALERT_TINT_OPACITY);
      const ratio = getContrastRatio(amber[800], tintedBg);
      console.log(`Warning alert text-on-tint contrast: ${ratio.toFixed(2)}`);
      expect(ratio).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
    });

    it('should test status colors on white background', () => {
      // Status colors used as inline text on white backgrounds
      const statusColors = [
        { name: 'Error', color: lightTheme.error },
        { name: 'Warning (amber-800)', color: amber[800] },
      ];

      statusColors.forEach(({ name, color }) => {
        const ratio = getContrastRatio(color, lightTheme.background);
        console.log(`${name} on white contrast: ${ratio.toFixed(2)}`);
        expect(ratio).toBeGreaterThanOrEqual(WCAG_AA_LARGE);
      });
    });
  });

  describe('Tailwind Utility Color Contrast (System Components)', () => {
    describe('Light mode: text on surface-100 (#ffffff)', () => {
      const bg = lightTheme.surface100;

      it('text-slate-600 passes AA for normal text', () => {
        expect(getContrastRatio(slate[600], bg)).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
      });

      it('text-slate-500 passes AA for large/bold text', () => {
        expect(getContrastRatio(slate[500], bg)).toBeGreaterThanOrEqual(WCAG_AA_LARGE);
      });

      it('text-slate-400 fails AA for normal text (regression guard)', () => {
        expect(getContrastRatio(slate[400], bg)).toBeLessThan(WCAG_AA_NORMAL);
      });
    });

    describe('Dark mode: text on surface-100 (#0f172a)', () => {
      const bg = darkTheme.surface100;

      it('text-slate-400 passes AA for normal text', () => {
        expect(getContrastRatio(slate[400], bg)).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
      });

      it('text-slate-300 passes AA for normal text', () => {
        expect(getContrastRatio(slate[300], bg)).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
      });

      it('text-slate-500 fails AA for normal text (regression guard)', () => {
        expect(getContrastRatio(slate[500], bg)).toBeLessThan(WCAG_AA_NORMAL);
      });
    });

    describe('Opacity on base-content', () => {
      it('70% opacity passes AA in light mode', () => {
        const color = applyOpacity(lightTheme.text, lightTheme.background, 0.7);
        expect(getContrastRatio(color, lightTheme.background)).toBeGreaterThanOrEqual(
          WCAG_AA_NORMAL
        );
      });

      it('70% opacity passes AA in dark mode', () => {
        const color = applyOpacity(darkTheme.text, darkTheme.surface100, 0.7);
        expect(getContrastRatio(color, darkTheme.surface100)).toBeGreaterThanOrEqual(
          WCAG_AA_NORMAL
        );
      });

      it('35% opacity fails AA in light mode (regression guard)', () => {
        const color = applyOpacity(lightTheme.text, lightTheme.background, 0.35);
        expect(getContrastRatio(color, lightTheme.background)).toBeLessThan(WCAG_AA_NORMAL);
      });
    });
  });
});

describe('Accessibility: error text token', () => {
  const css = readFileSync(
    join(dirname(fileURLToPath(import.meta.url)), '..', 'styles', 'tailwind.css'),
    'utf8'
  );

  const themes = [
    {
      name: 'light',
      base: blockBody(css, '@theme'),
      text: blockBody(css, ":root,\n[data-theme='light']"),
    },
    {
      name: 'dark',
      base: blockBody(css, "[data-theme='dark']"),
      text: blockBody(css, "[data-theme='dark']"),
    },
  ];

  for (const theme of themes) {
    for (const surface of ['--color-base-100', '--color-base-200', '--color-base-300']) {
      it(`--text-error passes AA on ${surface} in the ${theme.name} theme`, () => {
        const ratio = getContrastRatio(
          readVar(theme.text, '--text-error'),
          readVar(theme.base, surface)
        );
        expect(ratio).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
      });
    }
  }
});

describe('Error text rules', () => {
  const stylesDir = join(dirname(fileURLToPath(import.meta.url)), '..', 'styles');
  const css = readFileSync(join(stylesDir, 'tailwind.css'), 'utf8');

  /**
   * Bodies of every rule written as `selector { ... }` in `source`. A compound selector that ends
   * in `selector` (`.a .text-error { ... }`) matches too.
   */
  function ruleBodies(source: string, selector: string): string[] {
    const bodies: string[] = [];
    const opener = `${selector} {`;
    let from = 0;
    for (;;) {
      const start = source.indexOf(opener, from);
      if (start < 0) {
        return bodies;
      }
      const bodyStart = start + opener.length;
      const end = source.indexOf('}', bodyStart);
      if (end < 0) {
        throw new Error(`rule "${selector}" has no closing brace`);
      }
      bodies.push(source.slice(bodyStart, end));
      from = end;
    }
  }

  for (const selector of ['.text-error', '.alert-error', '.badge-status-error']) {
    it(`rule body of ${selector} is color: var(--text-error)`, () => {
      const bodies = ruleBodies(css, selector);
      expect(bodies.length, `${selector} rule found`).toBeGreaterThan(0);
      for (const body of bodies) {
        expect(body).toMatch(/(^|[\s;])color:\s*var\(--text-error\);/);
        expect(body).not.toMatch(/(^|[\s;])color:\s*var\(--color-error\)/);
      }
    });
  }

  // The tint under .alert-error and .badge-status-error stays the fill token, at the share the
  // contrast tests below assume.
  for (const selector of ['.alert-error', '.badge-status-error']) {
    it(`${selector} keeps its 15% --color-error tint`, () => {
      const bodies = ruleBodies(css, selector);
      expect(bodies.length).toBeGreaterThan(0);
      for (const body of bodies) {
        expect(body).toContain('color-mix(in srgb, var(--color-error) 15%, transparent)');
      }
    });
  }

  const desktopDir = join(stylesDir, '..', 'lib', 'desktop');
  // Component styles with error text. `fills` names the selectors whose border or tint keeps the
  // fill token, with the declaration text each must still hold.
  const COMPONENTS_WITH_ERROR_TEXT = [
    {
      file: 'components/media/AudioToolbar.svelte',
      fills: [
        {
          selector: '.toolbar-btn.error',
          declaration: 'border-color: var(--color-error, #ef4444)',
        },
      ],
    },
    {
      file: 'features/dashboard/components/PlayOverlay.svelte',
      fills: [
        { selector: '.error-indicator', declaration: 'color-mix(in srgb, var(--color-error) 10%' },
      ],
    },
    {
      file: 'views/Search.svelte',
      fills: [
        {
          selector: '.review-dropdown-item.false-positive:hover',
          declaration: 'color-mix(in srgb, var(--color-error) 15%',
        },
      ],
    },
  ];

  /** Source text of a listed component; read inside each test so a moved file fails only its tests. */
  function componentText(file: string): string {
    // eslint-disable-next-line security/detect-non-literal-fs-filename -- paths come from the fixed list above
    return readFileSync(join(desktopDir, file), 'utf8');
  }

  for (const { file, fills } of COMPONENTS_WITH_ERROR_TEXT) {
    it(`${file} has no plain color: var(--color-error) declaration`, () => {
      const text = componentText(file);
      // `color:` declarations only; border-color and background-color keep the fill token
      expect(text).not.toMatch(/^\s*color:\s*var\(--color-error[,)]/m);
      expect(text).toMatch(/^\s*color:\s*var\(--text-error\)/m);
    });

    for (const { selector, declaration } of fills) {
      it(`${file} ${selector} keeps the fill token for its border or tint`, () => {
        const bodies = ruleBodies(componentText(file), selector);
        expect(bodies.length, `${selector} rule found`).toBeGreaterThan(0);
        expect(bodies.some(body => body.includes(declaration))).toBe(true);
      });
    }
  }

  const lightBase = blockBody(css, '@theme');
  const darkBlock = blockBody(css, "[data-theme='dark']");

  for (const [name, block, textBlock] of [
    ['light', lightBase, blockBody(css, ":root,\n[data-theme='light']")],
    ['dark', darkBlock, darkBlock],
  ] as const) {
    it(`--text-error passes AA on a 15% error tint over base-100 in the ${name} theme`, () => {
      // .alert-error and .badge-status-error draw a 15% --color-error tint under --text-error
      const tint = applyOpacity(
        readVar(block, '--color-error'),
        readVar(block, '--color-base-100'),
        0.15
      );
      expect(getContrastRatio(readVar(textBlock, '--text-error'), tint)).toBeGreaterThanOrEqual(
        WCAG_AA_NORMAL
      );
    });
  }
});

describe('SelectDropdown option states in every color scheme', () => {
  const stylesDir = join(dirname(fileURLToPath(import.meta.url)), '..', 'styles');
  const tailwindCss = readFileSync(join(stylesDir, 'tailwind.css'), 'utf8');
  const schemesCss = readFileSync(join(stylesDir, 'schemes.css'), 'utf8');

  const FIXED_SCHEMES = ['blue', 'forest', 'amber', 'violet', 'rose'];
  const ALL_SCHEMES = [...FIXED_SCHEMES, 'custom'];

  /** Minimum ratio for the focus outline against its neighbours (WCAG 1.4.11). */
  const OUTLINE_MIN_RATIO = 3;

  const lightBase = blockBody(tailwindCss, '@theme');
  const darkBase = blockBody(tailwindCss, "[data-theme='dark']");

  /** Tint fraction written as `var(--token)_NN%` in a class constant. */
  function tintFraction(token: string, classes: string): number {
    // eslint-disable-next-line security/detect-non-literal-regexp -- token is a fixed custom property name
    const match = new RegExp(`var\\(${token}\\)_(\\d+)%`).exec(classes);
    if (!match) {
      throw new Error(`${token} tint percentage not found in "${classes}"`);
    }
    return Number(match[1]) / 100;
  }

  /** Custom property written as `outline-[var(--token)]` in a class constant. */
  function outlineColorToken(classes: string): string {
    const match = /outline-\[var\((--[a-z0-9-]+)\)\]/.exec(classes);
    if (!match) {
      throw new Error(`outline color not found in "${classes}"`);
    }
    return match[1];
  }

  const outlineToken = outlineColorToken(OPTION_HIGHLIGHT_OUTLINE_CLASS);

  const selectedFraction = tintFraction('--color-primary', OPTION_SELECTED_BG_CLASS);
  const highlightFraction = tintFraction('--color-base-content', OPTION_HIGHLIGHT_BG_CLASS);

  /** Primary color of a scheme in a theme, mirroring the cascade in tailwind.css and schemes.css. */
  function primaryOf(scheme: string, theme: 'light' | 'dark'): string {
    const lightScheme = findVar(
      blockBody(schemesCss, `\n[data-scheme='${scheme}']`),
      '--color-primary'
    );
    if (theme === 'light') {
      return lightScheme ?? readVar(lightBase, '--color-primary');
    }
    const darkSelector = `[data-theme='dark'][data-scheme='${scheme}']`;
    const darkScheme = schemesCss.includes(`${darkSelector} {`)
      ? findVar(blockBody(schemesCss, darkSelector), '--color-primary')
      : null;
    return darkScheme ?? lightScheme ?? readVar(darkBase, '--color-primary');
  }

  /** Contrast figures of one option for a given primary color and theme. */
  function measure(primary: string, theme: 'light' | 'dark') {
    const base = theme === 'light' ? lightBase : darkBase;
    const surface = readVar(base, '--color-base-100');
    const content = readVar(base, '--color-base-content');
    const outline = readVar(base, outlineToken);
    const selectedTint = applyOpacity(primary, surface, selectedFraction);
    const highlightTint = applyOpacity(content, surface, highlightFraction);
    return {
      textOnSelected: getContrastRatio(content, selectedTint),
      textOnHighlight: getContrastRatio(content, highlightTint),
      outlineOnSurface: getContrastRatio(outline, surface),
      outlineOnSelected: getContrastRatio(outline, selectedTint),
      outlineOnHighlight: getContrastRatio(outline, highlightTint),
    };
  }

  it('measures an outline color that no scheme overrides', () => {
    // The matrix reads the outline token from the theme blocks only, so a token that
    // schemes.css redefines per scheme (such as --color-primary) would be measured wrongly.
    expect(schemesCss).not.toContain(`${outlineToken}:`);
  });

  it('covers every scheme defined in schemes.css', () => {
    const names = new Set(
      [...schemesCss.matchAll(/\[data-scheme=["']?([^"'\]]+)["']?\]/g)].map(m => m[1])
    );
    expect([...names].sort()).toEqual([...ALL_SCHEMES].sort());
  });

  for (const scheme of FIXED_SCHEMES) {
    for (const theme of ['light', 'dark'] as const) {
      it(`${scheme} ${theme}: option text and highlight outline meet AA`, () => {
        const m = measure(primaryOf(scheme, theme), theme);
        expect(m.textOnSelected).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
        expect(m.textOnHighlight).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
        expect(m.outlineOnSurface).toBeGreaterThanOrEqual(OUTLINE_MIN_RATIO);
        expect(m.outlineOnSelected).toBeGreaterThanOrEqual(OUTLINE_MIN_RATIO);
        expect(m.outlineOnHighlight).toBeGreaterThanOrEqual(OUTLINE_MIN_RATIO);
      });
    }
  }

  it('custom scheme: option text and outline pass for any primary color', () => {
    const channel = [0x00, 0x33, 0x66, 0x99, 0xcc, 0xff];
    const toHex = (n: number) => n.toString(16).padStart(2, '0');
    const primaries = ['#2563eb', '#3b82f6'];
    for (const r of channel) {
      for (const g of channel) {
        for (const b of channel) {
          primaries.push(`#${toHex(r)}${toHex(g)}${toHex(b)}`);
        }
      }
    }
    expect(primaries).toHaveLength(216 + 2);

    for (const theme of ['light', 'dark'] as const) {
      for (const primary of primaries) {
        const m = measure(primary, theme);
        expect(m.textOnSelected, `${theme} ${primary} text`).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
        expect(m.outlineOnSelected, `${theme} ${primary} outline`).toBeGreaterThanOrEqual(
          OUTLINE_MIN_RATIO
        );
      }
    }
  });
});

describe('PlaceSearch colors in light and dark themes', () => {
  const stylesDir = join(dirname(fileURLToPath(import.meta.url)), '..', 'styles');
  const tailwindCss = readFileSync(join(stylesDir, 'tailwind.css'), 'utf8');
  const lightBase = blockBody(tailwindCss, '@theme');
  const lightText = blockBody(tailwindCss, ":root,\n[data-theme='light']");
  const darkBase = blockBody(tailwindCss, "[data-theme='dark']");

  /** Minimum ratio for an informative icon (WCAG 1.4.11). */
  const ICON_MIN_RATIO = 3;

  /** Custom property written as `text-[var(--token)]` in a class constant, without opacity. */
  function textToken(classes: string): string {
    const match = /(?:^|\s)text-\[var\((--[a-z0-9-]+)\)\](?!\/)/.exec(classes);
    if (!match) {
      throw new Error(`text color token not found in "${classes}"`);
    }
    return match[1];
  }

  /** Tint fraction written as `var(--token)_NN%` in a class constant. */
  function highlightFraction(): number {
    const match = /var\(--color-base-content\)_(\d+)%/.exec(OPTION_HIGHLIGHT_BG_CLASS);
    if (!match) {
      throw new Error('highlight tint not found');
    }
    return Number(match[1]) / 100;
  }

  for (const theme of ['light', 'dark'] as const) {
    describe(`${theme} theme`, () => {
      const base = theme === 'light' ? lightBase : darkBase;
      const textBlock = theme === 'light' ? lightText : darkBase;
      /** A token from the color block or, for the --text-* tokens, the theme's text block. */
      const tokenValue = (token: string) => findVar(base, token) ?? readVar(textBlock, token);
      const surface = readVar(base, '--color-base-100');
      const content = readVar(base, '--color-base-content');
      const highlightTint = applyOpacity(content, surface, highlightFraction());

      it('option name and detail text meet AA on the surface and on the highlighted option', () => {
        const name = tokenValue(textToken(PLACE_OPTION_CLASS));
        const detail = tokenValue(textToken(PLACE_OPTION_DETAIL_CLASS));
        for (const background of [surface, highlightTint]) {
          expect(getContrastRatio(name, background)).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
          expect(getContrastRatio(detail, background)).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
        }
      });

      it('uses the shared highlight outline and background', () => {
        const outline = tokenValue(outlineToken(OPTION_HIGHLIGHT_OUTLINE_CLASS));
        expect(getContrastRatio(outline, surface)).toBeGreaterThanOrEqual(ICON_MIN_RATIO);
        expect(getContrastRatio(outline, highlightTint)).toBeGreaterThanOrEqual(ICON_MIN_RATIO);
      });

      it('disclosure text and its link icon meet AA', () => {
        const text = tokenValue(textToken(PLACE_DISCLOSURE_CLASS));
        const icon = tokenValue(textToken(PLACE_DISCLOSURE_ICON_CLASS));
        expect(getContrastRatio(text, surface)).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
        expect(getContrastRatio(icon, surface)).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
      });

      it('error and status line text meet AA', () => {
        const error = tokenValue(textToken(PLACE_ERROR_CLASS));
        const status = tokenValue(textToken(PLACE_MESSAGE_CLASS));
        expect(getContrastRatio(error, surface)).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
        expect(getContrastRatio(status, surface)).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
      });
    });
  }

  function outlineToken(classes: string): string {
    const match = /outline-\[var\((--[a-z0-9-]+)\)\]/.exec(classes);
    if (!match) {
      throw new Error(`outline color not found in "${classes}"`);
    }
    return match[1];
  }
});

describe('Status colors with their content color', () => {
  const css = readFileSync(
    join(dirname(fileURLToPath(import.meta.url)), '..', 'styles', 'tailwind.css'),
    'utf8'
  );

  const STATUSES = ['info', 'success', 'warning', 'error'] as const;
  const lightBase = blockBody(css, '@theme');
  const darkBlock = blockBody(css, "[data-theme='dark']");

  /** A status token in a theme. Each theme must define it explicitly, with no fallback to the other. */
  function statusToken(theme: 'light' | 'dark', token: string): string {
    return readVar(theme === 'light' ? lightBase : darkBlock, token);
  }

  it('defines every status, hover and content token in each theme block', () => {
    for (const theme of ['light', 'dark'] as const) {
      for (const status of STATUSES) {
        for (const suffix of ['', '-hover', '-content']) {
          expect(() => statusToken(theme, `--color-${status}${suffix}`)).not.toThrow();
        }
      }
    }
  });

  for (const theme of ['light', 'dark'] as const) {
    for (const status of STATUSES) {
      const content = statusToken(theme, `--color-${status}-content`);

      it(`${theme} ${status}: content text meets AA on the status fill`, () => {
        const fill = statusToken(theme, `--color-${status}`);
        expect(getContrastRatio(content, fill)).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
      });

      it(`${theme} ${status}: content text meets AA on the hover fill`, () => {
        const hover = statusToken(theme, `--color-${status}-hover`);
        expect(getContrastRatio(content, hover)).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
      });
    }
  }

  it('toast types pair each status fill with its content color', () => {
    expect(Object.keys(TOAST_TYPE_CLASSES).sort()).toEqual([...STATUSES].sort());
    for (const [status, classes] of Object.entries(TOAST_TYPE_CLASSES)) {
      expect(classes).toContain(`bg-[var(--color-${status})]`);
      expect(classes).toContain(`text-[var(--color-${status}-content)]`);
    }
  });

  it('toast close button adds no background', () => {
    const backgrounds = TOAST_CLOSE_CLASS.split(/\s+/).filter(token =>
      token.slice(token.lastIndexOf(':') + 1).startsWith('bg-')
    );
    expect(backgrounds).toEqual([]);
  });

  it('toast action buttons add no background', () => {
    // The utility name follows the last variant prefix, so `hover:bg-white/30` counts too.
    const backgrounds = TOAST_ACTION_CLASS.split(/\s+/).filter(token =>
      token.slice(token.lastIndexOf(':') + 1).startsWith('bg-')
    );
    expect(backgrounds).toEqual([]);
  });

  describe('confidence blends', () => {
    /** Confidence percentages that select the two color-mix bands (see confidenceColors.ts). */
    const BLENDS = [
      { band: 'success and warning', percent: 80 },
      { band: 'warning and error', percent: 40 },
    ];

    for (const { band, percent } of BLENDS) {
      const classes = confidenceColorClasses(percent);

      it(`${band} blend uses a content token, not white`, () => {
        expect(classes).not.toContain('text-white');
        expect(classes).toMatch(/text-\[var\(--color-[a-z]+-content\)\]/);
      });

      for (const theme of ['light', 'dark'] as const) {
        it(`${band} blend keeps AA with its content color in the ${theme} theme`, () => {
          const mix = /color-mix\(in_srgb,var\((--[a-z-]+)\)_(\d+)%,var\((--[a-z-]+)\)\)/.exec(
            classes
          );
          expect(mix, `color-mix found in "${classes}"`).not.toBeNull();
          const [, first, share, second] = mix ?? [];
          const fill = applyOpacity(
            statusToken(theme, first),
            statusToken(theme, second),
            Number(share) / 100
          );
          const contentToken = /text-\[var\((--[a-z-]+)\)\]/.exec(classes)?.[1] ?? '';
          const content = statusToken(theme, contentToken);
          expect(getContrastRatio(content, fill)).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
        });
      }
    }
  });
});

describe('Components pair a status fill with its content color', () => {
  const libDir = join(dirname(fileURLToPath(import.meta.url)), '..', 'lib');
  const sources = readdirSync(libDir, { recursive: true, encoding: 'utf8' })
    .filter(file => file.endsWith('.svelte'))
    // eslint-disable-next-line security/detect-non-literal-fs-filename -- paths come from listing the component directory
    .map(file => ({ file, text: readFileSync(join(libDir, file), 'utf8') }));

  /** A status fill used in full, not as a translucent tint (`bg-[var(--color-error)]/10`). */
  const SOLID_FILL = /bg-\[var\(--color-(?:info|success|warning|error)\)\](?![/\w])/g;
  const QUOTES = ['"', "'", '`'];

  /** The quoted string around `index`, which holds the whole class list of a Tailwind class string. */
  function quotedAround(text: string, index: number): string {
    const start = Math.max(...QUOTES.map(quote => text.lastIndexOf(quote, index)));
    const ends = QUOTES.map(quote => text.indexOf(quote, index)).filter(end => end >= 0);
    return text.slice(start + 1, Math.min(...ends));
  }

  it('scans the component sources', () => {
    expect(sources.length).toBeGreaterThan(100);
  });

  it('no class string puts white text on a solid status token fill', () => {
    const offenders = sources.flatMap(({ file, text }) =>
      [...text.matchAll(SOLID_FILL)]
        .filter(match => /(^|\s)text-white(\s|$)/.test(quotedAround(text, match.index)))
        .map(() => file)
    );
    expect(offenders).toEqual([]);
  });

  it('no class string fades a solid status token fill on hover', () => {
    const offenders = sources.flatMap(({ file, text }) =>
      [...text.matchAll(SOLID_FILL)]
        .filter(match =>
          /hover:bg-\[var\(--color-(?:info|success|warning|error)\)\]\//.test(
            quotedAround(text, match.index)
          )
        )
        .map(() => file)
    );
    expect(offenders).toEqual([]);
  });

  it('no class string fades a solid status token fill with hover opacity', () => {
    const offenders = sources.flatMap(({ file, text }) =>
      [...text.matchAll(SOLID_FILL)]
        .filter(match => /(^|\s)hover:opacity-/.test(quotedAround(text, match.index)))
        .map(() => file)
    );
    expect(offenders).toEqual([]);
  });

  it('no style rule puts white text on a literal status color fill', () => {
    // The status token values of both themes, plus the emerald green the Search badges used.
    const css = readFileSync(join(libDir, '..', 'styles', 'tailwind.css'), 'utf8');
    const tokenBlocks = [blockBody(css, '@theme'), blockBody(css, "[data-theme='dark']")];
    const literals = [
      ...tokenBlocks.flatMap(block =>
        ['info', 'success', 'warning', 'error'].flatMap(status => [
          readVar(block, `--color-${status}`),
          readVar(block, `--color-${status}-hover`),
        ])
      ),
      '#10b981',
    ].map(hex => hex.toLowerCase());
    const offenders = sources.flatMap(({ file, text }) =>
      [...text.matchAll(/\{[^{}]*\}/g)]
        .filter(([rule]) => {
          const fill = /background(?:-color)?:\s*(#[0-9a-fA-F]{6})\b/.exec(rule)?.[1];
          return (
            fill !== undefined &&
            literals.includes(fill.toLowerCase()) &&
            /(?<![-\w])color:\s*(?:white|#fff(?:fff)?)\s*;/i.test(rule)
          );
        })
        .map(() => file)
    );
    expect(offenders).toEqual([]);
  });

  it('no style rule puts white text on a status token fill', () => {
    const offenders = sources.flatMap(({ file, text }) =>
      [...text.matchAll(/\{[^{}]*\}/g)]
        .filter(
          ([rule]) =>
            /background(?:-color)?:[^;]*var\(--color-(?:info|success|warning|error)\)/.test(rule) &&
            /(?<![-\w])color:\s*(?:white|#fff(?:fff)?)\s*;/i.test(rule)
        )
        .map(() => file)
    );
    expect(offenders).toEqual([]);
  });
});
