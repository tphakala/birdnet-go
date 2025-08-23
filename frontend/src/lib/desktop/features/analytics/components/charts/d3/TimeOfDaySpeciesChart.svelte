<!-- Multi-Species Time of Day Chart -->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import * as d3 from 'd3';

  import BaseChart from './BaseChart.svelte';
  import { createLinearScale } from './utils/scales';
  import { createAxis, styleAxis, addAxisLabel, createHourAxisFormatter } from './utils/axes';
  import { ChartTooltip, addCrosshair, createLegend } from './utils/interactions';
  import { generateSpeciesColors, type ChartTheme } from './utils/theme';

  interface HourlyData {
    hour: number;
    count: number;
  }

  interface SpeciesTimeData {
    species: string;
    commonName: string;
    data: HourlyData[];
    visible: boolean;
    color?: string;
  }

  interface Props {
    data: SpeciesTimeData[];
    width?: number;
    height?: number;
    selectedSpecies?: string[];
    onSpeciesToggle?: (_species: string, _visible: boolean) => void;
  }

  let {
    data = [],
    width = 800,
    height = 400,
    selectedSpecies = [],
    onSpeciesToggle,
  }: Props = $props();

  // Component state
  let tooltip: ChartTooltip | null = null;

  // Prepare data with colors
  const chartData = $derived(() => {
    if (!data.length) return [];

    const colors = generateSpeciesColors(data.length, {
      primary: '#3b82f6',
      secondary: '#6366f1',
      accent: '#f59e0b',
      success: '#10b981',
      warning: '#f59e0b',
      error: '#ef4444',
    } as ChartTheme);

    return data.map((species, index) => ({
      ...species,
      // eslint-disable-next-line security/detect-object-injection -- Safe: internal array access with controlled index
      color: species.color || colors[index],
      visible: selectedSpecies.length === 0 || selectedSpecies.includes(species.species),
    }));
  });

  // Get visible species data
  const visibleData = $derived(() => chartData().filter(s => s.visible));

  // Calculate scales
  const scales = $derived(() => {
    const visible = visibleData();
    if (!visible.length) return null;

    const maxCount =
      d3.max(
        visible.flatMap(s => s.data),
        d => d.count
      ) ?? 0;

    return {
      x: d3.scaleLinear().domain([0, 23]).range([0, 100]), // Percentage-based for responsiveness
      y: d3
        .scaleLinear()
        .domain([0, maxCount * 1.1])
        .range([100, 0]), // Inverted for SVG
    };
  });

  // Store chart context
  let chartContext = $state<{
    svg: d3.Selection<SVGSVGElement, unknown, null, undefined>;
    chartGroup: d3.Selection<globalThis.SVGGElement, unknown, null, undefined>;
    innerWidth: number;
    innerHeight: number;
    theme: ChartTheme;
  } | null>(null);

  function drawChart(context: {
    svg: d3.Selection<SVGSVGElement, unknown, null, undefined>;
    chartGroup: d3.Selection<globalThis.SVGGElement, unknown, null, undefined>;
    innerWidth: number;
    innerHeight: number;
    theme: ChartTheme;
  }) {
    // Store context for later use
    chartContext = context;

    const currentScales = scales();
    if (!currentScales || !visibleData().length) {
      // Clear existing content
      if (context.chartGroup) {
        context.chartGroup.selectAll('*').remove();
      }
      return '';
    }

    const { svg: _svg, chartGroup, innerWidth, innerHeight, theme } = context; // eslint-disable-line no-unused-vars

    // Update scale ranges for current dimensions
    const xScale = createLinearScale({
      domain: [0, 23],
      range: [0, innerWidth],
    });

    const yScale = createLinearScale({
      domain: [0, currentScales.y.domain()[1]],
      range: [innerHeight, 0],
    });

    // Clear existing content
    if (chartGroup) {
      chartGroup.selectAll('*').remove();
    }

    // Create axes
    const xAxis = createAxis({
      scale: xScale,
      orientation: 'bottom',
      tickFormat: createHourAxisFormatter() as any,
      tickCount: 12,
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
        text: 'Time of Day',
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
        text: 'Detection Count',
        orientation: 'left',
        offset: 45,
        width: innerWidth,
        height: innerHeight,
      },
      theme.axis
    );

    // Line generator
    const line = d3
      .line<HourlyData>()
      .x(d => xScale(d.hour))
      .y(d => yScale(d.count))
      .curve(d3.curveMonotoneX);

    // Draw lines for each species
    const linesGroup = chartGroup.append('g').attr('class', 'lines');

    const speciesLines = linesGroup
      .selectAll('.species-line')
      .data(visibleData(), (d: any) => (d as SpeciesTimeData).species);

    // Enter new lines
    speciesLines
      .enter()
      .append('path')
      .attr('class', 'species-line')
      .attr('d', d => line(d.data))
      .style('fill', 'none')
      .style('stroke', d => d.color ?? '#999999')
      .style('stroke-width', 2)
      .style('opacity', 0)
      .transition()
      .duration(500)
      .style('opacity', 0.8);

    // Update existing lines
    speciesLines
      .transition()
      .duration(300)
      .attr('d', d => line(d.data))
      .style('stroke', d => d.color ?? '#999999');

    // Exit old lines
    speciesLines.exit().transition().duration(300).style('opacity', 0).remove();

    // Add data points for better interaction
    const pointsGroup = chartGroup.append('g').attr('class', 'points');

    visibleData().forEach(species => {
      const speciesPoints = pointsGroup
        .selectAll(`.points-${species.species.replace(/\s+/g, '-')}`)
        .data(species.data);

      speciesPoints
        .enter()
        .append('circle')
        .attr('class', `points-${species.species.replace(/\s+/g, '-')}`)
        .attr('cx', d => xScale(d.hour))
        .attr('cy', d => yScale(d.count))
        .attr('r', 0)
        .style('fill', species.color ?? '#999999')
        .style('opacity', 0)
        .on('mouseenter', function (event, d) {
          // Highlight this point
          d3.select(this).transition().duration(150).attr('r', 6).style('opacity', 1);

          // Show tooltip
          const tooltipData = {
            title: `${species.commonName}`,
            items: [
              { label: 'Time', value: `${d.hour}:00` },
              { label: 'Detections', value: d.count },
              { label: 'Species', value: species.species },
            ],
            x: event.clientX,
            y: event.clientY,
          };

          tooltip?.show(tooltipData);
        })
        .on('mouseleave', function () {
          d3.select(this).transition().duration(150).attr('r', 3).style('opacity', 0.6);

          tooltip?.hide();
        })
        .transition()
        .duration(500)
        .delay((_, i) => i * 20)
        .attr('r', 3)
        .style('opacity', 0.6);
    });

    // Add crosshair
    addCrosshair(chartGroup, {
      width: innerWidth,
      height: innerHeight,
      onMove: (x, y) => {
        const hour = Math.round(xScale.invert(x));
        /* const _count = Math.round(yScale.invert(y)); */

        if (hour >= 0 && hour <= 23) {
          // Track hovered hour for potential future use
          // hoveredHour = hour;

          // Show crosshair tooltip with all species data at this hour
          const hourData = visibleData()
            .map(species => {
              const hourPoint = species.data.find(d => d.hour === hour);
              return hourPoint
                ? {
                    species: species.commonName,
                    count: hourPoint.count,
                    color: species.color ?? '#999999',
                  }
                : null;
            })
            .filter((item): item is NonNullable<typeof item> => item !== null);

          if (hourData.length > 0) {
            const tooltipData = {
              title: `${hour}:00`,
              items: hourData.map(item => ({
                label: item.species,
                value: item.count.toString(),
                color: item.color,
              })),
              x: x + 60, // Offset from chart area
              y: y + 60,
            };

            tooltip?.show(tooltipData);
          }
        }
      },
      onLeave: () => {
        tooltip?.hide();
      },
    });

    // Create legend
    const legendItems = visibleData().map(species => ({
      label: species.commonName,
      color: species.color ?? '#999999',
      visible: species.visible,
    }));

    if (legendItems.length > 0) {
      createLegend(chartGroup, {
        items: legendItems,
        position: { x: innerWidth - 150, y: 20 },
        itemHeight: 20,
        onToggle: (label, visible) => {
          const species = visibleData().find(s => s.commonName === label);
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
    const visible = visibleData(); // Track computed visible data
    const chartScales = scales(); // Track scale changes
    const ctx = chartContext; // Get the D3 context from snippet

    // CRITICAL: Force reactive dependency tracking without logging
    void {
      dataLength: currentData.length,
      hasChartContext: !!ctx,
      visibleDataLength: visible.length,
      hasScales: !!chartScales,
    };

    if (ctx && visible.length > 0 && chartScales) {
      drawChart(ctx);
    } else if (ctx && ctx.chartGroup && (!visible.length || !chartScales)) {
      ctx.chartGroup.selectAll('*').remove();
    }
  });

  onMount(() => {
    // Initialize tooltip
    const container = document.querySelector('.chart-container');
    if (container) {
      tooltip = new ChartTooltip(container as HTMLElement);
    }
  });

  onDestroy(() => {
    tooltip?.destroy();
  });
</script>

<div class="time-of-day-species-chart">
  <BaseChart {width} {height} responsive={true}>
    {#snippet children(context)}
      <!-- CRITICAL: Capture D3 context from BaseChart for use in $effect
           Snippets only render once, so we can't call drawChart directly here.
           Instead we store context and let the $effect handle reactive updates. -->
      {(chartContext = context)}
    {/snippet}
  </BaseChart>
</div>

<style>
  .time-of-day-species-chart {
    width: 100%;
    height: 100%;
    min-height: 400px;
  }

  :global(.species-line) {
    transition:
      stroke-width 0.2s ease,
      opacity 0.2s ease;
  }

  :global(.species-line:hover) {
    stroke-width: 3px !important;
    opacity: 1 !important;
  }

  :global(.legend-item:hover) {
    opacity: 0.8;
  }

  :global(.points circle) {
    transition:
      r 0.15s ease,
      opacity 0.15s ease;
  }
</style>
