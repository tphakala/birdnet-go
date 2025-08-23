// D3 theme utilities for analytics charts
import * as d3 from 'd3';
import type { AxisTheme } from './axes';

export interface ChartTheme {
  background: string;
  foreground: string;
  muted: string;
  accent: string;
  primary: string;
  secondary: string;
  success: string;
  warning: string;
  error: string;
  text: string;
  grid: string;
  axis: AxisTheme;
  tooltip: {
    background: string;
    text: string;
    border: string;
  };
}

/**
 * Get current theme from DaisyUI CSS variables
 * Following the pattern from chartHelpers.ts for consistency
 */
export function getCurrentTheme(): ChartTheme {
  const root = document.documentElement;
  // Check if we're in dark mode
  const isDark = root.getAttribute('data-theme') === 'dark';

  // Define colors based on theme - matching the approach in Analytics.svelte and chartHelpers.ts
  let textColor: string;
  let gridColor: string;
  let primary: string;
  let secondary: string;
  let accent: string;
  let success: string;
  let warning: string;
  let error: string;
  let background: string;
  let muted: string;
  let tooltipBgColor: string;
  let tooltipBorderColor: string;

  if (isDark) {
    // Dark theme colors
    textColor = 'rgba(200, 200, 200, 1)';
    gridColor = 'rgba(255, 255, 255, 0.1)';
    primary = '#3b82f6'; // Bright blue
    secondary = '#6b7280'; // Medium gray
    accent = '#0369a1'; // Darker sky blue
    success = '#16a34a'; // Success green
    warning = '#d97706'; // Warning yellow
    error = '#dc2626'; // Error red
    background = '#1f2937'; // Dark background
    muted = 'rgba(255, 255, 255, 0.6)';
    tooltipBgColor = 'rgba(55, 65, 81, 0.95)';
    tooltipBorderColor = 'rgba(255, 255, 255, 0.2)';
  } else {
    // Light theme colors
    textColor = 'rgba(55, 65, 81, 1)';
    gridColor = 'rgba(0, 0, 0, 0.1)';
    primary = '#2563eb'; // Blue
    secondary = '#4b5563'; // Gray
    accent = '#0284c7'; // Sky blue
    success = '#22c55e'; // Success green
    warning = '#f59e0b'; // Warning yellow
    error = '#ef4444'; // Error red
    background = '#ffffff'; // White background
    muted = 'rgba(0, 0, 0, 0.6)';
    tooltipBgColor = 'rgba(255, 255, 255, 0.95)';
    tooltipBorderColor = 'rgba(0, 0, 0, 0.2)';
  }

  return {
    background,
    foreground: textColor,
    muted,
    accent,
    primary,
    secondary,
    success,
    warning,
    error,
    text: textColor,
    grid: gridColor,
    axis: {
      color: textColor,
      fontSize: '12px',
      fontFamily: 'system-ui, -apple-system, sans-serif',
      strokeWidth: 1,
      gridColor,
    },
    tooltip: {
      background: tooltipBgColor,
      text: textColor,
      border: tooltipBorderColor,
    },
  };
}

/**
 * Create a reactive theme store for D3 charts
 */
export class ThemeStore {
  private currentTheme: ChartTheme;
  private readonly callbacks: Set<(theme: ChartTheme) => void> = new Set();
  private observer: MutationObserver | null = null;
  private mediaQuery: MediaQueryList | null = null;
  private mediaQueryListener: (() => void) | null = null;

  constructor() {
    this.currentTheme = getCurrentTheme();
    this.setupThemeObserver();
  }

  private setupThemeObserver(): void {
    // Watch for theme changes on the document element
    this.observer = new MutationObserver(mutations => {
      mutations.forEach(mutation => {
        if (mutation.type === 'attributes' && mutation.attributeName === 'data-theme') {
          this.updateTheme();
        }
      });
    });

    this.observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['data-theme'],
    });

    // Also listen for CSS variable changes
    this.mediaQuery = window.matchMedia('(prefers-color-scheme: dark)');
    this.mediaQueryListener = () => {
      this.updateTheme();
    };
    this.mediaQuery.addEventListener('change', this.mediaQueryListener);
  }

  private updateTheme(): void {
    // Small delay to ensure CSS variables are updated
    setTimeout(() => {
      this.currentTheme = getCurrentTheme();
      this.callbacks.forEach(callback => callback(this.currentTheme));
    }, 50);
  }

  get theme(): ChartTheme {
    return this.currentTheme;
  }

  subscribe(callback: (theme: ChartTheme) => void): () => void {
    this.callbacks.add(callback);

    // Return unsubscribe function
    return () => {
      this.callbacks.delete(callback);
    };
  }

  destroy(): void {
    if (this.observer) {
      this.observer.disconnect();
      this.observer = null;
    }

    if (this.mediaQuery && this.mediaQueryListener) {
      this.mediaQuery.removeEventListener('change', this.mediaQueryListener);
      this.mediaQuery = null;
      this.mediaQueryListener = null;
    }

    this.callbacks.clear();
  }
}

