// D3 scale utilities for analytics charts
import * as d3 from 'd3';

export interface ScaleConfig {
  domain: [number, number] | string[];
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
export function createLinearScale(config: ScaleConfig): d3.ScaleLinear<number, number> {
  const scale = d3
    .scaleLinear()
    .domain(config.domain as [number, number])
    .range(config.range)
    .nice();

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
export function createBandScale(config: ScaleConfig): d3.ScaleBand<string> {
  const scale = d3
    .scaleBand()
    .domain(config.domain as string[])
    .range(config.range)
    .padding(config.padding ?? 0.1);

  return scale;
}

/**
 * Create a color scale for species differentiation
 */
export function createSpeciesColorScale(species: string[]): d3.ScaleOrdinal<string, string> {
  // Use D3's category10 colors, extended with more colors if needed
  const colors = [
    '#1f77b4',
    '#ff7f0e',
    '#2ca02c',
    '#d62728',
    '#9467bd',
    '#8c564b',
    '#e377c2',
    '#7f7f7f',
    '#bcbd22',
    '#17becf',
    '#aec7e8',
    '#ffbb78',
    '#98df8a',
    '#ff9896',
    '#c5b0d5',
    '#c49c94',
    '#f7b6d3',
    '#c7c7c7',
    '#dbdb8d',
    '#9edae5',
  ];

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
      return `${hour}:00`;
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
  const innerWidth = config.containerWidth - config.margin.left - config.margin.right;
  const innerHeight = config.containerHeight - config.margin.top - config.margin.bottom;

  return {
    innerWidth,
    innerHeight,
    xRange: [0, innerWidth] as [number, number],
    yRange: [innerHeight, 0] as [number, number], // Inverted for SVG coordinate system
  };
}
