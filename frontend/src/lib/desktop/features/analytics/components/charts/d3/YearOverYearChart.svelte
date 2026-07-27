<!--
  Year-over-year tracker.

  Cumulative detections so far this year versus the same calendar dates one year earlier, drawn as two
  lines (this year solid, last year dashed) with a shaded band filling the running gap between them. A
  band sitting above last year's line means the current year is ahead of last year's pace by that date.
  The backend emits one point per current-year calendar day from Jan 1 through the requested date, each
  carrying both cumulative counts and their delta, aligned by calendar (month, day) so a leap boundary
  lines up. Both series are monotonic non-decreasing by construction.

  A transparent overlay drives a shared crosshair + tooltip across the whole range; per-point dots are
  added only on short ranges (and make a single in-range day visible, since a line needs two points).
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { pointer } from 'd3-selection';
  import { line, area, curveMonotoneX } from 'd3-shape';
  import { bisector } from 'd3-array';
  import type { AxisDomain, AxisScale } from 'd3-axis';

  import BaseChart from './BaseChart.svelte';
  import { parseLocalDateString } from '$lib/utils/date';
  import { createTimeScale, createLinearScale } from './utils/scales';
  import {
    createAxis,
    styleAxis,
    addAxisLabel,
    createDateAxisFormatter,
    pickDateRangeBucket,
  } from './utils/axes';
  import { ChartTooltip } from './utils/interactions';
  import { type ChartTheme } from './utils/theme';
  import {
    peakCumulative,
    type YearOverYearData,
    type YearOverYearPoint,
  } from './utils/yearOverYear';
  import { t } from '$lib/i18n';

  interface Props {
    data: YearOverYearData;
    width?: number;
    height?: number;
    ariaLabel?: string;
  }

  let { data, width = 800, height = 400, ariaLabel }: Props = $props();

  // Layout / behavior constants.
  // MARGIN.left and Y_AXIS_LABEL_OFFSET are sized together so the rotated "axisCount" title
  // clears wide y-axis tick numbers (cumulative counts render as plain integers, no thousands
  // separator, up to 6 digits for a very active station's yearly total: ~45px at the 12px axis
  // font). D3's default axisLeft tick gap (tickSizeInner 6 + tickPadding 3) adds 9px, and the
  // rotated title itself is ~13px "thick" (ascent + descent); a few px of buffer on each side
  // gives 9 + 45 + 4 + 13 + 4 = 75, rounded up to 78 for margin.left, with an offset (64) that
  // keeps the title's pivot centered in that clearance. See BarChart.svelte's analogous
  // VERTICAL_VALUE_AXIS_LABEL_OFFSET for the same derivation against comma-formatted counts.
  const MARGIN = { top: 24, right: 18, bottom: 48, left: 78 };
  const MAX_X_TICKS = 8;
  const X_AXIS_LABEL_OFFSET = 38;
  const Y_AXIS_LABEL_OFFSET = 64;
  const BAND_OPACITY = 0.15;
  const DOT_RADIUS = 3;
  const FOCUS_RADIUS = 4;
  // Above this many days, a dot per point clutters the lines; the strokes alone carry the shape and the
  // hover overlay still provides per-day readouts.
  const MAX_DOTS_DAYS = 31;
  // Top headroom so the upper line never sits flush against the chart's top edge.
  const Y_HEADROOM_FRAC = 0.08;
  // One day in milliseconds, used to pad a single-point x-domain so it is never zero-width.
  const MS_PER_DAY = 24 * 60 * 60 * 1000;

  interface PlottedPoint extends YearOverYearPoint {
    dateObj: Date;
  }

  // Maps a point to its y-pixel for one series (this-year or last-year), so the dot helper is shared.
  type SeriesAccessor = (_point: PlottedPoint) => number;

  let tooltip: ChartTooltip | null = null;
  let chartContainer: HTMLDivElement | null = null;

  // Points with a valid parsed date, in input (ascending date) order.
  const parsedPoints = $derived.by<PlottedPoint[]>(() => {
    const out: PlottedPoint[] = [];
    for (const p of data.points) {
      const d = parseLocalDateString(p.date);
      if (d && !isNaN(d.getTime())) out.push({ ...p, dateObj: d });
    }
    return out;
  });

  // Screen-reader summary so the chart is not opaque to assistive tech (spec section 7).
  const summary = $derived.by(() => {
    if (parsedPoints.length === 0) return '';
    const last = parsedPoints[parsedPoints.length - 1];
    return t('analytics.advanced.charts.yearOverYear.summary', {
      monthDay: last.monthDay,
      currentYear: data.currentYear,
      thisYear: last.thisYear,
      previousYear: data.previousYear,
      lastYear: last.lastYear,
      delta: formatDelta(last.delta),
    });
  });

  function formatDelta(delta: number): string {
    return delta >= 0 ? `+${delta}` : String(delta);
  }

  const bisectDate = bisector<PlottedPoint, Date>(d => d.dateObj).left;

  let chartContext = $state<{
    svg: import('d3-selection').Selection<SVGSVGElement, unknown, null, undefined>;
    chartGroup: import('d3-selection').Selection<globalThis.SVGGElement, unknown, null, undefined>;
    innerWidth: number;
    innerHeight: number;
    theme: ChartTheme;
  } | null>(null);

  function drawChart(context: NonNullable<typeof chartContext>): void {
    const { chartGroup, innerWidth, innerHeight, theme } = context;
    // A redraw removes the hovered elements, so their mouseleave would never fire; hide first.
    tooltip?.hide();
    chartGroup.selectAll('*').remove();
    if (innerWidth <= 0 || innerHeight <= 0) return;

    const points = parsedPoints;
    const peak = peakCumulative(data);
    // ChartCard owns the empty / not-enough-data state; bail before drawing axes on an empty chart.
    if (points.length === 0 || peak <= 0) return;

    const minDate = points[0].dateObj;
    const maxDate = points[points.length - 1].dateObj;
    // A single in-range day (one point) makes minDate === maxDate, a zero-width domain that D3 maps to
    // NaN. The not-enough-data gate keys on detection counts (not the day count), so a single day with
    // enough detections reaches here; pad the domain by a day on each side to keep the scale defined.
    const xDomain: [Date, Date] =
      minDate.getTime() === maxDate.getTime()
        ? [new Date(minDate.getTime() - MS_PER_DAY), new Date(maxDate.getTime() + MS_PER_DAY)]
        : [minDate, maxDate];
    const xScale = createTimeScale({ domain: xDomain, range: [0, innerWidth] });

    // Count axis always starts at 0; add a little headroom so the top line clears the chart edge.
    const yTop = peak + Math.max(1, Math.ceil(peak * Y_HEADROOM_FRAC));
    const yScale = createLinearScale({ domain: [0, yTop], range: [innerHeight, 0] });

    // X axis: locale-aware date labels, bucketed by the span (week / month / year).
    const dateFmt = createDateAxisFormatter(pickDateRangeBucket([minDate, maxDate]));
    const xAxis = createAxis({
      scale: xScale as unknown as AxisScale<AxisDomain>,
      orientation: 'bottom',
      tickCount: MAX_X_TICKS,
      tickFormat: (d: AxisDomain) => dateFmt(d as Date),
    });
    const xAxisGroup = chartGroup
      .append('g')
      .attr('class', 'x-axis')
      .attr('transform', `translate(0,${innerHeight})`)
      .call(xAxis);
    styleAxis(xAxisGroup, theme.axis);

    // Y axis: integer detection counts (round so a small domain never shows fractional ticks).
    const yAxis = createAxis({
      scale: yScale as unknown as AxisScale<AxisDomain>,
      orientation: 'left',
      tickFormat: (d: AxisDomain) => String(Math.round(Number(d))),
    });
    const yAxisGroup = chartGroup.append('g').attr('class', 'y-axis').call(yAxis);
    styleAxis(yAxisGroup, theme.axis);

    addAxisLabel(
      chartGroup,
      {
        text: t('analytics.advanced.charts.yearOverYear.axisDate'),
        orientation: 'bottom',
        offset: X_AXIS_LABEL_OFFSET,
        width: innerWidth,
        height: innerHeight,
      },
      theme.axis
    );
    addAxisLabel(
      chartGroup,
      {
        text: t('analytics.advanced.charts.yearOverYear.axisCount'),
        orientation: 'left',
        offset: Y_AXIS_LABEL_OFFSET,
        width: innerWidth,
        height: innerHeight,
      },
      theme.axis
    );

    // Shaded delta band filling the running gap between the two cumulative lines.
    const bandGen = area<PlottedPoint>()
      .x(d => xScale(d.dateObj))
      .y0(d => yScale(d.lastYear))
      .y1(d => yScale(d.thisYear))
      .curve(curveMonotoneX);
    chartGroup
      .append('path')
      .datum(points)
      .attr('class', 'yoy-band')
      .attr('fill', theme.accent)
      .attr('fill-opacity', BAND_OPACITY)
      .attr('stroke', 'none')
      .attr('d', bandGen);

    // Previous-year line (dashed, muted) drawn first so the current-year line reads on top.
    const lastLineGen = line<PlottedPoint>()
      .x(d => xScale(d.dateObj))
      .y(d => yScale(d.lastYear))
      .curve(curveMonotoneX);
    chartGroup
      .append('path')
      .datum(points)
      .attr('class', 'yoy-line-last')
      .attr('fill', 'none')
      .attr('stroke', theme.secondary)
      .attr('stroke-width', 2)
      .attr('stroke-dasharray', '5 4')
      .attr('d', lastLineGen);

    // Current-year line (solid, primary).
    const thisLineGen = line<PlottedPoint>()
      .x(d => xScale(d.dateObj))
      .y(d => yScale(d.thisYear))
      .curve(curveMonotoneX);
    chartGroup
      .append('path')
      .datum(points)
      .attr('class', 'yoy-line-this')
      .attr('fill', 'none')
      .attr('stroke', theme.primary)
      .attr('stroke-width', 2)
      .attr('d', thisLineGen);

    // Per-point dots only on short ranges; on a wide range the dense dots clutter the lines. These are
    // purely visual (the overlay below handles hover) and make a single in-range day visible.
    if (points.length <= MAX_DOTS_DAYS) {
      drawDots(chartGroup, points, 'yoy-dots-this', d => yScale(d.thisYear), theme.primary, xScale);
      drawDots(
        chartGroup,
        points,
        'yoy-dots-last',
        d => yScale(d.lastYear),
        theme.secondary,
        xScale
      );
    }

    drawHoverOverlay(chartGroup, points, xScale, yScale, innerWidth, innerHeight, theme);
  }

  function drawDots(
    group: NonNullable<typeof chartContext>['chartGroup'],
    points: PlottedPoint[],
    className: string,
    cy: SeriesAccessor,
    color: string,
    xScale: ReturnType<typeof createTimeScale>
  ): void {
    group
      .append('g')
      .attr('class', className)
      .selectAll('circle')
      .data(points)
      .enter()
      .append('circle')
      .attr('cx', (d: PlottedPoint) => xScale(d.dateObj))
      .attr('cy', (d: PlottedPoint) => cy(d))
      .attr('r', DOT_RADIUS)
      .style('fill', color)
      .style('opacity', 0.7);
  }

  function drawHoverOverlay(
    group: NonNullable<typeof chartContext>['chartGroup'],
    points: PlottedPoint[],
    xScale: ReturnType<typeof createTimeScale>,
    yScale: ReturnType<typeof createLinearScale>,
    innerWidth: number,
    innerHeight: number,
    theme: ChartTheme
  ): void {
    const overlayNode = group.node();

    // Crosshair guide + per-series focus markers sit above the transparent overlay (pointer-events
    // none) so they never steal the mouse from it.
    const guide = group
      .append('line')
      .attr('class', 'yoy-guide')
      .attr('y1', 0)
      .attr('y2', innerHeight)
      .style('stroke', theme.axis.color)
      .style('stroke-dasharray', '3 3')
      .style('opacity', 0)
      .style('pointer-events', 'none');
    const focusThis = group
      .append('circle')
      .attr('class', 'yoy-focus-this')
      .attr('r', FOCUS_RADIUS)
      .style('fill', theme.primary)
      .style('opacity', 0)
      .style('pointer-events', 'none');
    const focusLast = group
      .append('circle')
      .attr('class', 'yoy-focus-last')
      .attr('r', FOCUS_RADIUS)
      .style('fill', theme.secondary)
      .style('opacity', 0)
      .style('pointer-events', 'none');

    group
      .append('rect')
      .attr('class', 'yoy-overlay')
      .attr('width', innerWidth)
      .attr('height', innerHeight)
      .style('fill', 'none')
      .style('pointer-events', 'all')
      .on('mousemove', (event: MouseEvent) => {
        const [mx] = pointer(event, overlayNode);
        const x0 = xScale.invert(mx);
        const idx = bisectDate(points, x0, 1);
        // Array.at avoids variable bracket-indexing (no detect-object-injection lint) and yields
        // undefined past the ends, which the nearest-of-two pick below handles.
        const lo = points.at(idx - 1);
        const hi = points.at(idx);
        const p =
          !hi || (lo && x0.getTime() - lo.dateObj.getTime() <= hi.dateObj.getTime() - x0.getTime())
            ? lo
            : hi;
        if (!p) return;
        const cx = xScale(p.dateObj);
        guide.attr('x1', cx).attr('x2', cx).style('opacity', 0.6);
        focusThis.attr('cx', cx).attr('cy', yScale(p.thisYear)).style('opacity', 1);
        focusLast.attr('cx', cx).attr('cy', yScale(p.lastYear)).style('opacity', 1);
        showTooltip(event, p);
      })
      .on('mouseleave', () => {
        guide.style('opacity', 0);
        focusThis.style('opacity', 0);
        focusLast.style('opacity', 0);
        tooltip?.hide();
      });
  }

  function showTooltip(event: MouseEvent, d: PlottedPoint): void {
    tooltip?.show({
      title: d.date,
      items: [
        { label: String(data.currentYear), value: String(d.thisYear) },
        { label: String(data.previousYear), value: String(d.lastYear) },
        {
          label: t('analytics.advanced.charts.yearOverYear.tooltipDelta'),
          value: formatDelta(d.delta),
        },
      ],
      x: event.clientX,
      y: event.clientY,
    });
  }

  $effect(() => {
    if (chartContext) {
      drawChart(chartContext);
    }
  });

  onMount(() => {
    if (chartContainer) {
      tooltip = new ChartTooltip(chartContainer);
    }
  });

  onDestroy(() => {
    tooltip?.destroy();
  });
