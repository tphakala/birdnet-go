<!--
  Dawn-chorus onset tracker (design spec section 6.3).

  Scatter of each day's chorus onset relative to civil dawn (minutes; negative = before civil dawn)
  with a smoothed trend line and a y=0 reference line marking civil dawn. The backend emits one
  point per calendar day, so gap days (too few detections / no civil dawn) leave the scatter empty
  there. The smoothed trend spans short gaps (the moving average still has enough nearby days) but
  breaks over sustained ones, where the window has too few non-null days to average.
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { select } from 'd3-selection';
  import { line, curveMonotoneX } from 'd3-shape';
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
    movingAverageTrend,
    onsetCount,
    type DawnOnsetData,
    type DawnOnsetPoint,
  } from './utils/dawnOnset';
  import { t } from '$lib/i18n';

  interface Props {
    data: DawnOnsetData;
    width?: number;
    height?: number;
    ariaLabel?: string;
  }

  let { data, width = 800, height = 400, ariaLabel }: Props = $props();

  // Layout / behavior constants.
  const MARGIN = { top: 24, right: 18, bottom: 48, left: 60 };
  const TREND_WINDOW_DAYS = 7; // weekly centered moving average
  const TREND_MIN_SAMPLES = 3; // window needs this many non-null days, else the trend breaks here
  const DOT_RADIUS = 3;
  const DOT_IDLE_OPACITY = 0.55;
  const MAX_X_TICKS = 8;
  const X_AXIS_LABEL_OFFSET = 38;
  const Y_AXIS_LABEL_OFFSET = 46;
  // Pad a collapsed y-domain (all onsets identical) so the chart still renders a band.
  const FLAT_DOMAIN_PAD_MINUTES = 10;

  interface PlottedPoint extends DawnOnsetPoint {
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
    return t('analytics.advanced.charts.dawnOnset.summary', {
      days: parsedPoints.length,
      plotted: onsetCount(data),
    });
  });

  let chartContext = $state<{
    svg: import('d3-selection').Selection<SVGSVGElement, unknown, null, undefined>;
    chartGroup: import('d3-selection').Selection<globalThis.SVGGElement, unknown, null, undefined>;
    innerWidth: number;
    innerHeight: number;
    theme: ChartTheme;
  } | null>(null);

  // Signed minute label for the y axis (e.g. "+20", "0", "-15").
  function formatMinutes(value: number): string {
    const rounded = Math.round(value);
    return rounded > 0 ? `+${rounded}` : String(rounded);
  }

  // Human-readable onset for the tooltip, relative to civil dawn.
  function onsetText(rel: number): string {
    if (rel > 0) {
      return t('analytics.advanced.charts.dawnOnset.tooltipOnsetAfter', { minutes: rel });
    }
    if (rel < 0) {
      return t('analytics.advanced.charts.dawnOnset.tooltipOnsetBefore', { minutes: -rel });
    }
    return t('analytics.advanced.charts.dawnOnset.tooltipOnsetAt');
  }

  function drawChart(context: NonNullable<typeof chartContext>): void {
    const { chartGroup, innerWidth, innerHeight, theme } = context;
    // A redraw removes the hovered dot, so its mouseleave would never fire; hide first.
    tooltip?.hide();
    chartGroup.selectAll('*').remove();
    if (innerWidth <= 0 || innerHeight <= 0) return;

    const points = parsedPoints;
    const plotted = points.filter(p => p.onsetRelMinutes !== null);
    // ChartCard owns the empty / not-enough-data state; bail before drawing axes on an empty series.
    if (plotted.length === 0) return;

    const minDate = points[0].dateObj;
    const maxDate = points[points.length - 1].dateObj;
    const xScale = createTimeScale({ domain: [minDate, maxDate], range: [0, innerWidth] });

    const onsetValues = plotted.map(p => p.onsetRelMinutes as number);
    // Always include 0 (civil dawn) so the reference line is on-screen.
    let yLow = Math.min(0, ...onsetValues);
    let yHigh = Math.max(0, ...onsetValues);
    if (yLow === yHigh) {
      yLow -= FLAT_DOMAIN_PAD_MINUTES;
      yHigh += FLAT_DOMAIN_PAD_MINUTES;
    }
    const yScale = createLinearScale({ domain: [yLow, yHigh], range: [innerHeight, 0] });

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

    // Y axis: signed minutes from civil dawn.
    const yAxis = createAxis({
      scale: yScale as unknown as AxisScale<AxisDomain>,
      orientation: 'left',
      tickFormat: (d: AxisDomain) => formatMinutes(Number(d)),
    });
    const yAxisGroup = chartGroup.append('g').attr('class', 'y-axis').call(yAxis);
    styleAxis(yAxisGroup, theme.axis);

    addAxisLabel(
      chartGroup,
      {
        text: t('analytics.advanced.charts.dawnOnset.axisDate'),
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
        text: t('analytics.advanced.charts.dawnOnset.axisOnset'),
        orientation: 'left',
        offset: Y_AXIS_LABEL_OFFSET,
        width: innerWidth,
        height: innerHeight,
      },
      theme.axis
    );

    // Civil dawn reference line at y=0, with an inline label.
    const zeroY = yScale(0);
    chartGroup
      .append('line')
      .attr('class', 'civil-dawn-line')
      .attr('x1', 0)
      .attr('x2', innerWidth)
      .attr('y1', zeroY)
      .attr('y2', zeroY)
      .style('stroke', theme.axis.color)
      .style('stroke-dasharray', '4 3')
      .style('stroke-width', 1)
      .style('opacity', 0.7);
    chartGroup
      .append('text')
      .attr('x', innerWidth)
      .attr('y', zeroY - 4)
      .attr('text-anchor', 'end')
      .attr('aria-hidden', 'true')
      .style('fill', theme.axis.color)
      .style('font-size', theme.axis.fontSize)
      .text(t('analytics.advanced.charts.dawnOnset.civilDawn'));

    // Smoothed trend line; .defined() breaks it over gaps rather than interpolating to 0.
    const trend = movingAverageTrend(points, TREND_WINDOW_DAYS, TREND_MIN_SAMPLES);
    // trend is aligned 1:1 with points; .at() (not bracket indexing) keeps the lint clean.
    const trendData = points.map((p, i) => ({ dateObj: p.dateObj, value: trend.at(i) ?? null }));
    const trendLine = line<{ dateObj: Date; value: number | null }>()
      .defined(d => d.value !== null)
      .x(d => xScale(d.dateObj))
      .y(d => yScale(d.value as number))
      .curve(curveMonotoneX);
    chartGroup
      .append('path')
      .datum(trendData)
      .attr('class', 'onset-trend')
      .attr('fill', 'none')
      .attr('stroke', theme.primary)
      .attr('stroke-width', 2)
      .attr('d', trendLine);

    // Scatter dots for the days with a measurable onset.
    chartGroup
      .append('g')
      .attr('class', 'onset-dots')
      .selectAll('circle')
      .data(plotted)
      .enter()
      .append('circle')
      .attr('cx', (d: PlottedPoint) => xScale(d.dateObj))
      .attr('cy', (d: PlottedPoint) => yScale(d.onsetRelMinutes as number))
      .attr('r', DOT_RADIUS)
      .style('fill', theme.primary)
      .style('opacity', DOT_IDLE_OPACITY)
      .on('mouseenter', function (event: MouseEvent, d: PlottedPoint) {
        select(this).style('opacity', 1);
        tooltip?.show({
          title: d.date,
          items: [
            {
              label: t('analytics.advanced.charts.dawnOnset.tooltipOnset'),
              value: onsetText(d.onsetRelMinutes as number),
            },
            {
              label: t('analytics.advanced.charts.dawnOnset.tooltipCount'),
              value: String(d.detectionCount),
            },
          ],
          x: event.clientX,
          y: event.clientY,
        });
      })
      .on('mouseleave', function () {
        select(this).style('opacity', DOT_IDLE_OPACITY);
        tooltip?.hide();
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

<div class="dawn-onset-chart" bind:this={chartContainer}>
  <BaseChart
    {width}
    {height}
    margin={MARGIN}
    responsive={true}
    ariaLabel={ariaLabel ?? t('analytics.advanced.charts.dawnOnset.ariaLabel')}
  >
    {#snippet children(context)}
      {((chartContext = context), '')}
    {/snippet}
  </BaseChart>
  {#if summary}
    <p class="sr-only" data-testid="dawn-onset-summary">{summary}</p>
  {/if}
</div>

<style>
  .dawn-onset-chart {
    width: 100%;
    height: 100%;
    min-height: 320px;
  }

  :global(.onset-dots circle) {
    transition: opacity 0.12s ease;
  }
</style>
