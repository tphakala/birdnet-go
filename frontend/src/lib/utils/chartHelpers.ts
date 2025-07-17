import type { ChartOptions, TooltipItem } from 'chart.js';

export interface ChartTheme {
  color: {
    text: string;
    grid: string;
  };
  tooltip: {
    backgroundColor: string;
    borderColor: string;
    borderWidth: number;
    titleColor: string;
    bodyColor: string;
    padding: number;
    displayColors: boolean;
    cornerRadius: number;
  };
}

/**
 * Get theme color from CSS variables
 */
export function getThemeColor(colorName: string, opacity = 1): string {
  const color = window
    .getComputedStyle(document.documentElement)
    .getPropertyValue(`--${colorName}`)
    .trim();

  if (color.startsWith('#')) {
    const r = parseInt(color.slice(1, 3), 16);
    const g = parseInt(color.slice(3, 5), 16);
    const b = parseInt(color.slice(5, 7), 16);
    return `rgba(${r}, ${g}, ${b}, ${opacity})`;
  }

  if (color.startsWith('rgb')) {
    if (color.startsWith('rgba')) {
      return color.replace(/rgba\((.+?),\s*[\d.]+\)/, `rgba($1, ${opacity})`);
    }
    return color.replace(/rgb\((.+?)\)/, `rgba($1, ${opacity})`);
  }

  return color;
}

/**
 * Get chart theme configuration based on current theme
 */
export function getChartTheme(): ChartTheme {
  const currentTheme = document.documentElement.getAttribute('data-theme');
  let textColor: string;
  let gridColor: string;
  let tooltipBgColor: string;
  let tooltipBorderColor: string;

  if (currentTheme === 'dark') {
    textColor = 'rgba(200, 200, 200, 1)';
    gridColor = 'rgba(255, 255, 255, 0.1)';
    tooltipBgColor = 'rgba(55, 65, 81, 0.9)';
    tooltipBorderColor = 'rgba(255, 255, 255, 0.2)';
  } else {
    textColor = 'rgba(55, 65, 81, 1)';
    gridColor = 'rgba(0, 0, 0, 0.1)';
    tooltipBgColor = 'rgba(255, 255, 255, 0.9)';
    tooltipBorderColor = 'rgba(0, 0, 0, 0.2)';
  }

  return {
    color: {
      text: textColor,
      grid: gridColor,
    },
    tooltip: {
      backgroundColor: tooltipBgColor,
      borderColor: tooltipBorderColor,
      borderWidth: 1,
      titleColor: textColor,
      bodyColor: textColor,
      padding: 10,
      displayColors: false,
      cornerRadius: 6,
    },
  };
}

/**
 * Generate a color palette for charts
 */
export function generateColorPalette(count: number, alpha = 1): string[] {
  const baseColors = [
    `rgba(59, 130, 246, ${alpha})`, // Blue
    `rgba(16, 185, 129, ${alpha})`, // Green
    `rgba(245, 158, 11, ${alpha})`, // Orange
    `rgba(236, 72, 153, ${alpha})`, // Pink
    `rgba(139, 92, 246, ${alpha})`, // Purple
    `rgba(239, 68, 68, ${alpha})`, // Red
    `rgba(20, 184, 166, ${alpha})`, // Teal
    `rgba(234, 179, 8, ${alpha})`, // Yellow
    `rgba(99, 102, 241, ${alpha})`, // Indigo
    `rgba(249, 115, 22, ${alpha})`, // Orange-red
  ];

  if (count <= baseColors.length) {
    return baseColors.slice(0, count);
  } else {
    const palette = [...baseColors];
    while (palette.length < count) {
      const newAlpha = alpha * 0.8;
      const variations = baseColors.map(color => color.replace(`${alpha})`, `${newAlpha})`));
      palette.push(...variations);
    }
    return palette.slice(0, count);
  }
}

/**
 * Create default chart options with theme support
 */
export function createChartOptions(
  type: 'bar' | 'line' | 'pie' | 'doughnut' | 'radar' | 'polarArea',
  customOptions: ChartOptions = {}
): ChartOptions {
  const theme = getChartTheme();

  const baseOptions: ChartOptions = {
    responsive: true,
    maintainAspectRatio: false,
    plugins: {
      legend: {
        labels: {
          color: theme.color.text,
        },
      },
      tooltip: theme.tooltip,
    },
  };

  // Add scale options for charts that use them
  if (type === 'bar' || type === 'line' || type === 'radar') {
    baseOptions.scales = {
      x: {
        ticks: {
          color: theme.color.text,
        },
        grid: {
          color: theme.color.grid,
        },
      },
      y: {
        ticks: {
          color: theme.color.text,
        },
        grid: {
          color: theme.color.grid,
        },
      },
    };
  }

  // Merge with custom options
  return {
    ...baseOptions,
    ...customOptions,
    plugins: {
      ...baseOptions.plugins,
      ...customOptions.plugins,
    },
    scales: customOptions.scales ?? baseOptions.scales,
  };
}

/**
 * Format number with thousand separators
 */
export function formatChartNumber(value: number): string {
  return new Intl.NumberFormat('en-US').format(value);
}

/**
 * Create tooltip callback for formatting values
 */
export function createTooltipCallback(
  format: 'number' | 'percentage' | 'currency' = 'number',
  prefix = '',
  suffix = ''
) {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return function (context: TooltipItem<any>) {
    let label = context.dataset.label ?? '';
    if (label) {
      label += ': ';
    }

    const value = context.parsed.y ?? context.parsed;
    let formattedValue: string;

    switch (format) {
      case 'percentage':
        formattedValue = `${value.toFixed(1)}%`;
        break;
      case 'currency':
        formattedValue = new Intl.NumberFormat('en-US', {
          style: 'currency',
          currency: 'USD',
        }).format(value);
        break;
      case 'number':
      default:
        formattedValue = formatChartNumber(value);
        break;
    }

    return `${label}${prefix}${formattedValue}${suffix}`;
  };
}
