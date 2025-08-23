// D3 axis utilities for analytics charts
import { axisBottom, axisLeft, axisRight, axisTop } from 'd3-axis';
import { timeFormat } from 'd3-time-format';
import type { Axis, AxisDomain, AxisScale } from 'd3-axis';
import type { ScaleLinear, ScaleTime } from 'd3-scale';
import { select } from 'd3-selection';
import type { Selection } from 'd3-selection';

export interface AxisConfig {
  scale: ScaleLinear<number, number> | ScaleTime<number, number> | AxisScale<AxisDomain>;
  orientation: 'top' | 'right' | 'bottom' | 'left';
  tickCount?: number;
  tickFormat?: (d: AxisDomain, i: number) => string;
  tickSize?: number;
  tickPadding?: number;
  label?: string;
}

export interface AxisTheme {
  color: string;
  fontSize: string;
  fontFamily: string;
  strokeWidth: number;
  gridColor: string;
}

/**
 * Create and configure a D3 axis
 */
export function createAxis(config: AxisConfig): Axis<AxisDomain> {
  let axis: Axis<AxisDomain>;

  switch (config.orientation) {
    case 'top':
      axis = axisTop(config.scale as AxisScale<AxisDomain>);
      break;
    case 'right':
      axis = axisRight(config.scale as AxisScale<AxisDomain>);
      break;
    case 'bottom':
      axis = axisBottom(config.scale as AxisScale<AxisDomain>);
      break;
    case 'left':
      axis = axisLeft(config.scale as AxisScale<AxisDomain>);
      break;
  }

  if (config.tickCount !== undefined) {
    axis.ticks(config.tickCount);
  }

  if (config.tickFormat) {
    axis.tickFormat(config.tickFormat);
  }

  if (config.tickSize !== undefined) {
    axis.tickSize(config.tickSize);
  }

  if (config.tickPadding !== undefined) {
    axis.tickPadding(config.tickPadding);
  }

  return axis;
}

/**
 * Apply theme styling to an axis group
 */
export function styleAxis(
  axisGroup: Selection<SVGGElement, unknown, null, undefined>,
  theme: AxisTheme
): void {
  // Style the axis line and ticks
  axisGroup
    .selectAll('.domain, .tick line')
    .style('stroke', theme.color)
    .style('stroke-width', theme.strokeWidth);

  // Style the tick text
  axisGroup
    .selectAll('.tick text')
    .style('fill', theme.color)
    .style('font-size', theme.fontSize)
    .style('font-family', theme.fontFamily);
}

/**
 * Add axis label
 */
export function addAxisLabel(
  container: Selection<SVGGElement, unknown, null, undefined>,
  config: {
    text: string;
    orientation: 'top' | 'right' | 'bottom' | 'left';
    offset: number;
    width: number;
    height: number;
  },
  theme: AxisTheme
): void {
  const { text, orientation, offset, width, height } = config;

  let x: number,
    y: number,
    rotation = 0;

  switch (orientation) {
    case 'bottom':
      x = width / 2;
      y = height + offset;
      break;
    case 'left':
      x = -offset;
      y = height / 2;
      rotation = -90;
      break;
    case 'top':
      x = width / 2;
      y = -offset;
      break;
    case 'right':
      x = width + offset;
      y = height / 2;
      rotation = 90;
      break;
  }

  container
    .append('text')
    .attr('class', 'axis-label')
    .attr('text-anchor', 'middle')
    .attr('transform', `translate(${x},${y}) rotate(${rotation})`)
    .attr('aria-hidden', 'true')
    .style('fill', theme.color)
    .style('font-size', theme.fontSize)
    .style('font-family', theme.fontFamily)
    .style('font-weight', 'bold')
    .style('pointer-events', 'none')
    .text(text);
}

// Configurable constant for time format
const USE_24_HOUR_FORMAT: boolean = true; // Set to false for 12-hour format

