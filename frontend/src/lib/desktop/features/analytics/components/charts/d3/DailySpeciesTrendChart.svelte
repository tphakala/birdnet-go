<!-- Daily Species Trend Chart -->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { extent } from 'd3-array';
  import { select, type Selection } from 'd3-selection';
  import { line, area, curveMonotoneX } from 'd3-shape';
  import { scaleTime, scaleLinear } from 'd3-scale';
  import type { AxisDomain } from 'd3-axis';
  import type { ZoomTransform } from 'd3-zoom';

  import BaseChart from './BaseChart.svelte';
  import { getLocalDateString } from '$lib/utils/date';
  import { createTimeScale, createLinearScale } from './utils/scales';
  import { createAxis, styleAxis, addAxisLabel, createDateAxisFormatter } from './utils/axes';
  import {
    ChartTooltip,
    addZoomBehavior,
    addBrushBehavior,
    createLegend,
  } from './utils/interactions';
  import { generateSpeciesColors, getCurrentTheme, type ChartTheme } from './utils/theme';

  interface DailyData {
    date: Date;
    count: number;
  }

  interface SpeciesTrendData {
    species: string;
    commonName: string;
    data: DailyData[];
    visible: boolean;
    color?: string;
    id?: string; // Add stable ID for legend toggling
  }

  interface Props {
    data: SpeciesTrendData[];
    width?: number;
    height?: number;
    selectedSpecies?: string[];
    dateRange?: [Date, Date];
    showRelative?: boolean; // Show as percentage of total
    enableZoom?: boolean;
    enableBrush?: boolean;
    onSpeciesToggle?: (_species: string, _visible: boolean) => void;
    onDateRangeChange?: (_range: [Date, Date]) => void;
  }

  let {
    data = [],
    width = 800,
    height = 400,
    selectedSpecies = [],
    dateRange,
    showRelative = false,
    enableZoom = true,
    enableBrush = false,
    onSpeciesToggle,
    onDateRangeChange,
  }: Props = $props();

  // Component state
  let tooltip: ChartTooltip | null = null;
  let zoomTransform: ZoomTransform | null = null;
  let chartContainer: HTMLDivElement | null = null;
  // let brushSelection: [Date, Date] | null = null;

  // Prepare data with colors
  const chartData = $derived(() => {
    if (!data.length) return [];

    const currentTheme = getCurrentTheme();
    const colors = generateSpeciesColors(data.length, currentTheme);

    return data.map((species, index) => ({
      ...species,
      // eslint-disable-next-line security/detect-object-injection -- Safe: internal array access with controlled index
      color: species.color || colors[index],
      visible: selectedSpecies.length === 0 || selectedSpecies.includes(species.species),
      id: species.id || species.species, // Use species name as fallback ID
    }));
  });

  // Get visible species data
  const visibleData = $derived(() => chartData().filter(s => s.visible));

  // Process data for relative display
  const processedData = $derived(() => {
    if (!visibleData().length || !showRelative) return visibleData();

    // Calculate total counts per day
    const dailyTotals = new Map<string, number>();

    visibleData().forEach(species => {
      species.data.forEach(point => {
        const dateStr = getLocalDateString(point.date);
        const currentTotal = dailyTotals.get(dateStr) ?? 0;
        dailyTotals.set(dateStr, currentTotal + point.count);
      });
    });

    // Convert to percentages
    return visibleData().map(species => ({
      ...species,
      data: species.data.map(point => {
        const dateStr = getLocalDateString(point.date);
        const total = dailyTotals.get(dateStr) ?? 1;
        return {
          ...point,
          count: total > 0 ? (point.count / total) * 100 : 0,
        };
      }),
    }));
  });

  // Calculate scales
  const scales = $derived(() => {
    const processed: SpeciesTrendData[] = processedData();
    if (!processed.length) return null;

    const allDates: Date[] = processed.flatMap(species => species.data.map(point => point.date));
    const allCounts: number[] = processed.flatMap(species =>
      species.data.map(point => point.count)
    );

    const dateExtent = extent<Date>(allDates);
    const countExtent = extent<number>(allCounts);

    // Handle cases where extent returns undefined
    const safeDateExtent: [Date, Date] =
      dateExtent[0] && dateExtent[1] ? [dateExtent[0], dateExtent[1]] : [new Date(), new Date()];
    const safeCountExtent: [number, number] =
      countExtent[0] !== undefined && countExtent[1] !== undefined
        ? [countExtent[0], countExtent[1]]
        : [0, 100];

    return {
      x: scaleTime()
        .domain(dateRange || safeDateExtent)
        .range([0, 100]),
      y: scaleLinear()
        .domain([0, (safeCountExtent[1] || 0) * 1.1])
        .range([100, 0]),
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
    // Store context for later use
    chartContext = context;

    const processed = processedData();

    if (!scales() || !processed.length) {
      // Clear existing content
      if (context.chartGroup) {
        context.chartGroup.selectAll('*').remove();
      }
      return '';
    }

    const { svg, chartGroup, innerWidth, innerHeight, theme } = context;

    // Get scales safely
    const s = scales();
    if (!s) {
      throw new Error('Scales not available for chart rendering');
    }

    // Update scale ranges for current dimensions
    const xScale = createTimeScale({
      domain: s.x.domain() as [Date, Date],
      range: [0, innerWidth],
    });

    const yScale = createLinearScale({
      domain: s.y.domain() as [number, number],
      range: [innerHeight, 0],
    });

    // Apply zoom transform if exists
    if (zoomTransform) {
      const newXScale = zoomTransform.rescaleX(xScale);
      xScale.domain(newXScale.domain());
    }

    // Clear existing content
    if (chartGroup) {
      chartGroup.selectAll('*').remove();
    }

    // Determine appropriate date format based on range
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
      tickFormat: showRelative ? (_d: AxisDomain) => `${Number(_d).toFixed(0)}%` : undefined,
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
        text: 'Date',
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
        text: showRelative ? 'Percentage of Total Detections' : 'Detection Count',
        orientation: 'left',
        offset: 45,
        width: innerWidth,
        height: innerHeight,
      },
      theme.axis
    );

    // Line generator
    const lineGenerator = line<DailyData>()
      .x(d => xScale(d.date))
      .y(d => yScale(d.count))
      .curve(curveMonotoneX);

    // Area generator for filled areas (optional enhancement)
    const areaGenerator = area<DailyData>()
      .x(d => xScale(d.date))
      .y0(innerHeight)
      .y1(d => yScale(d.count))
      .curve(curveMonotoneX);

    // Create clip path for zoom (unique per instance)
    const clipPathId = `chart-area-${Math.random().toString(36).slice(2, 8)}`;
    chartGroup
      .append('defs')
      .append('clipPath')
      .attr('id', clipPathId)
      .append('rect')
      .attr('width', innerWidth)
      .attr('height', innerHeight);

    const chartArea = chartGroup.append('g').attr('clip-path', `url(#${clipPathId})`);

    // Draw lines for each species
    const linesGroup = chartArea.append('g').attr('class', 'lines');

    processed.forEach((species: SpeciesTrendData) => {
      // Add area (subtle background fill)
      linesGroup
        .append('path')
        .datum(species.data)
        .attr('class', `area-${species.species.replace(/\s+/g, '-')}`)
        .attr('d', areaGenerator)
        .style('fill', species.color ?? '#999999')
        .style('opacity', 0.1)
        .style('pointer-events', 'none');

      // Add line
      linesGroup
        .append('path')
        .datum(species.data)
        .attr('class', `line-${species.species.replace(/\s+/g, '-')}`)
        .attr('d', lineGenerator)
        .style('fill', 'none')
        .style('stroke', species.color ?? '#999999')
        .style('stroke-width', 2)
        .style('opacity', 0.8);

      // Add data points
      const pointsGroup = linesGroup
        .append('g')
        .attr('class', `points-${species.species.replace(/\s+/g, '-')}`);

      pointsGroup
        .selectAll('circle')
        .data(species.data)
        .enter()
        .append('circle')
        .attr('cx', (d: DailyData) => xScale(d.date))
        .attr('cy', (d: DailyData) => yScale(d.count))
        .attr('r', 3)
        .style('fill', species.color ?? '#999999')
        .style('opacity', 0.7)
        .on('mouseenter', function (event: MouseEvent, d: DailyData) {
          // Highlight this point
          select(this).transition().duration(150).attr('r', 6).style('opacity', 1);

          // Show tooltip
          const tooltipData = {
            title: species.commonName,
            items: [
              { label: 'Date', value: getLocalDateString(d.date) },
              {
                label: showRelative ? 'Percentage' : 'Detections',
                value: showRelative ? `${d.count.toFixed(1)}%` : d.count.toString(),
              },
            ],
            x: event.clientX,
            y: event.clientY,
          };

          tooltip?.show(tooltipData);
        })
        .on('mouseleave', function () {
          select(this).transition().duration(150).attr('r', 3).style('opacity', 0.7);

          tooltip?.hide();
        });
    });

    // Add zoom behavior if enabled
    if (enableZoom) {
      addZoomBehavior(svg, {
        scaleExtent: [1, 10],
        translateExtent: [
          [0, 0],
          [innerWidth, innerHeight],
        ],
        onZoom: transform => {
          zoomTransform = transform;

          const newXScale = transform.rescaleX(xScale);

          // Update axes
          xAxisGroup.call(
            createAxis({
              scale: newXScale,
              orientation: 'bottom',
              tickFormat: (d: AxisDomain) => dateTick(d as Date),
              tickCount: Math.min(8, Math.floor(innerWidth / 80)),
            })
          );

          // Update lines and points
          linesGroup.selectAll('path').attr('d', function () {
            const className = (this as globalThis.SVGPathElement).className.baseVal;
            const isArea = className.includes('area-');
            const generator = isArea
              ? areaGenerator.x(d => newXScale(d.date))
              : lineGenerator.x(d => newXScale(d.date));
            return generator(select(this).datum() as DailyData[]);
          });

          linesGroup.selectAll('circle').attr('cx', (d: unknown) => {
            const dailyData = d as DailyData;
            return newXScale(dailyData.date);
          });
        },
      });
    }

    // Add brush behavior if enabled
    if (enableBrush) {
      const brushGroup = chartGroup.append('g').attr('class', 'brush');

      addBrushBehavior(brushGroup, {
        extent: [
          [0, 0],
          [innerWidth, innerHeight],
        ],
        onEnd: selection => {
          if (selection) {
            const [x1, x2] = selection;
            const dateRange: [Date, Date] = [xScale.invert(x1), xScale.invert(x2)];
            // brushSelection = dateRange;
            onDateRangeChange?.(dateRange);
          } else {
            // brushSelection = null;
          }
        },
      });
    }

    // Create legend
    const processedForLegend = processedData();
    const legendItems = processedForLegend.map((species: SpeciesTrendData) => ({
      id: species.id || species.species,
      label: species.commonName,
      color: species.color ?? '#999999',
      visible: species.visible,
    }));

    if (legendItems.length > 0) {
      createLegend(chartGroup, {
        items: legendItems,
        position: { x: innerWidth - 150, y: 20 },
        itemHeight: 20,
        onToggle: (id, visible) => {
          const species = processedForLegend.find(
            (s: SpeciesTrendData) => (s.id || s.species) === id
          );
          if (species) {
            onSpeciesToggle?.(species.species, visible);
          }
        },
      });
    }

    return '';
  }

  // Re-render chart when data changes
  // IMPORTANT: Svelte 5 reactivity pattern for D3 charts with snippets
  // The $effect must explicitly read all reactive values to track them.
  // Without these reads, the effect won't re-run when data changes.
  // This is because $derived is lazy and snippets only render once.
  $effect(() => {
    // Force evaluation of reactive dependencies by accessing them
    // These assignments are CRITICAL - they make the effect track these values
    const currentData = data; // Track the data prop changes
    const processed = processedData(); // Track computed processed data
    const chartScales = scales(); // Track scale changes
    const ctx = chartContext; // Get the D3 context from snippet

    // CRITICAL: Force reactive dependency tracking without logging
    void {
      dataLength: currentData.length,
      hasChartContext: !!ctx,
      processedDataLength: processed.length,
      hasScales: !!chartScales,
    };

    if (ctx && processed.length > 0 && chartScales) {
      drawChart(ctx);
    } else if (ctx && ctx.chartGroup && (!processed.length || !chartScales)) {
      ctx.chartGroup.selectAll('*').remove();
    }
  });

  onMount(() => {
    // Initialize tooltip - use component's own container
    if (chartContainer) {
      tooltip = new ChartTooltip(chartContainer);
    }
  });

  onDestroy(() => {
    tooltip?.destroy();
  });
</script>

<div class="daily-species-trend-chart" bind:this={chartContainer}>
  <BaseChart {width} {height} responsive={true}>
    {#snippet children(context)}
      <!-- CRITICAL: Capture D3 context from BaseChart for use in $effect
           Snippets only render once, so we can't call drawChart directly here.
           Instead we store context and let the $effect handle reactive updates. -->
      {((chartContext = context), '')}
    {/snippet}
  </BaseChart>
</div>

<style>
  .daily-species-trend-chart {
    width: 100%;
    height: 100%;
    min-height: 400px;
  }

  :global([class^='line-']) {
    transition:
      stroke-width 0.2s ease,
      opacity 0.2s ease;
  }

  :global([class^='line-']:hover) {
    stroke-width: 3px !important;
    opacity: 1 !important;
  }

  :global(.brush .selection) {
    fill: rgb(59 130 246 / 0.2);
    stroke: #3b82f6;
  }

  :global(.zoom) {
    cursor: grab;
  }

  :global(.zoom:active) {
    cursor: grabbing;
  }
</style>
