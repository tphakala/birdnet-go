// D3 axis utilities for analytics charts
import * as d3 from 'd3';

export interface AxisConfig {
  scale:
    | d3.ScaleLinear<number, number>
    | d3.ScaleTime<number, number>
    | d3.AxisScale<d3.AxisDomain>;
  orientation: 'top' | 'right' | 'bottom' | 'left';
  tickCount?: number;
  tickFormat?: (d: d3.AxisDomain, i: number) => string;
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
export function createAxis(config: AxisConfig): d3.Axis<d3.AxisDomain> {
  let axis: d3.Axis<d3.AxisDomain>;

  switch (config.orientation) {
    case 'top':
      axis = d3.axisTop(config.scale as d3.AxisScale<d3.AxisDomain>);
      break;
    case 'right':
      axis = d3.axisRight(config.scale as d3.AxisScale<d3.AxisDomain>);
      break;
    case 'bottom':
      axis = d3.axisBottom(config.scale as d3.AxisScale<d3.AxisDomain>);
      break;
    case 'left':
      axis = d3.axisLeft(config.scale as d3.AxisScale<d3.AxisDomain>);
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
  axisGroup: d3.Selection<SVGGElement, unknown, null, undefined>,
  theme: AxisTheme,
  showGrid = false
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

  // Add grid lines if requested
  if (showGrid) {
    axisGroup
      .selectAll('.tick line')
      .style('stroke', theme.gridColor)
      .style('stroke-dasharray', '2,2')
      .style('opacity', 0.3);
  }
}

/**
 * Add axis label
 */
export function addAxisLabel(
  container: d3.Selection<SVGGElement, unknown, null, undefined>,
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
    .style('fill', theme.color)
    .style('font-size', theme.fontSize)
    .style('font-family', theme.fontFamily)
    .style('font-weight', 'bold')
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
      return (d: Date) => d3.timeFormat('%H:%M')(d);
    case 'week':
      return (d: Date) => d3.timeFormat('%a %d')(d);
    case 'month':
      return (d: Date) => d3.timeFormat('%b %d')(d);
    case 'year':
      return (d: Date) => d3.timeFormat('%b %Y')(d);
    default:
      return (d: Date) => d3.timeFormat('%b %d')(d);
  }
}

/**
 * Create grid lines for a chart
 */
export function createGridLines(
  container: d3.Selection<SVGGElement, unknown, null, undefined>,
  config: {
    xScale?: d3.AxisScale<d3.AxisDomain>;
    yScale?: d3.AxisScale<d3.AxisDomain>;
    width: number;
    height: number;
  },
  theme: AxisTheme
): void {
  // Vertical grid lines
  if (config.xScale) {
    const xAxis = d3
      .axisBottom(config.xScale)
      .tickSize(-config.height)
      .tickFormat(() => '');

    container
      .append('g')
      .attr('class', 'grid grid-x')
      .attr('transform', `translate(0,${config.height})`)
      .call(xAxis)
      .selectAll('line')
      .style('stroke', theme.gridColor)
      .style('stroke-dasharray', '2,2')
      .style('opacity', 0.3);
  }

  // Horizontal grid lines
  if (config.yScale) {
    const yAxis = d3
      .axisLeft(config.yScale)
      .tickSize(-config.width)
      .tickFormat(() => '');

    container
      .append('g')
      .attr('class', 'grid grid-y')
      .call(yAxis)
      .selectAll('line')
      .style('stroke', theme.gridColor)
      .style('stroke-dasharray', '2,2')
      .style('opacity', 0.3);
  }

  // Hide the domain line for grid
  container.selectAll('.grid .domain').style('display', 'none');
}
