<!--
  Species accumulation curve (biodiversity collector's curve).

  Cumulative count of distinct species first seen within the selected range, plotted per calendar day.
  The backend emits one point per day (a continuous, monotonic non-decreasing series), so the curve
  rises as new species appear and flattens toward an asymptote once the common species are exhausted.
  A still-climbing tail means the site is under-sampled; a flat tail means most species have been
  recorded. "First seen" is windowed (bounded to the selected range), not lifetime.

  Rendered as a filled area under a stroked line (curveMonotoneX) with a dashed reference line at the
  final/total species count. Per-point dots are only drawn for short ranges; on a wide range hundreds
  of overlapping dots on a steep line read as muddy, so the line alone carries the shape.
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { select } from 'd3-selection';
  import { line, area, curveMonotoneX } from 'd3-shape';
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
    finalCumulative,
    type AccumulationData,
    type AccumulationPoint,
  } from './utils/accumulation';
  import { t } from '$lib/i18n';

  interface Props {
    data: AccumulationData;
    width?: number;
    height?: number;
    ariaLabel?: string;
  }

  let { data, width = 800, height = 400, ariaLabel }: Props = $props();

  // Layout / behavior constants.
  const MARGIN = { top: 24, right: 18, bottom: 48, left: 60 };
  const MAX_X_TICKS = 8;
  const X_AXIS_LABEL_OFFSET = 38;
  const Y_AXIS_LABEL_OFFSET = 46;
  const AREA_OPACITY = 0.15;
  const DOT_RADIUS = 3;
  const DOT_IDLE_OPACITY = 0.7;
  // Above this many days, drawing a dot per point clutters a steep line; the stroke alone carries it.
  const MAX_DOTS_DAYS = 31;
  // Top headroom so the line and the asymptote label do not sit flush against the chart's top edge.
  const Y_HEADROOM_FRAC = 0.08;

  interface PlottedPoint extends AccumulationPoint {
    dateObj: Date;
  }

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
    return t('analytics.advanced.charts.accumulation.summary', {
      species: finalCumulative(data),
      days: parsedPoints.length,
    });
  });

  let chartContext = $state<{
    svg: import('d3-selection').Selection<SVGSVGElement, unknown, null, undefined>;
    chartGroup: import('d3-selection').Selection<globalThis.SVGGElement, unknown, null, undefined>;
    innerWidth: number;
    innerHeight: number;
    theme: ChartTheme;
  } | null>(null);

  function drawChart(context: NonNullable<typeof chartContext>): void {
    const { chartGroup, innerWidth, innerHeight, theme } = context;
    // A redraw removes the hovered dot, so its mouseleave would never fire; hide first.
    tooltip?.hide();
    chartGroup.selectAll('*').remove();
    if (innerWidth <= 0 || innerHeight <= 0) return;

    const points = parsedPoints;
    const maxCumulative = finalCumulative(data);
    // ChartCard owns the empty / not-enough-data state; bail before drawing axes on an empty curve.
    if (points.length === 0 || maxCumulative <= 0) return;

    const minDate = points[0].dateObj;
    const maxDate = points[points.length - 1].dateObj;
    const xScale = createTimeScale({ domain: [minDate, maxDate], range: [0, innerWidth] });

    // Count axis always starts at 0; add a little headroom so the asymptote line/label clears the top.
    const yTop = maxCumulative + Math.max(1, Math.ceil(maxCumulative * Y_HEADROOM_FRAC));
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

    // Y axis: integer species counts (round so a small domain never shows fractional ticks).
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
        text: t('analytics.advanced.charts.accumulation.axisDate'),
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
        text: t('analytics.advanced.charts.accumulation.axisSpecies'),
        orientation: 'left',
        offset: Y_AXIS_LABEL_OFFSET,
        width: innerWidth,
        height: innerHeight,
      },
      theme.axis
    );

    // Filled area under the curve (the "species accumulated so far" volume).
    const areaGen = area<PlottedPoint>()
      .x(d => xScale(d.dateObj))
      .y0(innerHeight)
      .y1(d => yScale(d.cumulativeSpecies))
      .curve(curveMonotoneX);
    chartGroup
      .append('path')
      .datum(points)
      .attr('class', 'accumulation-area')
      .attr('fill', theme.primary)
      .attr('fill-opacity', AREA_OPACITY)
      .attr('stroke', 'none')
      .attr('d', areaGen);

    // The cumulative line itself.
    const lineGen = line<PlottedPoint>()
      .x(d => xScale(d.dateObj))
      .y(d => yScale(d.cumulativeSpecies))
      .curve(curveMonotoneX);
    chartGroup
      .append('path')
      .datum(points)
      .attr('class', 'accumulation-line')
      .attr('fill', 'none')
      .attr('stroke', theme.primary)
      .attr('stroke-width', 2)
      .attr('d', lineGen);

    // Reference line at the total species count, with an inline label. The flat tail of the curve
    // approaches this; for a still-climbing curve it marks how many species have been recorded so far.
    const totalY = yScale(maxCumulative);
    chartGroup
      .append('line')
      .attr('class', 'accumulation-asymptote')
      .attr('x1', 0)
      .attr('x2', innerWidth)
      .attr('y1', totalY)
      .attr('y2', totalY)
      .style('stroke', theme.axis.color)
      .style('stroke-dasharray', '4 3')
      .style('stroke-width', 1)
      .style('opacity', 0.7);
    chartGroup
      .append('text')
      .attr('x', innerWidth)
      .attr('y', totalY - 4)
      .attr('text-anchor', 'end')
      .attr('aria-hidden', 'true')
      .style('fill', theme.axis.color)
      .style('font-size', theme.axis.fontSize)
      .text(t('analytics.advanced.charts.accumulation.totalSpecies', { species: maxCumulative }));

    // Per-point dots only on short ranges; on a wide range the dense dots clutter the steep line.
    if (points.length <= MAX_DOTS_DAYS) {
      chartGroup
        .append('g')
        .attr('class', 'accumulation-dots')
        .selectAll('circle')
        .data(points)
        .enter()
        .append('circle')
        .attr('cx', (d: PlottedPoint) => xScale(d.dateObj))
        .attr('cy', (d: PlottedPoint) => yScale(d.cumulativeSpecies))
        .attr('r', DOT_RADIUS)
        .style('fill', theme.primary)
        .style('opacity', DOT_IDLE_OPACITY)
        .on('mouseenter', function (event: MouseEvent, d: PlottedPoint) {
          select(this).style('opacity', 1);
          showTooltip(event, d);
        })
        .on('mouseleave', function () {
          select(this).style('opacity', DOT_IDLE_OPACITY);
          tooltip?.hide();
        });
    }
  }

  function showTooltip(event: MouseEvent, d: PlottedPoint): void {
    tooltip?.show({
      title: d.date,
      items: [
        {
          label: t('analytics.advanced.charts.accumulation.tooltipCumulative'),
          value: String(d.cumulativeSpecies),
        },
        {
          label: t('analytics.advanced.charts.accumulation.tooltipNew'),
          value: String(d.newSpecies),
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

<div class="accumulation-chart" bind:this={chartContainer}>
  <BaseChart
    {width}
    {height}
    margin={MARGIN}
    responsive={true}
    ariaLabel={ariaLabel ?? t('analytics.advanced.charts.accumulation.ariaLabel')}
  >
    {#snippet children(context)}
      {((chartContext = context), '')}
    {/snippet}
  </BaseChart>
  {#if summary}
    <p class="sr-only" data-testid="accumulation-summary">{summary}</p>
  {/if}
</div>

<style>
  .accumulation-chart {
    width: 100%;
    height: 100%;
    min-height: 320px;
  }

  :global(.accumulation-dots circle) {
    transition: opacity 0.12s ease;
  }
</style>
