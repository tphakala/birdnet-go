<!-- Species Diversity Over Time Chart -->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { extent } from 'd3-array';
  import { select, type Selection } from 'd3-selection';
  import { line, area, curveMonotoneX } from 'd3-shape';
  import type { AxisDomain } from 'd3-axis';

  import BaseChart from './BaseChart.svelte';
  import { getLocalDateString } from '$lib/utils/date';
  import { createTimeScale, createLinearScale } from './utils/scales';
  import { createAxis, styleAxis, addAxisLabel, createDateAxisFormatter } from './utils/axes';
  import { ChartTooltip } from './utils/interactions';
  import { type ChartTheme } from './utils/theme';
  import { t } from '$lib/i18n';

  interface DiversityDatum {
    date: Date;
    uniqueSpecies: number;
  }

  interface Props {
    data: DiversityDatum[];
    width?: number;
    height?: number;
    dateRange?: [Date, Date];
  }

  let { data = [], width = 800, height = 400, dateRange }: Props = $props();

  // Chart styling constants
  const Y_SCALE_HEADROOM = 1.1;
  const MAX_X_TICKS = 8;
  const TICK_SPACING_PX = 80;
  const Y_TICK_COUNT = 6;
  const X_AXIS_LABEL_OFFSET = 35;
  const Y_AXIS_LABEL_OFFSET = 45;
  const LINE_STROKE_WIDTH = 2.5;
  const AREA_OPACITY = 0.15;
  const LINE_OPACITY = 0.9;
  const POINT_RADIUS = 3;
  const POINT_HOVER_RADIUS = 6;
  const POINT_OPACITY = 0.7;
  const TRANSITION_DURATION_MS = 150;

  // Date range thresholds (days)
  const WEEK_THRESHOLD = 7;
  const MONTH_THRESHOLD = 30;
  const YEAR_THRESHOLD = 365;
  const MS_PER_DAY = 1000 * 60 * 60 * 24;

  // Component state
  let tooltip: ChartTooltip | null = null;
  let chartContainer: HTMLDivElement | null = null;

  // Calculate scales
  const scales = $derived.by(() => {
    if (!data.length) return null;

    const allDates = data.map(d => d.date);
    const allCounts = data.map(d => d.uniqueSpecies);

    const dateExtent = extent<Date>(allDates);
    const countExtent = extent<number>(allCounts);

    const safeDateExtent: [Date, Date] =
      dateExtent[0] && dateExtent[1] ? [dateExtent[0], dateExtent[1]] : [new Date(), new Date()];
    const safeMaxCount = countExtent[1] !== undefined ? countExtent[1] : 10;

    return {
      x: safeDateExtent,
      yMax: safeMaxCount,
    };
  });

  // Store chart context
  let chartContext = $state<{
    svg: Selection<SVGSVGElement, unknown, null, undefined>;
    chartGroup: Selection<globalThis.SVGGElement, unknown, null, undefined>;
    innerWidth: number;
    innerHeight: number;
    theme: ChartTheme;
  } | null>(null);

  function drawChart(context: {
    svg: Selection<SVGSVGElement, unknown, null, undefined>;
    chartGroup: Selection<globalThis.SVGGElement, unknown, null, undefined>;
    innerWidth: number;
    innerHeight: number;
    theme: ChartTheme;
  }) {
    if (!scales || !data.length) {
      if (context.chartGroup) {
        context.chartGroup.selectAll('*').remove();
      }
      return '';
    }

    const { chartGroup, innerWidth, innerHeight, theme } = context;

    // Update scale ranges for current dimensions
    const xScale = createTimeScale({
      domain: dateRange || scales.x,
      range: [0, innerWidth],
    });

    const yScale = createLinearScale({
      domain: [0, scales.yMax * Y_SCALE_HEADROOM],
      range: [innerHeight, 0],
    });

    // Clear existing content
    if (chartGroup) {
      chartGroup.selectAll('*').remove();
    }

    // Determine date format based on range
    const timeRange = xScale.domain();
    const daysDiff = (timeRange[1].getTime() - timeRange[0].getTime()) / MS_PER_DAY;
    const dateFormat =
      daysDiff <= WEEK_THRESHOLD
        ? 'day'
        : daysDiff <= MONTH_THRESHOLD
          ? 'week'
          : daysDiff <= YEAR_THRESHOLD
            ? 'month'
            : 'year';

    // Create axes
    const dateTick = createDateAxisFormatter(dateFormat);
    const xAxis = createAxis({
      scale: xScale,
      orientation: 'bottom',
      tickFormat: (d: AxisDomain) => dateTick(d as Date),
      tickCount: Math.min(MAX_X_TICKS, Math.floor(innerWidth / TICK_SPACING_PX)),
    });

    const yAxis = createAxis({
      scale: yScale,
      orientation: 'left',
      tickCount: Y_TICK_COUNT,
    });

    // Draw axes
    const xAxisGroup = chartGroup
      .append('g')
      .attr('class', 'x-axis')
      .attr('transform', `translate(0,${innerHeight})`)
      .call(xAxis);

    const yAxisGroup = chartGroup.append('g').attr('class', 'y-axis').call(yAxis);

    // Style axes
    styleAxis(xAxisGroup, theme.axis);
    styleAxis(yAxisGroup, theme.axis);

    // Add axis labels
    addAxisLabel(
      chartGroup,
      {
        text: t('analytics.advanced.charts.diversity.axisDate'),
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
        text: t('analytics.advanced.charts.diversity.axisUniqueSpecies'),
        orientation: 'left',
        offset: Y_AXIS_LABEL_OFFSET,
        width: innerWidth,
        height: innerHeight,
      },
      theme.axis
    );

    // Create clip path to constrain content within chart area
    const clipId = `diversity-clip-${Math.random().toString(36).slice(2, 8)}`;
    chartGroup
      .append('defs')
      .append('clipPath')
      .attr('id', clipId)
      .append('rect')
      .attr('width', innerWidth)
      .attr('height', innerHeight);

    const chartArea = chartGroup.append('g').attr('clip-path', `url(#${clipId})`);

    // Area generator
    const areaGenerator = area<DiversityDatum>()
      .x(d => xScale(d.date))
      .y0(innerHeight)
      .y1(d => yScale(d.uniqueSpecies))
      .curve(curveMonotoneX);

    // Line generator
    const lineGenerator = line<DiversityDatum>()
      .x(d => xScale(d.date))
      .y(d => yScale(d.uniqueSpecies))
      .curve(curveMonotoneX);

    // Draw filled area
    chartArea
      .append('path')
      .datum(data)
      .attr('class', 'diversity-area')
      .attr('d', areaGenerator)
      .style('fill', theme.primary)
      .style('opacity', AREA_OPACITY)
      .style('pointer-events', 'none');

    // Draw line on top
    chartArea
      .append('path')
      .datum(data)
      .attr('class', 'diversity-line')
      .attr('d', lineGenerator)
      .style('fill', 'none')
      .style('stroke', theme.primary)
      .style('stroke-width', LINE_STROKE_WIDTH)
      .style('opacity', LINE_OPACITY);

    // Add data points with tooltips
    chartArea
      .selectAll('.diversity-point')
      .data(data)
      .enter()
      .append('circle')
      .attr('class', 'diversity-point')
      .attr('cx', d => xScale(d.date))
      .attr('cy', d => yScale(d.uniqueSpecies))
      .attr('r', POINT_RADIUS)
      .style('fill', theme.primary)
      .style('opacity', POINT_OPACITY)
      .on('mouseenter', function (event: MouseEvent, d: DiversityDatum) {
        select(this)
          .transition()
          .duration(TRANSITION_DURATION_MS)
          .attr('r', POINT_HOVER_RADIUS)
          .style('opacity', 1);

        tooltip?.show({
          title: t('analytics.advanced.charts.diversity.title'),
          items: [
            {
              label: t('analytics.advanced.charts.diversity.axisDate'),
              value: getLocalDateString(d.date),
            },
            {
              label: t('analytics.advanced.charts.diversity.axisUniqueSpecies'),
              value: d.uniqueSpecies.toString(),
            },
          ],
          x: event.clientX,
          y: event.clientY,
        });
      })
      .on('mouseleave', function () {
        select(this)
          .transition()
          .duration(TRANSITION_DURATION_MS)
          .attr('r', POINT_RADIUS)
          .style('opacity', POINT_OPACITY);
        tooltip?.hide();
      });

    return '';
  }

  // Re-render chart when data, scales, or chart context change
  $effect(() => {
    if (chartContext && data.length > 0 && scales) {
      drawChart(chartContext);
    } else if (chartContext?.chartGroup && (!data.length || !scales)) {
      chartContext.chartGroup.selectAll('*').remove();
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

<div class="species-diversity-chart" bind:this={chartContainer}>
  <BaseChart {width} {height} responsive={true}>
    {#snippet children(context)}
      {((chartContext = context), '')}
    {/snippet}
  </BaseChart>
</div>

<style>
  .species-diversity-chart {
    width: 100%;
    height: 100%;
    min-height: 400px;
  }

  :global(.diversity-line) {
    transition:
      stroke-width 0.2s ease,
      opacity 0.2s ease;
  }

  :global(.diversity-point) {
    transition:
      r 0.15s ease,
      opacity 0.15s ease;
  }
</style>