/**
 * Generate species color palette based on theme
 * Using a curated palette for better visual distinction
 */
export function generateSpeciesColors(count: number, theme: ChartTheme): string[] {
  // Check if we're in dark mode
  const isDark = theme.background === '#1f2937';

  // Adjust base opacity based on theme
  const baseOpacity = isDark ? 0.8 : 0.7;

  // Use a more diverse color palette for better distinction between species
  // These colors are carefully chosen to be distinguishable in both light and dark themes
  const baseColors = [
    `rgba(59, 130, 246, ${baseOpacity})`, // Blue
    `rgba(16, 185, 129, ${baseOpacity})`, // Green
    `rgba(245, 158, 11, ${baseOpacity})`, // Orange
    `rgba(236, 72, 153, ${baseOpacity})`, // Pink
    `rgba(139, 92, 246, ${baseOpacity})`, // Purple
    `rgba(239, 68, 68, ${baseOpacity})`, // Red
    `rgba(20, 184, 166, ${baseOpacity})`, // Teal
    `rgba(234, 179, 8, ${baseOpacity})`, // Yellow
    `rgba(99, 102, 241, ${baseOpacity})`, // Indigo
    `rgba(249, 115, 22, ${baseOpacity})`, // Orange-red
    `rgba(168, 85, 247, ${baseOpacity})`, // Purple-pink
    `rgba(34, 197, 94, ${baseOpacity})`, // Emerald
  ];

  if (count <= baseColors.length) {
    return baseColors.slice(0, count);
  }

  // Generate additional colors by modifying opacity
  const colors = [...baseColors];
  const opacityVariations = isDark ? [0.6, 0.4, 0.9] : [0.5, 0.3, 0.8];

  while (colors.length < count) {
    for (let i = 0; i < baseColors.length && colors.length < count; i++) {
      const variationIndex =
        Math.floor((colors.length - baseColors.length) / baseColors.length) %
        opacityVariations.length;
      // eslint-disable-next-line security/detect-object-injection -- Safe: internal array access with controlled index
      const opacity = opacityVariations[variationIndex];

      // Extract rgba values and apply new opacity
      // eslint-disable-next-line security/detect-object-injection -- Safe: internal array access with controlled index
      const baseColor = baseColors[i];
      const rgbaMatch = baseColor.match(/rgba\((\d+),\s*(\d+),\s*(\d+),\s*[\d.]+\)/);

      if (rgbaMatch) {
        const [, r, g, b] = rgbaMatch;
        colors.push(`rgba(${r}, ${g}, ${b}, ${opacity})`);
      }
    }
  }

  return colors.slice(0, count);
}

/**
 * Get contrast color for text on colored backgrounds
 */
export function getContrastColor(backgroundColor: string): string {
  // Simple luminance calculation to determine if we need light or dark text
  const hex = backgroundColor.replace('#', '');
  const r = parseInt(hex.substr(0, 2), 16);
  const g = parseInt(hex.substr(2, 2), 16);
  const b = parseInt(hex.substr(4, 2), 16);

  const luminance = (0.299 * r + 0.587 * g + 0.114 * b) / 255;

  return luminance > 0.5 ? '#000000' : '#ffffff';
}

/**
 * Apply theme transitions to chart elements
 */
export function applyThemeTransition(
  selection: d3.Selection<d3.BaseType, unknown, d3.BaseType, unknown>,
  duration = 300
): d3.Transition<d3.BaseType, unknown, d3.BaseType, unknown> {
  return selection.transition().duration(duration).ease(d3.easeQuadInOut);
}