/**
 * Create hour-specific axis formatter (0-23 hours)
 * Uses 24-hour format by default, configurable via USE_24_HOUR_FORMAT
 */
export function createHourAxisFormatter(): (d: number) => string {
  return (d: number) => {
    const hour = d as number;

    if (USE_24_HOUR_FORMAT) {
      // 24-hour format: "00:00", "13:00", "23:00"
      return `${hour.toString().padStart(2, '0')}:00`;
    } else {
      // 12-hour format with AM/PM
      if (hour === 0) return '12 AM';
      if (hour === 12) return '12 PM';
      if (hour < 12) return `${hour} AM`;
      return `${hour - 12} PM`;
    }
  };
}

/**
 * Create date axis formatter for different time ranges
 */
export function createDateAxisFormatter(
  range: 'day' | 'week' | 'month' | 'year'
): (d: Date) => string {
  switch (range) {
    case 'day':
      return (d: Date) => timeFormat('%H:%M')(d);
    case 'week':
      return (d: Date) => timeFormat('%a %d')(d);
    case 'month':
      return (d: Date) => timeFormat('%b %d')(d);
    case 'year':
      return (d: Date) => timeFormat('%b %Y')(d);
    default:
      return (d: Date) => timeFormat('%b %d')(d);
  }
}

/**
 * Create grid lines for a chart
 */
export function createGridLines(
  container: Selection<SVGGElement, unknown, null, undefined>,
  config: {
    xScale?: ScaleLinear<number, number> | ScaleTime<number, number> | AxisScale<AxisDomain>;
    yScale?: ScaleLinear<number, number> | ScaleTime<number, number> | AxisScale<AxisDomain>;
    width: number;
    height: number;
  },
  theme: AxisTheme
): void {
  // Remove existing grids to prevent duplicates (idempotent)
  container.selectAll('.grid').remove();

  // Vertical grid lines
  if (config.xScale) {
    const xAxis = axisBottom(config.xScale as AxisScale<AxisDomain>)
      .tickSize(-config.height)
      .tickFormat(() => '');

    const xGridGroup = container
      .append('g')
      .attr('class', 'grid grid-x')
      .attr('transform', `translate(0,${config.height})`)
      .style('pointer-events', 'none'); // Non-interactive

    xGridGroup.call(xAxis);

    // Style grid lines
    xGridGroup
      .selectAll('line')
      .style('stroke', theme.gridColor)
      .style('stroke-dasharray', '2,2')
      .style('opacity', 0.3);

    // Hide domain line
    xGridGroup.select('.domain').style('display', 'none');

    // Remove outer tick lines (first and last) using D3 selection methods
    const xTicks = xGridGroup.selectAll('.tick');
    if (!xTicks.empty()) {
      xTicks.nodes().forEach((tick, index, array) => {
        if (index === 0 || index === array.length - 1) {
          select(tick).selectAll('line').remove();
        }
      });
    }
  }

  // Horizontal grid lines
  if (config.yScale) {
    const yAxis = axisLeft(config.yScale as AxisScale<AxisDomain>)
      .tickSize(-config.width)
      .tickFormat(() => '');

    const yGridGroup = container
      .append('g')
      .attr('class', 'grid grid-y')
      .style('pointer-events', 'none'); // Non-interactive

    yGridGroup.call(yAxis);

    // Style grid lines
    yGridGroup
      .selectAll('line')
      .style('stroke', theme.gridColor)
      .style('stroke-dasharray', '2,2')
      .style('opacity', 0.3);

    // Hide domain line
    yGridGroup.select('.domain').style('display', 'none');

    // Remove outer tick lines (first and last) using D3 selection methods
    const yTicks = yGridGroup.selectAll('.tick');
    if (!yTicks.empty()) {
      yTicks.nodes().forEach((tick, index, array) => {
        if (index === 0 || index === array.length - 1) {
          select(tick).selectAll('line').remove();
        }
      });
    }
  }
}
