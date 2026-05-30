<!-- Time-series line chart (single or multi series) -->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { extent, max } from 'd3-array';
  import { select } from 'd3-selection';
  import { line, curveMonotoneX } from 'd3-shape';
  import type { AxisDomain } from 'd3-axis';

  import BaseChart from './BaseChart.svelte';
  import { getLocalDateString } from '$lib/utils/date';
  import { createTimeScale, createLinearScale } from './utils/scales';
  import {
    createAxis,
    styleAxis,
    addAxisLabel,
    createDateAxisFormatter,
    pickDateRangeBucket,
  } from './utils/axes';
  import { ChartTooltip, addCrosshair, createLegend } from './utils/interactions';
  import { generateSpeciesColors, type ChartRenderContext } from './utils/theme';

  export interface LineSeriesPoint {
    date: Date;
    value: number;
  }

  export interface LineSeries {
    id: string;
    label: string;
    color?: string;
    data: LineSeriesPoint[];
  }

  interface Props {
    series: LineSeries[];
    width?: number;
    height?: number;
    valueAxisLabel?: string;
    dateAxisLabel?: string;
    dateRange?: [Date, Date];
    showLegend?: boolean;
    formatValue?: (_n: number) => string;
    valueTooltipLabel?: string;
    ariaLabel?: string;
  }

  let {
    series = [],
    width = 800,
    height = 400,
    valueAxisLabel,
    dateAxisLabel,
    dateRange,
    showLegend,
    formatValue = (n: number) => String(n),
    valueTooltipLabel,
    ariaLabel,
  }: Props = $props();

  // Styling constants
  const VALUE_HEADROOM = 1.1;
  const MAX_X_TICKS = 8;
  const TICK_SPACING_PX = 80;
  const VALUE_TICK_COUNT = 6;
  const X_AXIS_LABEL_OFFSET = 35;
  const Y_AXIS_LABEL_OFFSET = 45;
  const LINE_STROKE_WIDTH = 2.5;
  const POINT_RADIUS = 3;
  const POINT_HOVER_RADIUS = 6;
  const POINT_OPACITY = 0.8;
  const TRANSITION_MS = 150;
  const LEGEND_WIDTH = 150;
  const MS_PER_DAY = 86_400_000;

  let tooltip: ChartTooltip | null = null;
  let chartContainer: HTMLDivElement | null = null;

  const legendVisible = $derived(showLegend ?? series.length > 1);

  let chartContext = $state<ChartRenderContext | null>(null);

  function drawChart(context: ChartRenderContext) {
    const { chartGroup, innerWidth, innerHeight, theme } = context;

    chartGroup.selectAll('*').remove();

    const seriesWithData = series.filter(s => s.data.length > 0);
    if (!seriesWithData.length || innerWidth <= 0 || innerHeight <= 0) {
      return;
    }

    // Assign colors (use provided color, else theme palette by index).
    const palette = generateSpeciesColors(seriesWithData.length, theme);
    const resolved = seriesWithData.map((s, i) => ({
      ...s,
      // eslint-disable-next-line security/detect-object-injection -- i iterates the series array
      color: s.color ?? (seriesWithData.length === 1 ? theme.primary : palette[i]),
    }));

    const allDates = resolved.flatMap(s => s.data.map(p => p.date));
    const allValues = resolved.flatMap(s => s.data.map(p => p.value));

    const dateExtent = extent<Date>(allDates);
    // Pad a missing or collapsed (single-day) domain so the time scale never
    // gets a zero-width domain, which would otherwise stack every point at one x.
    const safeDateExtent: [Date, Date] = (() => {
      const start = dateExtent[0];
      const end = dateExtent[1];
      if (!start || !end) {
        const now = Date.now();
        return [new Date(now - MS_PER_DAY), new Date(now + MS_PER_DAY)];
      }
      if (start.getTime() === end.getTime()) {
        return [new Date(start.getTime() - MS_PER_DAY), new Date(end.getTime() + MS_PER_DAY)];
      }
      return [start, end];
    })();
    const maxValue = max(allValues) ?? 0;

    const xScale = createTimeScale({
      domain: dateRange ?? safeDateExtent,
      range: [0, innerWidth],
    });
    const yScale = createLinearScale({
      domain: [0, maxValue * VALUE_HEADROOM || 1],
      range: [innerHeight, 0],
    });

    // Date format based on span. These are daily-granularity points, so the
    // bucket is never 'day' (which would render clock times).
    const span = xScale.domain();
    const dateTick = createDateAxisFormatter(pickDateRangeBucket([span[0], span[1]]));

    const xAxis = createAxis({
      scale: xScale,
      orientation: 'bottom',
      tickFormat: (d: AxisDomain) => dateTick(d as Date),
      tickCount: Math.min(MAX_X_TICKS, Math.max(2, Math.floor(innerWidth / TICK_SPACING_PX))),
    });
    const yAxis = createAxis({
      scale: yScale,
      orientation: 'left',
      tickCount: VALUE_TICK_COUNT,
      tickFormat: (d: AxisDomain) => formatValue(Number(d)),
    });

    const xAxisGroup = chartGroup
      .append('g')
      .attr('class', 'x-axis')
      .attr('transform', `translate(0,${innerHeight})`)
      .call(xAxis);
    const yAxisGroup = chartGroup.append('g').attr('class', 'y-axis').call(yAxis);

    styleAxis(xAxisGroup, theme.axis);
    styleAxis(yAxisGroup, theme.axis);

    if (dateAxisLabel) {
      addAxisLabel(
        chartGroup,
        {
          text: dateAxisLabel,
          orientation: 'bottom',
          offset: X_AXIS_LABEL_OFFSET,
          width: innerWidth,
          height: innerHeight,
        },
        theme.axis
      );
    }
    if (valueAxisLabel) {
      addAxisLabel(
        chartGroup,
        {
          text: valueAxisLabel,
          orientation: 'left',
          offset: Y_AXIS_LABEL_OFFSET,
          width: innerWidth,
          height: innerHeight,
        },
        theme.axis
      );
    }

    // Crosshair for value tracking (polish).
    addCrosshair(chartGroup, {
      width: innerWidth,
      height: innerHeight,
      onMove: (_x, _y, event) => {
        tooltip?.move(event.clientX, event.clientY);
      },
      onLeave: () => tooltip?.hide(),
    });

    const lineGenerator = line<LineSeriesPoint>()
      .x(p => xScale(p.date))
      .y(p => yScale(p.value))
      .curve(curveMonotoneX);

    const seriesGroup = chartGroup.append('g').attr('class', 'series');

    resolved.forEach(s => {
      const group = seriesGroup.append('g').attr('class', 'series-group').attr('data-series', s.id);

      group
        .append('path')
        .datum(s.data)
        .attr('class', 'line-series')
        .attr('data-series', s.id)
        .attr('d', lineGenerator)
        .style('fill', 'none')
        .style('stroke', s.color)
        .style('stroke-width', LINE_STROKE_WIDTH)
        .style('opacity', 0.9);

      const points = group
        .selectAll('circle.line-point')
        .data(s.data)
        .enter()
        .append('circle')
        .attr('class', 'line-point')
        .attr('data-series', s.id)
        .attr('cx', p => xScale(p.date))
        .attr('cy', p => yScale(p.value))
        .attr('r', POINT_RADIUS)
        .style('fill', s.color)
        .style('opacity', POINT_OPACITY);

      const tooltipLabel = valueTooltipLabel ?? s.label;
      points
        .on('mouseenter', function (event: MouseEvent, p: LineSeriesPoint) {
          select(this).transition().duration(TRANSITION_MS).attr('r', POINT_HOVER_RADIUS);
          tooltip?.show({
            title: s.label,
            items: [
              { label: tooltipLabel, value: formatValue(p.value), color: s.color },
              { label: '', value: getLocalDateString(p.date) },
            ],
            x: event.clientX,
            y: event.clientY,
          });
        })
        .on('mousemove', function (event: MouseEvent) {
          tooltip?.move(event.clientX, event.clientY);
        })
        .on('mouseleave', function () {
          select(this).transition().duration(TRANSITION_MS).attr('r', POINT_RADIUS);
          tooltip?.hide();
        });
    });

    if (legendVisible) {
      createLegend(chartGroup, {
        items: resolved.map(s => ({ id: s.id, label: s.label, color: s.color, visible: true })),
        position: { x: Math.max(0, innerWidth - LEGEND_WIDTH), y: 0 },
        itemHeight: 20,
      });
    }
  }

  // Repaint when any drawChart input changes (data or label/format/locale).
  $effect(() => {
    void series;
    void dateRange;
    void valueAxisLabel;
    void dateAxisLabel;
    void legendVisible;
    void formatValue;
    void valueTooltipLabel;
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
    tooltip = null;
  });
</script>

<div class="line-chart" bind:this={chartContainer}>
  <BaseChart {width} {height} {ariaLabel} responsive={true}>
    {#snippet children(context)}
      {((chartContext = context), '')}
    {/snippet}
  </BaseChart>
</div>

<style>
  .line-chart {
    width: 100%;
    height: 100%;
    min-height: 300px;
  }

  :global(.line-chart circle.line-point) {
    transition:
      r 0.15s ease,
      opacity 0.15s ease;
  }
</style>
