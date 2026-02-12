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
      domain: [0, scales.yMax * 1.1],
      range: [innerHeight, 0],
    });

    // Clear existing content
    if (chartGroup) {
      chartGroup.selectAll('*').remove();
    }

    // Determine date format based on range
    const timeRange = xScale.domain();
    const daysDiff = (timeRange[1].getTime() - timeRange[0].getTime()) / (1000 * 60 * 60 * 24);
    const dateFormat =
      daysDiff <= 7 ? 'day' : daysDiff <= 30 ? 'week' : daysDiff <= 365 ? 'month' : 'year';

    // Create axes
    const dateTick = createDateAxisFormatter(dateFormat);
    const xAxis = createAxis({
      scale: xScale,
      orientation: 'bottom',
      tickFormat: (d: AxisDomain) => dateTick(d as Date),
      tickCount: Math.min(8, Math.floor(innerWidth / 80)),
    });

    const yAxis = createAxis({
      scale: yScale,
      orientation: 'left',
      tickCount: 6,
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
        offset: 35,
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
        offset: 45,
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
      .style('opacity', 0.15)
      .style('pointer-events', 'none');

    // Draw line on top
    chartArea
      .append('path')
      .datum(data)
      .attr('class', 'diversity-line')
      .attr('d', lineGenerator)
      .style('fill', 'none')
      .style('stroke', theme.primary)
      .style('stroke-width', 2.5)
      .style('opacity', 0.9);

    // Add data points with tooltips
    chartArea
      .selectAll('.diversity-point')
      .data(data)
      .enter()
      .append('circle')
      .attr('class', 'diversity-point')
      .attr('cx', d => xScale(d.date))
      .attr('cy', d => yScale(d.uniqueSpecies))
      .attr('r', 3)
      .style('fill', theme.primary)
      .style('opacity', 0.7)
      .on('mouseenter', function (event: MouseEvent, d: DiversityDatum) {
        select(this).transition().duration(150).attr('r', 6).style('opacity', 1);

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
        select(this).transition().duration(150).attr('r', 3).style('opacity', 0.7);
        tooltip?.hide();
      });

    return '';
  }

  // Re-render chart when data changes
  $effect(() => {
    const currentData = data;
    const chartScales = scales;
    const ctx = chartContext;

    void {
      dataLength: currentData.length,
      hasChartContext: !!ctx,
      hasScales: !!chartScales,
    };

    if (ctx && currentData.length > 0 && chartScales) {
      drawChart(ctx);
    } else if (ctx && ctx.chartGroup && (!currentData.length || !chartScales)) {
      ctx.chartGroup.selectAll('*').remove();
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
