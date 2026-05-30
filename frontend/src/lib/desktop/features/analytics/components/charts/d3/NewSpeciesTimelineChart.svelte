<!-- New species timeline: horizontal markers on a time x-axis -->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { extent } from 'd3-array';
  import { select } from 'd3-selection';
  import type { AxisDomain, AxisScale } from 'd3-axis';

  import BaseChart from './BaseChart.svelte';
  import { getLocalDateString } from '$lib/utils/date';
  import { createTimeScale, createBandScale } from './utils/scales';
  import {
    createAxis,
    styleAxis,
    addAxisLabel,
    createDateAxisFormatter,
    pickDateRangeBucket,
  } from './utils/axes';
  import { ChartTooltip } from './utils/interactions';
  import { generateSpeciesColors, type ChartRenderContext } from './utils/theme';

  export interface NewSpeciesDatum {
    commonName: string;
    scientificName?: string;
    firstHeard: Date;
  }

  interface Props {
    data: NewSpeciesDatum[];
    dateRange?: [Date, Date];
    width?: number;
    height?: number;
    firstHeardLabel?: string;
    dateAxisLabel?: string;
    ariaLabel?: string;
  }

  let {
    data = [],
    dateRange,
    width = 800,
    height = 400,
    firstHeardLabel = 'First heard',
    dateAxisLabel,
    ariaLabel,
  }: Props = $props();

  // Styling constants
  const MAX_X_TICKS = 8;
  const TICK_SPACING_PX = 80;
  const X_AXIS_LABEL_OFFSET = 35;
  const BAND_PADDING = 0.3;
  const MARKER_OPACITY = 0.85;
  const MARKER_HOVER_OPACITY = 1;
  const MARKER_DIM_OPACITY = 0.3;
  const TRANSITION_MS = 120;
  const MS_PER_DAY = 1000 * 60 * 60 * 24;

  // Wider left margin so long species names on the category axis are not
  // clipped at BaseChart's default 60px left margin. Other sides keep defaults.
  const MARGIN = { top: 20, right: 20, bottom: 65, left: 140 };

  let tooltip: ChartTooltip | null = null;
  let chartContainer: HTMLDivElement | null = null;

  let chartContext = $state<ChartRenderContext | null>(null);

  function drawChart(context: ChartRenderContext) {
    const { chartGroup, innerWidth, innerHeight, theme } = context;

    chartGroup.selectAll('*').remove();

    if (!data.length || innerWidth <= 0 || innerHeight <= 0) {
      return;
    }

    // Sort ascending by first-heard date so the earliest species sits at top.
    const sorted = [...data].sort((a, b) => a.firstHeard.getTime() - b.firstHeard.getTime());

    // X domain: explicit dateRange, else data extent padded by one day on each
    // side so single-day markers are not clipped at the chart edges.
    const dates = sorted.map(d => d.firstHeard);
    const dateExtent = extent<Date>(dates);
    let domainStart: Date;
    let domainEnd: Date;
    if (dateRange) {
      [domainStart, domainEnd] = dateRange;
    } else if (dateExtent[0] && dateExtent[1]) {
      domainStart = new Date(dateExtent[0].getTime() - MS_PER_DAY);
      domainEnd = new Date(dateExtent[1].getTime() + 2 * MS_PER_DAY);
    } else {
      domainStart = new Date();
      domainEnd = new Date();
    }

    const xScale = createTimeScale({
      domain: [domainStart, domainEnd],
      range: [0, innerWidth],
    });
    const yScale = createBandScale({
      domain: sorted.map(d => d.commonName),
      range: [0, innerHeight],
      padding: BAND_PADDING,
    });

    // Daily-granularity markers, so the bucket is never 'day' (clock times).
    const span = xScale.domain();
    const dateTick = createDateAxisFormatter(pickDateRangeBucket([span[0], span[1]]));

    const xAxis = createAxis({
      scale: xScale,
      orientation: 'bottom',
      tickFormat: (d: AxisDomain) => dateTick(d as Date),
      tickCount: Math.min(MAX_X_TICKS, Math.max(2, Math.floor(innerWidth / TICK_SPACING_PX))),
    });
    // Band scales are valid AxisScales at runtime; widen the type for createAxis.
    const yAxis = createAxis({
      scale: yScale as unknown as AxisScale<AxisDomain>,
      orientation: 'left',
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

    const colors = generateSpeciesColors(sorted.length, theme);

    // Width of one-day marker in pixels (minimum a few px so it stays visible).
    const dayWidth = Math.max(4, xScale(new Date(MS_PER_DAY)) - xScale(new Date(0)));

    const markers = chartGroup
      .append('g')
      .attr('class', 'markers')
      .selectAll('rect.timeline-marker')
      .data(sorted)
      .enter()
      .append('rect')
      .attr('class', 'timeline-marker')
      .attr('x', d => xScale(d.firstHeard))
      .attr('y', d => yScale(d.commonName) ?? 0)
      .attr('width', dayWidth)
      .attr('height', yScale.bandwidth())
      .attr('rx', 2)
      .style('fill', (_d, i) => colors[i % colors.length])
      .style('opacity', MARKER_OPACITY);

    markers
      .on('mouseenter', function (event: MouseEvent, d: NewSpeciesDatum) {
        select(this).transition().duration(TRANSITION_MS).style('opacity', MARKER_HOVER_OPACITY);
        markers.filter(other => other !== d).style('opacity', MARKER_DIM_OPACITY);
        tooltip?.show({
          title: d.commonName,
          items: [{ label: firstHeardLabel, value: getLocalDateString(d.firstHeard) }],
          x: event.clientX,
          y: event.clientY,
        });
      })
      .on('mousemove', function (event: MouseEvent) {
        tooltip?.move(event.clientX, event.clientY);
      })
      .on('mouseleave', function () {
        markers.transition().duration(TRANSITION_MS).style('opacity', MARKER_OPACITY);
        tooltip?.hide();
      });
  }

  // Repaint when any drawChart input changes (data or label/locale).
  $effect(() => {
    void data;
    void dateRange;
    void dateAxisLabel;
    void firstHeardLabel;
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

<div class="new-species-timeline-chart" bind:this={chartContainer}>
  <BaseChart {width} {height} {ariaLabel} margin={MARGIN} responsive={true}>
    {#snippet children(context)}
      {((chartContext = context), '')}
    {/snippet}
  </BaseChart>
</div>

<style>
  .new-species-timeline-chart {
    width: 100%;
    height: 100%;
    min-height: 300px;
  }

  :global(.new-species-timeline-chart rect.timeline-marker) {
    transition: opacity 0.12s ease;
  }
</style>
