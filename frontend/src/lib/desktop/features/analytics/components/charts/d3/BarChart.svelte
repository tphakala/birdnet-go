<!-- Generic categorical bar chart (vertical or horizontal) -->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { max } from 'd3-array';
  import { select, type Selection } from 'd3-selection';
  import type { AxisDomain, AxisScale } from 'd3-axis';

  import BaseChart from './BaseChart.svelte';
  import { createBandScale, createLinearScale } from './utils/scales';
  import { createAxis, styleAxis, addAxisLabel, createGridLines } from './utils/axes';
  import { ChartTooltip } from './utils/interactions';
  import { generateSpeciesColors, type ChartRenderContext } from './utils/theme';

  export interface BarChartDatum {
    label: string;
    value: number;
    color?: string;
  }

  interface Props {
    data: BarChartDatum[];
    orientation?: 'vertical' | 'horizontal';
    width?: number;
    height?: number;
    valueAxisLabel?: string;
    categoryAxisLabel?: string;
    formatValue?: (_n: number) => string;
    valueTooltipLabel?: string;
    ariaLabel?: string;
  }

  let {
    data = [],
    orientation = 'vertical',
    width = 800,
    height = 400,
    valueAxisLabel,
    categoryAxisLabel,
    formatValue = (n: number) => String(n),
    valueTooltipLabel,
    ariaLabel,
  }: Props = $props();

  // Styling constants
  const VALUE_HEADROOM = 1.05;
  const VALUE_TICK_COUNT = 6;
  const AXIS_LABEL_OFFSET = 40;
  const BAND_PADDING = 0.2;
  const BAR_OPACITY = 0.85;
  const BAR_HOVER_OPACITY = 1;
  const BAR_DIM_OPACITY = 0.3;
  const TRANSITION_MS = 120;
  const CATEGORY_LABEL_ROTATION = -30;
  const ROTATED_CATEGORY_LABEL_OFFSET = 70;
  // Clearance for the vertical chart's rotated value-axis title so it does not overlap the
  // y-axis tick numbers. Worked from the widest realistic tick label ("300,000"-scale detection
  // counts, ~60px at the 12px axis font) plus D3's default axisLeft tick gap
  // (tickSizeInner 6 + tickPadding 3 = 9px) plus the rotated title's own ~13px glyph thickness
  // (ascent + descent), each with a few px of buffer: 9 + 60 + 4 + 13 + 4 = 90.
  const VERTICAL_VALUE_AXIS_LABEL_OFFSET = 76;

  // Margins per orientation. Horizontal bars put long category names on the
  // left axis, so widen the left margin to avoid clipping. Vertical bars rotate
  // their category labels along the bottom, so add bottom room for them, and
  // widen the left margin so the rotated value-axis title clears wide tick numbers
  // (see VERTICAL_VALUE_AXIS_LABEL_OFFSET for the math).
  const HORIZONTAL_MARGIN = { top: 20, right: 20, bottom: 65, left: 130 };
  const VERTICAL_MARGIN = { top: 20, right: 20, bottom: 90, left: 90 };
  const margin = $derived(orientation === 'horizontal' ? HORIZONTAL_MARGIN : VERTICAL_MARGIN);

  let tooltip: ChartTooltip | null = null;
  let chartContainer: HTMLDivElement | null = null;

  // Captured BaseChart render context.
  let chartContext = $state<ChartRenderContext | null>(null);

  function drawChart(context: ChartRenderContext) {
    const { chartGroup, innerWidth, innerHeight, theme } = context;

    chartGroup.selectAll('*').remove();

    if (!data.length || innerWidth <= 0 || innerHeight <= 0) {
      return;
    }

    const labels = data.map(d => d.label);
    const maxValue = max(data, d => d.value) ?? 0;
    const colors = generateSpeciesColors(data.length, theme);
    const colorFor = (index: number) =>
      // eslint-disable-next-line security/detect-object-injection -- index iterates the data array
      data[index].color ?? colors[index % colors.length];

    const valueDomain: [number, number] = [0, maxValue * VALUE_HEADROOM || 1];

    if (orientation === 'horizontal') {
      // x = value (linear), y = category (band)
      const xScale = createLinearScale({ domain: valueDomain, range: [0, innerWidth] });
      const yScale = createBandScale({
        domain: labels,
        range: [0, innerHeight],
        padding: BAND_PADDING,
      });

      createGridLines(chartGroup, { xScale, width: innerWidth, height: innerHeight }, theme.axis);

      const xAxis = createAxis({
        scale: xScale,
        orientation: 'bottom',
        tickCount: VALUE_TICK_COUNT,
        tickFormat: (d: AxisDomain) => formatValue(Number(d)),
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

      if (valueAxisLabel) {
        addAxisLabel(
          chartGroup,
          {
            text: valueAxisLabel,
            orientation: 'bottom',
            offset: AXIS_LABEL_OFFSET,
            width: innerWidth,
            height: innerHeight,
          },
          theme.axis
        );
      }
      if (categoryAxisLabel) {
        addAxisLabel(
          chartGroup,
          {
            text: categoryAxisLabel,
            orientation: 'left',
            offset: AXIS_LABEL_OFFSET + 20,
            width: innerWidth,
            height: innerHeight,
          },
          theme.axis
        );
      }

      const bars = chartGroup
        .append('g')
        .attr('class', 'bars')
        .selectAll('rect.bar')
        .data(data)
        .enter()
        .append('rect')
        .attr('class', 'bar')
        .attr('x', 0)
        .attr('y', d => yScale(d.label) ?? 0)
        .attr('width', d => Math.max(0, xScale(d.value)))
        .attr('height', yScale.bandwidth())
        .style('fill', (_d, i) => colorFor(i))
        .style('opacity', BAR_OPACITY);

      attachHover(bars);
    } else {
      // x = category (band), y = value (linear)
      const xScale = createBandScale({
        domain: labels,
        range: [0, innerWidth],
        padding: BAND_PADDING,
      });
      const yScale = createLinearScale({ domain: valueDomain, range: [innerHeight, 0] });

      createGridLines(chartGroup, { yScale, width: innerWidth, height: innerHeight }, theme.axis);

      // Band scales are valid AxisScales at runtime; widen the type for createAxis.
      const xAxis = createAxis({
        scale: xScale as unknown as AxisScale<AxisDomain>,
        orientation: 'bottom',
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

      // Rotate category-axis labels so the six time-of-day labels do not
      // overlap horizontally. The extra bottom margin (VERTICAL_MARGIN) leaves
      // room for the rotated text.
      xAxisGroup
        .selectAll('.tick text')
        .attr('transform', `rotate(${CATEGORY_LABEL_ROTATION})`)
        .attr('text-anchor', 'end')
        .attr('dx', '-0.5em')
        .attr('dy', '0.25em');

      if (valueAxisLabel) {
        addAxisLabel(
          chartGroup,
          {
            text: valueAxisLabel,
            orientation: 'left',
            offset: VERTICAL_VALUE_AXIS_LABEL_OFFSET,
            width: innerWidth,
            height: innerHeight,
          },
          theme.axis
        );
      }
      if (categoryAxisLabel) {
        // Sits below the rotated category labels, hence the larger offset.
        addAxisLabel(
          chartGroup,
          {
            text: categoryAxisLabel,
            orientation: 'bottom',
            offset: ROTATED_CATEGORY_LABEL_OFFSET,
            width: innerWidth,
            height: innerHeight,
          },
          theme.axis
        );
      }

      const bars = chartGroup
        .append('g')
        .attr('class', 'bars')
        .selectAll('rect.bar')
        .data(data)
        .enter()
        .append('rect')
        .attr('class', 'bar')
        .attr('x', d => xScale(d.label) ?? 0)
        .attr('y', d => yScale(d.value))
        .attr('width', xScale.bandwidth())
        .attr('height', d => Math.max(0, innerHeight - yScale(d.value)))
        .style('fill', (_d, i) => colorFor(i))
        .style('opacity', BAR_OPACITY);

      attachHover(bars);
    }
  }

  function attachHover(
    bars: Selection<globalThis.SVGRectElement, BarChartDatum, globalThis.SVGGElement, unknown>
  ) {
    const tooltipLabel = valueTooltipLabel ?? '';
    bars
      .on('mouseenter', function (event: MouseEvent, d: BarChartDatum) {
        select(this).transition().duration(TRANSITION_MS).style('opacity', BAR_HOVER_OPACITY);
        bars.filter(other => other !== d).style('opacity', BAR_DIM_OPACITY);
        tooltip?.show({
          title: d.label,
          items: [{ label: tooltipLabel, value: formatValue(d.value) }],
          x: event.clientX,
          y: event.clientY,
        });
      })
      .on('mousemove', function (event: MouseEvent) {
        tooltip?.move(event.clientX, event.clientY);
      })
      .on('mouseleave', function () {
        bars.transition().duration(TRANSITION_MS).style('opacity', BAR_OPACITY);
        tooltip?.hide();
      });
  }

  // Repaint when any drawChart input changes (data or label/format/locale).
  $effect(() => {
    void data;
    void orientation;
    void valueAxisLabel;
    void categoryAxisLabel;
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

<div class="bar-chart" bind:this={chartContainer}>
  <BaseChart {width} {height} {ariaLabel} {margin} responsive={true}>
    {#snippet children(context)}
      {((chartContext = context), '')}
    {/snippet}
  </BaseChart>
</div>

<style>
  .bar-chart {
    width: 100%;
    height: 100%;
    min-height: 300px;
  }

  :global(.bar-chart rect.bar) {
    transition: opacity 0.12s ease;
  }
</style>
