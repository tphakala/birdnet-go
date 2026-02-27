/* eslint-disable no-console */
/**
 * Color contrast testing for WCAG 2.1 Level AA compliance
 * Tests color combinations from the actual Tailwind v4 theme (src/styles/tailwind.css)
 */
import { describe, it, expect } from 'vitest';

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
    infoContent: '#ffffff', // --color-info-content
    success: '#22c55e', // --color-success
    successContent: '#ffffff', // --color-success-content
    warning: '#f59e0b', // --color-warning
    warningContent: '#ffffff', // --color-warning-content
    error: '#ef4444', // --color-error
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
    infoContent: '#ffffff', // --color-info-content
    success: '#16a34a', // --color-success
    successContent: '#ffffff', // --color-success-content
    warning: '#d97706', // --color-warning
    warningContent: '#ffffff', // --color-warning-content
    error: '#dc2626', // --color-error
    errorContent: '#020617', // --color-error-content
  };

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
      const tintedBg = applyOpacity(lightTheme.warning, lightTheme.background, 0.15);
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

  describe('Tailwind Utility Color Contrast (System Components)', () => {
    describe('Light mode — text on surface-100 (#ffffff)', () => {
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

    describe('Dark mode — text on surface-100 (#0f172a)', () => {
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
