<!-- Multi-Species Time of Day Chart -->
<script lang="ts">
  import { onDestroy } from 'svelte';
  import { select } from 'd3-selection';
  import { line as d3Line, curveMonotoneX } from 'd3-shape';
  import { max, scaleLinear } from 'd3';
  import type { Selection, AxisDomain } from 'd3';

  import { t } from '$lib/i18n';
  import BaseChart from './BaseChart.svelte';
  import { createLinearScale } from './utils/scales';
  import { createAxis, styleAxis, addAxisLabel, createHourAxisFormatter } from './utils/axes';
  import { ChartTooltip, addCrosshair, createLegend } from './utils/interactions';
  import { generateSpeciesColors, getCurrentTheme, type ChartTheme } from './utils/theme';

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

  let { data = [], width = 800, height = 400, selectedSpecies = [] }: Props = $props();

  // Component state
  let tooltip: ChartTooltip | null = null;

  // Prepare data with colors
  const chartData = $derived.by(() => {
    if (!data.length) return [];

    const currentTheme = getCurrentTheme();
    const colors = generateSpeciesColors(data.length, currentTheme);

    return data.map((species, index) => ({
      ...species,
      // eslint-disable-next-line security/detect-object-injection -- Safe: internal array access with controlled index
      color: species.color || colors[index],
      visible: selectedSpecies.length === 0 || selectedSpecies.includes(species.species),
    }));
  });

  // Get visible species data
  const visibleData = $derived(chartData.filter(s => s.visible));

  // Calculate scales
  const scales = $derived.by(() => {
    const visible = visibleData;
    if (!visible.length) return null;

    const maxCount =
      max(
        visible.flatMap(s => s.data),
        d => d.count
      ) ?? 0;

    return {
      x: scaleLinear().domain([0, 23]).range([0, 100]), // Percentage-based for responsiveness
      y: scaleLinear()
        .domain([0, maxCount * 1.1])
        .range([100, 0]), // Inverted for SVG
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

    const currentScales = scales;
    if (!currentScales || !visibleData.length) {
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
    const hourFormatter = createHourAxisFormatter();
    const xAxis = createAxis({
      scale: xScale,
      orientation: 'bottom',
      tickFormat: (d: AxisDomain) => hourFormatter(d as number),
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
        text: t('analytics.advanced.charts.timeOfDay.axisTime'),
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
        text: t('analytics.advanced.charts.timeOfDay.axisCount'),
        orientation: 'left',
        offset: 45,
        width: innerWidth,
        height: innerHeight,
      },
      theme.axis
    );

    // Line generator
    const line = d3Line<HourlyData>()
      .x(d => xScale(d.hour))
      .y(d => yScale(d.count))
      .curve(curveMonotoneX);

    // Draw lines for each species
    const linesGroup = chartGroup.append('g').attr('class', 'lines');

    const speciesLines = linesGroup
      .selectAll<globalThis.SVGPathElement, SpeciesTimeData>('.species-line')
      .data<SpeciesTimeData>(visibleData, d => d.species);

    // Enter new lines
    speciesLines
      .enter()
      .append('path')
      .attr('class', 'species-line')
      .attr('data-species', d => d.species)
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

    visibleData.forEach(species => {
      const speciesPoints = pointsGroup
        .selectAll(`.points-${species.species.replace(/\s+/g, '-')}`)
        .data(species.data);

      speciesPoints
        .enter()
        .append('circle')
        .attr('class', `points-${species.species.replace(/\s+/g, '-')}`)
        .attr('data-species', species.species)
        .attr('cx', d => xScale(d.hour))
        .attr('cy', d => yScale(d.count))
        .attr('r', 0)
        .style('fill', species.color ?? '#999999')
        .style('opacity', 0)
        .on('mouseenter', function (event: MouseEvent, d: HourlyData) {
          // Highlight this point
          select(this).transition().duration(150).attr('r', 6).style('opacity', 1);

          // Show tooltip
          const tooltipData = {
            title: `${species.commonName}`,
            items: [
              { label: t('analytics.advanced.charts.tooltips.time'), value: `${d.hour}:00` },
              { label: t('analytics.advanced.charts.tooltips.detections'), value: d.count },
              { label: t('analytics.advanced.charts.tooltips.species'), value: species.species },
            ],
            x: event.clientX,
            y: event.clientY,
          };

          tooltip?.show(tooltipData);
        })
        // eslint-disable-next-line no-unused-vars -- `this` context is used by D3 for DOM element reference
        .on('mouseleave', function (this: globalThis.SVGCircleElement) {
          select(this).transition().duration(150).attr('r', 3).style('opacity', 0.6);

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
      onMove: (x, _y, event) => {
        const hour = Math.round(xScale.invert(x));

        if (hour >= 0 && hour <= 23) {
          // Show crosshair tooltip with all species data at this hour
          const hourData = visibleData
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
              x: event.clientX,
              y: event.clientY,
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
    const legendItems = visibleData.map(species => ({
      id: species.species,
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
          // Toggle visibility of the corresponding line and points
          chartGroup
            .selectAll(`[data-species="${id}"]`)
            .transition()
            .duration(300)
            .style('opacity', visible ? 0.8 : 0)
            .style('pointer-events', visible ? 'all' : 'none');
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
    // Simply access reactive values - Svelte 5 tracks automatically
    if (chartContext && visibleData.length > 0 && scales) {
      drawChart(chartContext);
    } else if (chartContext?.chartGroup) {
      chartContext.chartGroup.selectAll('*').remove();
    }
  });

  // Initialize tooltip when chart context becomes available
  $effect(() => {
    const ctx = chartContext;
    if (!tooltip && ctx) {
      const node = ctx.svg?.node?.();
      const container = (node?.closest?.('.chart-container') ?? null) as HTMLElement | null;
      if (container) {
        tooltip = new ChartTooltip(container);
      }
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
