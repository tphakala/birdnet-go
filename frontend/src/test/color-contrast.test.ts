/**
 * Color contrast testing for WCAG 2.1 Level AA compliance
 * Tests common color combinations used in the application
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
    r: Number.parseInt(cleanHex.substr(0, 2), 16),
    g: Number.parseInt(cleanHex.substr(2, 2), 16),
    b: Number.parseInt(cleanHex.substr(4, 2), 16),
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
 * Apply opacity to color
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
  // DaisyUI theme colors from tailwind.config.js (light theme)
  const lightTheme = {
    background: '#ffffff', // base-100
    backgroundAlt: '#f3f4f6', // base-200
    text: '#1f2937', // base-content
    primary: '#2563eb', // primary
    secondary: '#374151', // secondary (updated for better contrast)
    accent: '#0284c7', // accent
    neutral: '#1f2937', // neutral
    info: '#0369a1', // info (updated for better contrast)
    success: '#15803d', // success (updated for better contrast)
    warning: '#b45309', // warning (updated for better contrast)
    error: '#dc2626', // error (updated for better contrast)
  };

  // DaisyUI theme colors from tailwind.config.js (dark theme)
  const darkTheme = {
    background: '#1f2937', // base-100
    backgroundAlt: '#111827', // base-200
    text: '#d1d5db', // base-content
    primary: '#60a5fa', // primary (updated for better contrast on dark)
    secondary: '#9ca3af', // secondary (updated for better contrast)
    accent: '#0369a1', // accent
    neutral: '#d1d5db', // neutral
    info: '#0284c7', // info
    success: '#16a34a', // success
    warning: '#d97706', // warning
    error: '#dc2626', // error
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
      // Test text-base-content/70 (70% opacity)
      const opacity70 = applyOpacity(lightTheme.text, lightTheme.background, 0.7);
      const ratio70 = getContrastRatio(opacity70, lightTheme.background);

      // Test text-base-content/60 (60% opacity)
      const opacity60 = applyOpacity(lightTheme.text, lightTheme.background, 0.6);
      const ratio60 = getContrastRatio(opacity60, lightTheme.background);

      // Test text-base-content/50 (50% opacity)
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

    it('should test error text contrast', () => {
      const ratio = getContrastRatio(lightTheme.error, lightTheme.background);
      expect(ratio).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
    });

    it('should test success text contrast', () => {
      const ratio = getContrastRatio(lightTheme.success, lightTheme.background);
      expect(ratio).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
    });
  });

  describe('Dark Theme Contrast', () => {
    it('should pass contrast test for normal text on dark background', () => {
      const ratio = getContrastRatio(darkTheme.text, darkTheme.background);
      expect(ratio).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
    });

    it('should test low opacity text combinations in dark theme', () => {
      // Test text-base-content/70 (70% opacity)
      const opacity70 = applyOpacity(darkTheme.text, darkTheme.background, 0.7);
      const ratio70 = getContrastRatio(opacity70, darkTheme.background);

      // Test text-base-content/60 (60% opacity)
      const opacity60 = applyOpacity(darkTheme.text, darkTheme.background, 0.6);
      const ratio60 = getContrastRatio(opacity60, darkTheme.background);

      // Test text-base-content/50 (50% opacity)
      const opacity50 = applyOpacity(darkTheme.text, darkTheme.background, 0.5);
      const ratio50 = getContrastRatio(opacity50, darkTheme.background);

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

    it('should test primary color on dark background', () => {
      const ratio = getContrastRatio(darkTheme.primary, darkTheme.background);
      expect(ratio).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
    });
  });

  describe('Component-Specific Contrast Tests', () => {
    it('should test button states', () => {
      // Primary button (white text on primary background)
      const primaryButtonRatio = getContrastRatio('#ffffff', lightTheme.primary);
      expect(primaryButtonRatio).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);

      // Secondary button
      const secondaryButtonRatio = getContrastRatio('#ffffff', lightTheme.secondary);
      expect(secondaryButtonRatio).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
    });

    it('should test form field placeholders and help text', () => {
      // Help text typically uses 70% opacity
      const helpTextColor = applyOpacity(lightTheme.text, lightTheme.background, 0.7);
      const ratio = getContrastRatio(helpTextColor, lightTheme.background);

      // Help text can be slightly lower contrast but should still be readable
      expect(ratio).toBeGreaterThanOrEqual(WCAG_AA_LARGE); // 3.0 for large text standard
    });

    it('should test alert and status colors', () => {
      const colors = [
        { name: 'Error', color: lightTheme.error },
        { name: 'Success', color: lightTheme.success },
        { name: 'Warning', color: lightTheme.warning },
        { name: 'Info', color: lightTheme.info },
      ];

      colors.forEach(({ name, color }) => {
        const ratio = getContrastRatio(color, lightTheme.background);
        console.log(`${name} color contrast: ${ratio.toFixed(2)}`);
        expect(ratio).toBeGreaterThanOrEqual(WCAG_AA_NORMAL);
      });
    });
  });
});