</script>

<div class="yoy-chart" bind:this={chartContainer}>
  <BaseChart
    {width}
    {height}
    margin={MARGIN}
    responsive={true}
    ariaLabel={ariaLabel ?? t('analytics.advanced.charts.yearOverYear.ariaLabel')}
  >
    {#snippet children(context)}
      {((chartContext = context), '')}
    {/snippet}
  </BaseChart>
  <ul class="yoy-legend">
    <li class="yoy-legend-item">
      <span class="yoy-swatch yoy-swatch-this" aria-hidden="true"></span>
      {t('analytics.advanced.charts.yearOverYear.legendThis', { year: data.currentYear })}
    </li>
    <li class="yoy-legend-item">
      <span class="yoy-swatch yoy-swatch-last" aria-hidden="true"></span>
      {t('analytics.advanced.charts.yearOverYear.legendLast', { year: data.previousYear })}
    </li>
  </ul>
  {#if summary}
    <p class="sr-only" data-testid="yoy-summary">{summary}</p>
  {/if}
</div>

<style>
  .yoy-chart {
    width: 100%;
    height: 100%;
    min-height: 320px;
  }

  .yoy-legend {
    display: flex;
    gap: 1rem;
    justify-content: center;
    margin: 0;
    padding: 0;
    list-style: none;
    font-size: 0.875rem;
    color: var(--color-base-content);
  }

  .yoy-legend-item {
    display: inline-flex;
    align-items: center;
    gap: 0.375rem;
  }

  .yoy-swatch {
    display: inline-block;
    width: 1.25rem;
    height: 0;
    border-top-width: 2px;
    border-top-style: solid;
  }

  .yoy-swatch-this {
    border-top-color: var(--color-primary);
  }

  .yoy-swatch-last {
    border-top-style: dashed;
    border-top-color: var(--color-secondary);
  }
</style>
