<!--
  Seasonal density heatmap: detection count by (date, intra-day slot).

  Brightness (opacity over the theme's primary hue) encodes count, so migration waves and
  dawn-chorus drift read as bands across the season. The server sends a columnar sparse payload
  ({ dates, slotResolutionMinutes, cells }) and downsamples the slot resolution on wide ranges;
  this component renders it as scaleBand x scaleBand rects. On narrow viewports it folds the grid
  down to 24 hourly rows (toHourlyResolution) so rows stay readable.
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { select, type Selection } from 'd3-selection';
  import type { AxisDomain, AxisScale } from 'd3-axis';

  import BaseChart from './BaseChart.svelte';
  import { parseLocalDateString } from '$lib/utils/date';
  import { createBandScale } from './utils/scales';
  import { createAxis, styleAxis, addAxisLabel } from './utils/axes';
  import { ChartTooltip } from './utils/interactions';
  import { type ChartTheme } from './utils/theme';
  import {
    heatmapCells,
    toHourlyResolution,
    slotStartLabel,
    slotsPerDay,
    maxCellCount,
    type HeatmapData,
    type HeatmapCell,
  } from './utils/heatmap';
  import { t } from '$lib/i18n';

  interface Props {
    data: HeatmapData;
    width?: number;
    height?: number;
    ariaLabel?: string;
  }

  let { data, width = 800, height = 400, ariaLabel }: Props = $props();

  // Layout / style constants.
  const MARGIN = { top: 30, right: 16, bottom: 48, left: 56 };
  // Below this inner width the per-slot grid is unreadable, so fold to 24 hourly rows.
  const COMPACT_WIDTH_THRESHOLD = 480;
  const CELL_PADDING = 0.04;
  // Floor so the faintest non-zero cell stays visible against the card background.
  const MIN_CELL_OPACITY = 0.12;
  const MINUTES_PER_HOUR = 60;
  const Y_TICK_EVERY_HOURS = 3; // label the time axis every 3 hours
  const MAX_X_TICKS = 10;
  const X_AXIS_LABEL_OFFSET = 38;
  const Y_AXIS_LABEL_OFFSET = 44;
  const LEGEND_STEPS = 5;
  const LEGEND_SWATCH = 14;

  let tooltip: ChartTooltip | null = null;
  let chartContainer: HTMLDivElement | null = null;

  // Screen-reader summary so the heatmap is not opaque to assistive tech (spec section 7).
  const summary = $derived.by(() => {
    const cells = heatmapCells(data);
    if (cells.length === 0) return '';
    const total = cells.reduce((sum, c) => sum + c.count, 0);
    const peak = cells.reduce((best, c) => (c.count > best.count ? c : best), cells[0]);
    const peakDate = data.dates[peak.dateIndex] ?? '';
    return t('analytics.advanced.charts.heatmap.summary', {
      total,
      days: data.dates.length,
      time: slotStartLabel(peak.slot, data.slotResolutionMinutes),
      date: peakDate,
    });
  });

  let chartContext = $state<{
    svg: Selection<SVGSVGElement, unknown, null, undefined>;
    chartGroup: Selection<globalThis.SVGGElement, unknown, null, undefined>;
    innerWidth: number;
    innerHeight: number;
    theme: ChartTheme;
  } | null>(null);

  // Cached once: constructing an Intl.DateTimeFormat per tick is costly on frequent redraws.
  const dateTickFormatter = new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric' });

  function formatDateTick(dateStr: string): string {
    const d = parseLocalDateString(dateStr);
    if (!d || isNaN(d.getTime())) return dateStr;
    return dateTickFormatter.format(d);
  }

  // Evenly spaced subset of the domain so axis labels never crowd.
  function sampleTicks(domain: string[], maxTicks: number): string[] {
    if (domain.length <= maxTicks) return domain;
    const step = Math.ceil(domain.length / maxTicks);
    return domain.filter((_, i) => i % step === 0);
  }

  function drawChart(context: NonNullable<typeof chartContext>): void {
    const { chartGroup, innerWidth, innerHeight, theme } = context;
    // Hide any active tooltip first: a redraw removes the hovered rect, so its mouseleave
    // would never fire and the tooltip could otherwise get stuck on screen.
    tooltip?.hide();
    chartGroup.selectAll('*').remove();
    if (innerWidth <= 0 || innerHeight <= 0) return;

    // Narrow viewports collapse to a 24-row hourly grid for legibility.
    const compact = innerWidth < COMPACT_WIDTH_THRESHOLD;
    const view: HeatmapData = compact ? toHourlyResolution(data) : data;
    const resolution = view.slotResolutionMinutes;
    const nSlots = slotsPerDay(resolution);
    const cells = heatmapCells(view);
    if (cells.length === 0 || view.dates.length === 0) return;

    const colorMax = Math.max(1, maxCellCount(cells));

    const xScale = createBandScale({
      domain: view.dates,
      range: [0, innerWidth],
      padding: CELL_PADDING,
    });

    const slotDomain = Array.from({ length: nSlots }, (_, i) => String(i));
    const yScale = createBandScale({
      domain: slotDomain,
      range: [0, innerHeight],
      padding: CELL_PADDING,
    });

    // X axis: a sampled subset of dates, formatted "Mon D".
    // Band scales are valid AxisScales at runtime; widen the type for createAxis.
    const xAxis = createAxis({
      scale: xScale as unknown as AxisScale<AxisDomain>,
      orientation: 'bottom',
      tickFormat: (d: AxisDomain) => formatDateTick(String(d)),
    });
    xAxis.tickValues(sampleTicks(view.dates, MAX_X_TICKS));
    const xAxisGroup = chartGroup
      .append('g')
      .attr('class', 'x-axis')
      .attr('transform', `translate(0,${innerHeight})`)
      .call(xAxis);
    styleAxis(xAxisGroup, theme.axis);

    // Y axis: every few hours, labelled HH:MM.
    const slotsPerTick = Math.max(1, (Y_TICK_EVERY_HOURS * MINUTES_PER_HOUR) / resolution);
    const yTickValues = slotDomain.filter((_, i) => i % slotsPerTick === 0);
    const yAxis = createAxis({
      scale: yScale as unknown as AxisScale<AxisDomain>,
      orientation: 'left',
      tickFormat: (d: AxisDomain) => slotStartLabel(Number(d), resolution),
    });
    yAxis.tickValues(yTickValues);
    const yAxisGroup = chartGroup.append('g').attr('class', 'y-axis').call(yAxis);
    styleAxis(yAxisGroup, theme.axis);

    addAxisLabel(
      chartGroup,
      {
        text: t('analytics.advanced.charts.heatmap.axisDate'),
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
        text: t('analytics.advanced.charts.heatmap.axisTime'),
        orientation: 'left',
        offset: Y_AXIS_LABEL_OFFSET,
        width: innerWidth,
        height: innerHeight,
      },
      theme.axis
    );

    // Cells: density encoded as opacity over the primary hue (keeps it theme-driven and avoids
    // parsing CSS color tokens, which can be oklch() under Tailwind v4 and break d3 interpolation).
    const bandW = xScale.bandwidth();
    const bandH = yScale.bandwidth();
    chartGroup
      .append('g')
      .attr('class', 'heatmap-cells')
      .selectAll('rect.heatmap-cell')
      .data(cells)
      .enter()
      .append('rect')
      .attr('class', 'heatmap-cell')
      .attr('x', (d: HeatmapCell) => xScale(view.dates[d.dateIndex]) ?? 0)
      .attr('y', (d: HeatmapCell) => yScale(String(d.slot)) ?? 0)
      .attr('width', bandW)
      .attr('height', bandH)
      .attr('rx', 1)
      .style('fill', theme.primary)
      .style(
        'opacity',
        (d: HeatmapCell) => MIN_CELL_OPACITY + (1 - MIN_CELL_OPACITY) * (d.count / colorMax)
      )
      .on('mouseenter', function (event: MouseEvent, d: HeatmapCell) {
        select(this).style('stroke', theme.text).style('stroke-width', 1);
        tooltip?.show({
          title: data.dates[d.dateIndex] ?? '',
          items: [
            {
              label: t('analytics.advanced.charts.heatmap.tooltipTime'),
              value: slotStartLabel(d.slot, resolution),
            },
            {
              label: t('analytics.advanced.charts.heatmap.tooltipCount'),
              value: String(d.count),
            },
          ],
          x: event.clientX,
          y: event.clientY,
        });
      })
      .on('mouseleave', function () {
        select(this).style('stroke', 'none');
        tooltip?.hide();
      });

    drawLegend(context, colorMax);
  }

  // Compact gradient legend in the top margin: faint -> solid swatches with min/max labels.
  //
  // The group is provisionally placed at `innerWidth - legendWidth` (flush with the inner-right
  // edge), then shifted further left once the "More {max}" label's real width is known -- that
  // label sits past the swatches at `legendWidth + 6`, so left-aligning the group alone left its
  // right edge running past `innerWidth` and into the 16px right margin, clipping. See PR brief.
  function drawLegend(context: NonNullable<typeof chartContext>, colorMax: number): void {
    const { chartGroup, innerWidth, theme } = context;
    const legendWidth = LEGEND_STEPS * LEGEND_SWATCH;
    const legend = chartGroup
      .append('g')
      .attr('class', 'heatmap-legend')
      .attr(
        'transform',
        `translate(${Math.max(0, innerWidth - legendWidth)},${-LEGEND_SWATCH - 6})`
      )
      .style('pointer-events', 'none');

    for (let i = 0; i < LEGEND_STEPS; i++) {
      const fraction = (i + 1) / LEGEND_STEPS;
      legend
        .append('rect')
        .attr('x', i * LEGEND_SWATCH)
        .attr('y', 0)
        .attr('width', LEGEND_SWATCH)
        .attr('height', LEGEND_SWATCH)
        .attr('rx', 1)
        .style('fill', theme.primary)
        .style('opacity', MIN_CELL_OPACITY + (1 - MIN_CELL_OPACITY) * fraction);
    }

    legend
      .append('text')
      .attr('x', -6)
      .attr('y', LEGEND_SWATCH - 3)
      .attr('text-anchor', 'end')
      .style('fill', theme.axis.color)
      .style('font-size', theme.axis.fontSize)
      .text(t('analytics.advanced.charts.heatmap.legendLess'));
    const moreLabel = legend
      .append('text')
      .attr('x', legendWidth + 6)
      .attr('y', LEGEND_SWATCH - 3)
      .attr('text-anchor', 'start')
      .style('fill', theme.axis.color)
      .style('font-size', theme.axis.fontSize)
      .text(t('analytics.advanced.charts.heatmap.legendMore', { max: colorMax }));

    // Re-anchor the whole group so the "More" label's measured right edge lands within
    // innerWidth instead of the hard-coded `innerWidth - legendWidth`. getComputedTextLength()
    // returns a real width in browsers; jsdom (unit tests) does not implement the method, so the
    // optional call short-circuits and we fall back to 0, reproducing the original flush-right
    // placement (tests unaffected). The "Less" label (end-anchored at x = -6) stays inside because
    // the group only ever moves further left, never right.
    const moreNode = moreLabel.node();
    const moreWidth = moreNode?.getComputedTextLength?.() ?? 0;
    const groupX = Math.max(0, innerWidth - legendWidth - 6 - moreWidth);
    legend.attr('transform', `translate(${groupX},${-LEGEND_SWATCH - 6})`);
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

<div class="seasonal-heatmap-chart" bind:this={chartContainer}>
  <BaseChart
    {width}
    {height}
    margin={MARGIN}
    responsive={true}
    ariaLabel={ariaLabel ?? t('analytics.advanced.charts.heatmap.ariaLabel')}
  >
    {#snippet children(context)}
      {((chartContext = context), '')}
    {/snippet}
  </BaseChart>
  {#if summary}
    <p class="sr-only" data-testid="heatmap-summary">{summary}</p>
  {/if}
</div>

<style>
  .seasonal-heatmap-chart {
    width: 100%;
    height: 100%;
    min-height: 400px;

    /* Narrow viewports fold to 24 hourly rows; allow vertical scroll if they still overflow. */
    overflow-y: auto;
  }

  :global(.heatmap-cell) {
    transition: opacity 0.12s ease;
  }
</style>
