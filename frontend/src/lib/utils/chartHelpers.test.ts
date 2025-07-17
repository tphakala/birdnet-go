import { describe, it, expect, vi, beforeEach } from 'vitest';
import {
  getThemeColor,
  getChartTheme,
  generateColorPalette,
  createChartOptions,
  formatChartNumber,
  createTooltipCallback,
} from './chartHelpers';

describe('chartHelpers', () => {
  beforeEach(() => {
    // Mock getComputedStyle
    global.getComputedStyle = vi.fn().mockImplementation(() => ({
      getPropertyValue: vi.fn().mockReturnValue('#3b82f6'),
    }));
  });

  describe('getThemeColor', () => {
    it('converts hex colors to rgba', () => {
      const color = getThemeColor('primary', 0.5);
      expect(color).toBe('rgba(59, 130, 246, 0.5)');
    });

    it('handles rgb colors', () => {
      global.getComputedStyle = vi.fn().mockImplementation(() => ({
        getPropertyValue: vi.fn().mockReturnValue('rgb(59, 130, 246)'),
      }));

      const color = getThemeColor('primary', 0.8);
      expect(color).toBe('rgba(59, 130, 246, 0.8)');
    });

    it('handles rgba colors', () => {
      global.getComputedStyle = vi.fn().mockImplementation(() => ({
        getPropertyValue: vi.fn().mockReturnValue('rgba(59, 130, 246, 0.7)'),
      }));

      const color = getThemeColor('primary', 0.5);
      expect(color).toBe('rgba(59, 130, 246, 0.5)');
    });

    it('returns color as-is if not hex or rgb', () => {
      global.getComputedStyle = vi.fn().mockImplementation(() => ({
        getPropertyValue: vi.fn().mockReturnValue('blue'),
      }));

      const color = getThemeColor('primary');
      expect(color).toBe('blue');
    });

    it('uses default opacity of 1', () => {
      const color = getThemeColor('primary');
      expect(color).toBe('rgba(59, 130, 246, 1)');
    });
  });

  describe('getChartTheme', () => {
    it('returns dark theme configuration', () => {
      document.documentElement.setAttribute('data-theme', 'dark');

      const theme = getChartTheme();

      expect(theme.color.text).toBe('rgba(200, 200, 200, 1)');
      expect(theme.color.grid).toBe('rgba(255, 255, 255, 0.1)');
      expect(theme.tooltip.backgroundColor).toBe('rgba(55, 65, 81, 0.9)');
      expect(theme.tooltip.borderColor).toBe('rgba(255, 255, 255, 0.2)');
      expect(theme.tooltip.titleColor).toBe('rgba(200, 200, 200, 1)');
      expect(theme.tooltip.bodyColor).toBe('rgba(200, 200, 200, 1)');

      document.documentElement.removeAttribute('data-theme');
    });

    it('returns light theme configuration', () => {
      document.documentElement.removeAttribute('data-theme');

      const theme = getChartTheme();

      expect(theme.color.text).toBe('rgba(55, 65, 81, 1)');
      expect(theme.color.grid).toBe('rgba(0, 0, 0, 0.1)');
      expect(theme.tooltip.backgroundColor).toBe('rgba(255, 255, 255, 0.9)');
      expect(theme.tooltip.borderColor).toBe('rgba(0, 0, 0, 0.2)');
      expect(theme.tooltip.titleColor).toBe('rgba(55, 65, 81, 1)');
      expect(theme.tooltip.bodyColor).toBe('rgba(55, 65, 81, 1)');
    });

    it('includes all tooltip properties', () => {
      const theme = getChartTheme();

      expect(theme.tooltip.borderWidth).toBe(1);
      expect(theme.tooltip.padding).toBe(10);
      expect(theme.tooltip.displayColors).toBe(false);
      expect(theme.tooltip.cornerRadius).toBe(6);
    });
  });

  describe('generateColorPalette', () => {
    it('generates exact number of colors when count is less than base colors', () => {
      const colors = generateColorPalette(5);
      expect(colors).toHaveLength(5);
      expect(colors[0]).toBe('rgba(59, 130, 246, 1)');
      expect(colors[4]).toBe('rgba(139, 92, 246, 1)');
    });

    it('generates colors with custom alpha', () => {
      const colors = generateColorPalette(3, 0.5);
      expect(colors).toHaveLength(3);
      expect(colors[0]).toBe('rgba(59, 130, 246, 0.5)');
      expect(colors[1]).toBe('rgba(16, 185, 129, 0.5)');
      expect(colors[2]).toBe('rgba(245, 158, 11, 0.5)');
    });

    it('generates more colors than base when needed', () => {
      const colors = generateColorPalette(15, 1);
      expect(colors).toHaveLength(15);
      // First 10 should be base colors
      expect(colors[0]).toBe('rgba(59, 130, 246, 1)');
      expect(colors[9]).toBe('rgba(249, 115, 22, 1)');
      // Next ones should be variations with reduced alpha
      expect(colors[10]).toContain('0.8)');
    });

    it('returns all base colors when count equals base colors length', () => {
      const colors = generateColorPalette(10);
      expect(colors).toHaveLength(10);
    });
  });

  describe('createChartOptions', () => {
    beforeEach(() => {
      document.documentElement.removeAttribute('data-theme');
    });

    it('creates base options for bar chart', () => {
      const options = createChartOptions('bar');

      expect(options.responsive).toBe(true);
      expect(options.maintainAspectRatio).toBe(false);
      expect(options.plugins?.legend?.labels?.color).toBe('rgba(55, 65, 81, 1)');
      expect(options.scales?.x).toBeDefined();
      expect(options.scales?.y).toBeDefined();
    });

    it('creates base options for line chart', () => {
      const options = createChartOptions('line');

      expect(options.scales?.x).toBeDefined();
      expect(options.scales?.y).toBeDefined();
    });

    it('creates base options for pie chart without scales', () => {
      const options = createChartOptions('pie');

      expect(options.responsive).toBe(true);
      expect(options.scales).toBeUndefined();
    });

    it('merges custom options', () => {
      const customOptions = {
        plugins: {
          title: {
            display: true,
            text: 'Custom Chart',
          },
        },
        animation: {
          duration: 2000,
        },
      };

      const options = createChartOptions('bar', customOptions);

      expect(options.plugins?.title?.display).toBe(true);
      expect(options.plugins?.title?.text).toBe('Custom Chart');
      expect((options.animation as { duration?: number }).duration).toBe(2000);
      // Should still have theme options
      expect(options.plugins?.legend?.labels?.color).toBe('rgba(55, 65, 81, 1)');
    });

    it('preserves custom scales when provided', () => {
      const customOptions = {
        scales: {
          x: {
            beginAtZero: false,
          },
          y: {
            max: 100,
          },
        },
      };

      const options = createChartOptions('bar', customOptions);

      expect((options.scales?.x as { beginAtZero?: boolean }).beginAtZero).toBe(false);
      expect(options.scales?.y?.max).toBe(100);
    });
  });

  describe('formatChartNumber', () => {
    it('formats numbers with thousand separators', () => {
      expect(formatChartNumber(1000)).toBe('1,000');
      expect(formatChartNumber(1000000)).toBe('1,000,000');
      expect(formatChartNumber(1234567.89)).toBe('1,234,567.89');
    });

    it('handles small numbers', () => {
      expect(formatChartNumber(0)).toBe('0');
      expect(formatChartNumber(123)).toBe('123');
      expect(formatChartNumber(-500)).toBe('-500');
    });

    it('handles decimal numbers', () => {
      expect(formatChartNumber(1234.56)).toBe('1,234.56');
      expect(formatChartNumber(0.123)).toBe('0.123');
    });
  });

  describe('createTooltipCallback', () => {
    const mockContext = {
      dataset: { label: 'Sales' },
      parsed: { y: 1234.56 },
    };

    it('formats as number by default', () => {
      const callback = createTooltipCallback();
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const result = callback(mockContext as any);
      expect(result).toBe('Sales: 1,234.56');
    });

    it('formats as percentage', () => {
      const callback = createTooltipCallback('percentage');
      const percentContext = { ...mockContext, parsed: { y: 85.67 } };
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const result = callback(percentContext as any);
      expect(result).toBe('Sales: 85.7%');
    });

    it('formats as currency', () => {
      const callback = createTooltipCallback('currency');
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const result = callback(mockContext as any);
      expect(result).toBe('Sales: $1,234.56');
    });

    it('adds prefix and suffix', () => {
      const callback = createTooltipCallback('number', '~', ' units');
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const result = callback(mockContext as any);
      expect(result).toBe('Sales: ~1,234.56 units');
    });

    it('handles context without y value', () => {
      const callback = createTooltipCallback();
      const pieContext = {
        dataset: { label: 'Category' },
        parsed: 42,
      };
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const result = callback(pieContext as any);
      expect(result).toBe('Category: 42');
    });

    it('handles context without label', () => {
      const callback = createTooltipCallback();
      const noLabelContext = {
        dataset: {},
        parsed: { y: 100 },
      };
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const result = callback(noLabelContext as any);
      expect(result).toBe('100');
    });
  });
});
