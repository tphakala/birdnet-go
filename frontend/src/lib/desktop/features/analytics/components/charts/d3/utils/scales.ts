// D3 scale utilities for analytics charts
import * as d3 from 'd3';
import { generateSpeciesColors, getCurrentTheme } from './theme';

export interface LinearScaleConfig {
  domain: [number, number];
  range: [number, number];
}

export interface BandScaleConfig {
  domain: string[] | unknown[];
  range: [number, number];
  padding?: number;
}

export interface TimeScaleConfig {
  domain: [Date, Date];
  range: [number, number];
}

/**
 * Create a linear scale with nice ticks
 */
export function createLinearScale(config: LinearScaleConfig): d3.ScaleLinear<number, number> {
  // Validate domain for linear scale
  if (
    !Array.isArray(config.domain) ||
    typeof config.domain[0] !== 'number' ||
    typeof config.domain[1] !== 'number'
  ) {
    throw new TypeError('createLinearScale: domain must be [number, number]');
  }

  const scale = d3.scaleLinear().domain(config.domain).range(config.range).nice();

  return scale;
}

/**
 * Create a time scale for date-based charts
 */
export function createTimeScale(config: TimeScaleConfig): d3.ScaleTime<number, number> {
  return d3.scaleTime().domain(config.domain).range(config.range);
}

/**
 * Create a band scale for categorical data
 */
export function createBandScale(config: BandScaleConfig): d3.ScaleBand<string> {
  // Validate and coerce domain to string array
  if (!Array.isArray(config.domain)) {
    throw new TypeError('createBandScale: domain must be an array');
  }

  // Coerce all items to strings
  const validatedDomain = config.domain.map(item => String(item));

  const scale = d3
    .scaleBand()
    .domain(validatedDomain)
    .range(config.range)
    .padding(config.padding ?? 0.1);

  return scale;
}

/**
 * Create a color scale for species differentiation
 */
export function createSpeciesColorScale(species: string[]): d3.ScaleOrdinal<string, string> {
  // Use the theme's generateSpeciesColors function for consistent theming
  const theme = getCurrentTheme();
  const colors = generateSpeciesColors(species.length, theme);

  return d3.scaleOrdinal<string, string>().domain(species).range(colors);
}

/**
 * Get nice tick values for a numeric domain
 */
export function getNiceTicks(domain: [number, number], targetTicks = 5): number[] {
  const scale = d3.scaleLinear().domain(domain).nice();

  return scale.ticks(targetTicks);
}

/**
 * Format tick values based on the data type and range
 */
export function formatTick(value: number | Date, type: 'number' | 'time' | 'hour'): string {
  switch (type) {
    case 'number':
      return d3.format('.0f')(value as number);
    case 'time':
      return d3.timeFormat('%b %d')(value as Date);
    case 'hour': {
      const hour = value as number;
      return `${String(hour).padStart(2, '0')}:00`;
    }
    default:
      return String(value);
  }
}

/**
 * Create responsive scales that adjust to container size
 */
export interface ResponsiveScaleConfig {
  containerWidth: number;
  containerHeight: number;
  margin: { top: number; right: number; bottom: number; left: number };
}

export function createResponsiveScales(config: ResponsiveScaleConfig) {
  // Clamp inner dimensions to non-negative values to prevent negative ranges
  const innerWidth = Math.max(0, config.containerWidth - config.margin.left - config.margin.right);
  const innerHeight = Math.max(
    0,
    config.containerHeight - config.margin.top - config.margin.bottom
  );

  return {
    innerWidth,
    innerHeight,
    xRange: [0, innerWidth] as [number, number],
    yRange: [innerHeight, 0] as [number, number], // Inverted for SVG coordinate system
  };
}
