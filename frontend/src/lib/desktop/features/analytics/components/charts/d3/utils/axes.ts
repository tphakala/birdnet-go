// D3 axis utilities for analytics charts
import { axisBottom, axisLeft, axisRight, axisTop } from 'd3-axis';
import { getLocale } from '$lib/i18n';
import type { Axis, AxisDomain, AxisScale } from 'd3-axis';
import type { ScaleLinear, ScaleTime } from 'd3-scale';
import type { Selection } from 'd3-selection';
import type { AxisTheme } from './theme';

export interface AxisConfig {
  scale: ScaleLinear<number, number> | ScaleTime<number, number> | AxisScale<AxisDomain>;
  orientation: 'top' | 'right' | 'bottom' | 'left';
  tickCount?: number;
  tickFormat?: (d: AxisDomain, i: number) => string;
  tickSize?: number;
  tickPadding?: number;
  label?: string;
}

/**
 * Create and configure a D3 axis
 */
export function createAxis(config: AxisConfig): Axis<AxisDomain> {
  const factories: Record<
    'top' | 'right' | 'bottom' | 'left',
    (s: AxisScale<AxisDomain>) => Axis<AxisDomain>
  > = {
    top: axisTop,
    right: axisRight,
    bottom: axisBottom,
    left: axisLeft,
  };

  const axis = factories[config.orientation](config.scale as AxisScale<AxisDomain>);

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

/**
 * Create hour-specific axis formatter (0-23 hours)
 * Uses 24-hour format by default, configurable via parameter
 */
export function createHourAxisFormatter(use24Hour = true): (d: number) => string {
  return (d: number) => {
    const hour = Math.max(0, Math.min(23, Math.round(d)));

    if (use24Hour) {
      return `${hour.toString().padStart(2, '0')}:00`;
    }

    // Localized 12-hour label; UTC prevents tz offset
    const dt = new Date(Date.UTC(1970, 0, 1, hour, 0, 0));
    return new Intl.DateTimeFormat(undefined, {
      hour: 'numeric',
      hour12: true,
      timeZone: 'UTC',
    }).format(dt);
  };
}

// Constants for date-range bucketing.
const MS_PER_DAY = 86400000;
const WEEK_THRESHOLD = 7;
const YEAR_THRESHOLD = 365;

/**
 * Pick the appropriate date-axis bucket for a daily-granularity time domain.
 *
 * NOTE: This intentionally NEVER returns 'day'. The 'day' bucket maps to a
 * clock-time format (%H:%M) via createDateAxisFormatter, which is only correct
 * for intra-day (hourly) data. The analytics charts that use this helper plot
 * one point per calendar day, so a short (<= 7 day) span must still show date
 * labels, not "00:00 00:00 ...". A 7-day or shorter span therefore uses 'week'
 * (weekday + day), longer spans use 'month', and spans over a year use 'year'.
 */
export function pickDateRangeBucket(domain: [Date, Date]): 'day' | 'week' | 'month' | 'year' {
  const days = (domain[1].getTime() - domain[0].getTime()) / MS_PER_DAY;
  if (days <= WEEK_THRESHOLD) return 'week';
  if (days <= YEAR_THRESHOLD) return 'month';
  return 'year';
}

// Two-digit zero-padded day-of-month, matching the previous D3 '%d' token.
function padDay(d: Date): string {
  return d.getDate().toString().padStart(2, '0');
}

/**
 * Create date axis formatter for different time ranges.
 *
 * Weekday and month names are localized to the active app locale via
 * Intl.DateTimeFormat (e.g. "Sun 24" -> "Dom 24" in Portuguese). The
 * weekday-/month-first ordering and zero-padded day of the original D3
 * '%a %d' / '%b %d' / '%b %Y' formats are preserved by composing the parts
 * manually, so only the translated names change, not the layout.
 */
export function createDateAxisFormatter(
  range: 'day' | 'week' | 'month' | 'year',
  opts: { use24Hour?: boolean } = {}
): (d: Date) => string {
  const use24Hour = opts.use24Hour ?? true;
  const locale = getLocale();

  switch (range) {
    case 'day': {
      // Clock time (e.g. "14:30" or "02:30 PM"), locale-aware.
      const time = new Intl.DateTimeFormat(locale, {
        hour: '2-digit',
        minute: '2-digit',
        hour12: !use24Hour,
      });
      return (d: Date) => time.format(d);
    }
    case 'week': {
      // Localized weekday abbreviation + day (e.g. "Sun 24" / "Dom 24").
      const weekday = new Intl.DateTimeFormat(locale, { weekday: 'short' });
      return (d: Date) => `${weekday.format(d)} ${padDay(d)}`;
    }
    case 'year': {
      // Localized month abbreviation + year (e.g. "Oct 2025").
      const month = new Intl.DateTimeFormat(locale, { month: 'short' });
      return (d: Date) => `${month.format(d)} ${d.getFullYear()}`;
    }
    case 'month':
    default: {
      // Localized month abbreviation + day (e.g. "Oct 01").
      const month = new Intl.DateTimeFormat(locale, { month: 'short' });
      return (d: Date) => `${month.format(d)} ${padDay(d)}`;
    }
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
      .tickSizeOuter(0)
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

    // Outer ticks suppressed via tickSizeOuter(0)
  }

  // Horizontal grid lines
  if (config.yScale) {
    const yAxis = axisLeft(config.yScale as AxisScale<AxisDomain>)
      .tickSize(-config.width)
      .tickSizeOuter(0)
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

    // Outer ticks suppressed via tickSizeOuter(0)
  }
}
