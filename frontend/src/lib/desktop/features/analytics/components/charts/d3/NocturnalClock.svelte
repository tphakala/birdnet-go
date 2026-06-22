<!--
  Nocturnal activity clock (design spec section 6.4).

  A 24-hour radial histogram of detection counts (one bar per hour, midnight at the top, clockwise)
  with the daytime arc shaded by sunrise/sunset so nocturnal activity stands out. On narrow
  viewports the radial gets too small to read, so it falls back to a linear 24-hour bar layout. Sun
  times come from a separate endpoint and may be unavailable (polar day/night, or no station
  coordinates); when so, the bars render without day/night shading.
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { select } from 'd3-selection';
  import { arc as d3arc } from 'd3-shape';
  import { scaleBand, scaleLinear } from 'd3-scale';
  import type { AxisDomain, AxisScale } from 'd3-axis';

  import BaseChart from './BaseChart.svelte';
  import { createAxis, styleAxis, addAxisLabel, createHourAxisFormatter } from './utils/axes';
  import { ChartTooltip } from './utils/interactions';
  import { type ChartTheme } from './utils/theme';
  import {
    hourlyTotal,
    maxHourly,
    peakHour,
    minuteToAngle,
    polarToCartesian,
    formatMinuteOfDay,
    arcAngles,
    dayRegionSegments,
    HOURS_PER_DAY,
    MINUTES_PER_HOUR,
    type NocturnalClockData,
  } from './utils/nocturnal';
  import { t } from '$lib/i18n';

  interface Props {
    data: NocturnalClockData;
    width?: number;
    height?: number;
    ariaLabel?: string;
  }

  let { data, width = 800, height = 400, ariaLabel }: Props = $props();

  // Layout / behavior constants.
  const MARGIN = { top: 20, right: 20, bottom: 36, left: 44 };
  // Below this inner width the radial dial is too cramped to read; fall back to a linear bar chart.
  const RADIAL_MIN_WIDTH = 360;
  const RADIAL_INNER_FRACTION = 0.38; // hollow center radius as a fraction of the dial radius
  const RADIAL_LABEL_PAD = 18; // gap between the dial edge and the hour tick labels
  const BAR_PAD_ANGLE = 0.01; // small angular gap between adjacent radial bars
  const DAY_FILL_OPACITY = 0.18;
  const TWILIGHT_FILL_OPACITY = 0.1;
  const NIGHT_FILL_OPACITY = 0.06;
  const BAR_IDLE_OPACITY = 0.85;
  const HOUR_TICK_STEP = 6; // label the dial every 6 hours (0, 6, 12, 18)
  const LINEAR_X_TICK_STEP = 3; // label the linear axis every 3 hours
  const X_AXIS_LABEL_OFFSET = 30;

  let tooltip: ChartTooltip | null = null;
  let chartContainer: HTMLDivElement | null = null;

  // Bounded, finite hourly counts indexed 0..23 (defensive against a short/ragged payload).
  const hours = $derived.by<number[]>(() => {
    const out: number[] = [];
    for (let h = 0; h < HOURS_PER_DAY; h++) {
      // eslint-disable-next-line security/detect-object-injection -- h is a bounded loop index
      const c = data.hourly[h];
      out.push(Number.isFinite(c) ? c : 0);
    }
    return out;
  });

  // Screen-reader summary so the chart is not opaque to assistive tech (spec section 7).
  const summary = $derived.by(() => {
    const total = hourlyTotal(data);
    if (total === 0) return '';
    const peak = peakHour(data);
    return t('analytics.advanced.charts.nocturnal.summary', {
      total,
      peak: peak === null ? '-' : formatMinuteOfDay(peak * MINUTES_PER_HOUR),
    });
  });

  let chartContext = $state<{
    svg: import('d3-selection').Selection<SVGSVGElement, unknown, null, undefined>;
    chartGroup: import('d3-selection').Selection<globalThis.SVGGElement, unknown, null, undefined>;
    innerWidth: number;
    innerHeight: number;
    theme: ChartTheme;
  } | null>(null);

  function hourLabel(hour: number): string {
    return formatMinuteOfDay((hour % HOURS_PER_DAY) * MINUTES_PER_HOUR);
  }

  function showBarTooltip(event: MouseEvent, hour: number, count: number): void {
    tooltip?.show({
      title: t('analytics.advanced.charts.nocturnal.tooltipHour', {
        start: hourLabel(hour),
        end: hourLabel(hour + 1),
      }),
      items: [
        {
          label: t('analytics.advanced.charts.nocturnal.tooltipCount'),
          value: String(count),
        },
      ],
      x: event.clientX,
      y: event.clientY,
    });
  }

  // Draw the radial dial: a night base ring, the shaded day/twilight arcs, the hour bars, and the
  // perimeter hour ticks. Angles use the clock convention (minute 0 at top, clockwise).
  function drawRadial(context: NonNullable<typeof chartContext>): void {
    const { chartGroup, innerWidth, innerHeight, theme } = context;
    const cx = innerWidth / 2;
    const cy = innerHeight / 2;
    const dialRadius = Math.min(innerWidth, innerHeight) / 2 - RADIAL_LABEL_PAD;
    if (dialRadius <= 0) return;
    const innerRadius = dialRadius * RADIAL_INNER_FRACTION;

    const root = chartGroup
      .append('g')
      .attr('class', 'clock-radial')
      .attr('transform', `translate(${cx},${cy})`);

    const makeArc = d3arc<{
      innerRadius: number;
      outerRadius: number;
      startAngle: number;
      endAngle: number;
    }>()
      .innerRadius(d => d.innerRadius)
      .outerRadius(d => d.outerRadius)
      .startAngle(d => d.startAngle)
      .endAngle(d => d.endAngle);

    const sun = data.sun;
    // Night base ring spanning the whole dial; the day/twilight arcs are drawn on top of it.
    if (sun?.available && sun.sunrise !== null && sun.sunset !== null) {
      root
        .append('path')
        .attr('class', 'night-ring')
        .attr(
          'd',
          makeArc({ innerRadius, outerRadius: dialRadius, startAngle: 0, endAngle: 2 * Math.PI })
        )
        .style('fill', theme.secondary)
        .style('opacity', NIGHT_FILL_OPACITY);

      // Twilight bands (civil dawn -> sunrise, sunset -> civil dusk) when a genuine twilight exists.
      // arcAngles wraps the sweep forward over midnight so a band whose end lands past 00:00 still
      // shades the correct side of the dial.
      if (sun.civilDawn !== null) {
        root
          .append('path')
          .attr('class', 'twilight-arc')
          .attr(
            'd',
            makeArc({
              innerRadius,
              outerRadius: dialRadius,
              ...arcAngles(sun.civilDawn, sun.sunrise),
            })
          )
          .style('fill', theme.warning)
          .style('opacity', TWILIGHT_FILL_OPACITY);
      }
      if (sun.civilDusk !== null) {
        root
          .append('path')
          .attr('class', 'twilight-arc')
          .attr(
            'd',
            makeArc({
              innerRadius,
              outerRadius: dialRadius,
              ...arcAngles(sun.sunset, sun.civilDusk),
            })
          )
          .style('fill', theme.warning)
          .style('opacity', TWILIGHT_FILL_OPACITY);
      }

      // Daytime arc (sunrise -> sunset).
      root
        .append('path')
        .attr('class', 'day-arc')
        .attr(
          'd',
          makeArc({ innerRadius, outerRadius: dialRadius, ...arcAngles(sun.sunrise, sun.sunset) })
        )
        .style('fill', theme.warning)
        .style('opacity', DAY_FILL_OPACITY);
    }

    // Hour bars: each bar grows outward from the inner radius proportional to its count.
    const rScale = scaleLinear()
      .domain([0, Math.max(1, maxHourly(data))])
      .range([innerRadius, dialRadius]);
    const sliceAngle = (2 * Math.PI) / HOURS_PER_DAY;

    const bars = root.append('g').attr('class', 'hour-bars');
    hours.forEach((count, hour) => {
      bars
        .append('path')
        .attr('class', 'hour-bar')
        .attr(
          'd',
          makeArc({
            innerRadius,
            outerRadius: rScale(count),
            startAngle: hour * sliceAngle + BAR_PAD_ANGLE,
            endAngle: (hour + 1) * sliceAngle - BAR_PAD_ANGLE,
          })
        )
        .style('fill', theme.primary)
        .style('opacity', BAR_IDLE_OPACITY)
        .on('mouseenter', function (event: MouseEvent) {
          select(this).style('opacity', 1);
          showBarTooltip(event, hour, count);
        })
        .on('mouseleave', function () {
          select(this).style('opacity', BAR_IDLE_OPACITY);
          tooltip?.hide();
        });
    });

    // Perimeter hour ticks (every HOUR_TICK_STEP hours).
    const ticks = root.append('g').attr('class', 'hour-ticks');
    for (let hour = 0; hour < HOURS_PER_DAY; hour += HOUR_TICK_STEP) {
      const { x, y } = polarToCartesian(
        0,
        0,
        dialRadius + RADIAL_LABEL_PAD * 0.6,
        minuteToAngle(hour * MINUTES_PER_HOUR)
      );
      ticks
        .append('text')
        .attr('class', 'hour-tick')
        .attr('x', x)
        .attr('y', y)
        .attr('text-anchor', 'middle')
        .attr('dominant-baseline', 'middle')
        .attr('aria-hidden', 'true')
        .style('fill', theme.axis.color)
        .style('font-size', theme.axis.fontSize)
        .text(hourLabel(hour));
    }
  }

  // Draw the linear fallback: a 24-bar histogram with the daytime region shaded behind the bars.
  function drawLinear(context: NonNullable<typeof chartContext>): void {
    const { chartGroup, innerWidth, innerHeight, theme } = context;

    const root = chartGroup.append('g').attr('class', 'clock-linear');

    const xScale = scaleBand<number>()
      .domain(Array.from({ length: HOURS_PER_DAY }, (_, h) => h))
      .range([0, innerWidth])
      .padding(0.15);
    const yScale = scaleLinear()
      .domain([0, Math.max(1, maxHourly(data))])
      .range([innerHeight, 0])
      .nice();

    // Daytime region (sunrise -> sunset) shaded behind the bars; minute-of-day maps to x linearly.
    // dayRegionSegments splits the band in two when it wraps past midnight so it never collapses.
    const sun = data.sun;
    if (sun?.available && sun.sunrise !== null && sun.sunset !== null) {
      for (const seg of dayRegionSegments(sun.sunrise, sun.sunset, innerWidth)) {
        if (seg.width <= 0) continue;
        root
          .append('rect')
          .attr('class', 'day-region')
          .attr('x', seg.x)
          .attr('y', 0)
          .attr('width', seg.width)
          .attr('height', innerHeight)
          .style('fill', theme.warning)
          .style('opacity', DAY_FILL_OPACITY);
      }
    }

    // Bars.
    const bars = root.append('g').attr('class', 'hour-bars');
    hours.forEach((count, hour) => {
      const x = xScale(hour) ?? 0;
      bars
        .append('rect')
        .attr('class', 'hour-bar')
        .attr('x', x)
        .attr('y', yScale(count))
        .attr('width', xScale.bandwidth())
        .attr('height', innerHeight - yScale(count))
        .style('fill', theme.primary)
        .style('opacity', BAR_IDLE_OPACITY)
        .on('mouseenter', function (event: MouseEvent) {
          select(this).style('opacity', 1);
          showBarTooltip(event, hour, count);
        })
        .on('mouseleave', function () {
          select(this).style('opacity', BAR_IDLE_OPACITY);
          tooltip?.hide();
        });
    });

    // X axis: hour-of-day labels every LINEAR_X_TICK_STEP hours (others blanked to avoid clutter).
    const hourFmt = createHourAxisFormatter();
    const xAxis = createAxis({
      scale: xScale as unknown as AxisScale<AxisDomain>,
      orientation: 'bottom',
      tickFormat: (d: AxisDomain) =>
        Number(d) % LINEAR_X_TICK_STEP === 0 ? hourFmt(Number(d)) : '',
    });
    const xAxisGroup = root
      .append('g')
      .attr('class', 'x-axis')
      .attr('transform', `translate(0,${innerHeight})`)
      .call(xAxis);
    styleAxis(xAxisGroup, theme.axis);

    // Y axis: detection counts.
    const yAxis = createAxis({
      scale: yScale as unknown as AxisScale<AxisDomain>,
      orientation: 'left',
    });
    const yAxisGroup = root.append('g').attr('class', 'y-axis').call(yAxis);
    styleAxis(yAxisGroup, theme.axis);

    addAxisLabel(
      root,
      {
        text: t('analytics.advanced.charts.nocturnal.axisHour'),
        orientation: 'bottom',
        offset: X_AXIS_LABEL_OFFSET,
        width: innerWidth,
        height: innerHeight,
      },
      theme.axis
    );
  }

  function drawChart(context: NonNullable<typeof chartContext>): void {
    const { chartGroup, innerWidth, innerHeight } = context;
    // A redraw removes the hovered bar, so its mouseleave would never fire; hide first.
    tooltip?.hide();
    chartGroup.selectAll('*').remove();
    if (innerWidth <= 0 || innerHeight <= 0) return;

    if (innerWidth < RADIAL_MIN_WIDTH) {
      drawLinear(context);
    } else {
      drawRadial(context);
    }
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

<div class="nocturnal-clock-chart" bind:this={chartContainer}>
  <div class="chart-area">
    <BaseChart
      {width}
      {height}
      margin={MARGIN}
      responsive={true}
      ariaLabel={ariaLabel ?? t('analytics.advanced.charts.nocturnal.ariaLabel')}
    >
      {#snippet children(context)}
        {((chartContext = context), '')}
      {/snippet}
    </BaseChart>
  </div>
  <!-- Legend maps the day/night shading to text, so the bands are not conveyed by color alone
       (matches the seasonal heatmap's legend). Shown only when sun times are available. -->
  {#if data.sun?.available && data.sun.sunrise !== null && data.sun.sunset !== null}
    <ul class="nocturnal-legend" data-testid="nocturnal-legend">
      <li>
        <span class="swatch swatch-day"></span>{t('analytics.advanced.charts.nocturnal.legendDay')}
      </li>
      {#if data.sun.civilDawn !== null || data.sun.civilDusk !== null}
        <li>
          <span class="swatch swatch-twilight"></span>{t(
            'analytics.advanced.charts.nocturnal.legendTwilight'
          )}
        </li>
      {/if}
      <li>
        <span class="swatch swatch-night"></span>{t(
          'analytics.advanced.charts.nocturnal.legendNight'
        )}
      </li>
    </ul>
  {/if}
  {#if summary}
    <p class="sr-only" data-testid="nocturnal-summary">{summary}</p>
  {/if}
</div>

<style>
  .nocturnal-clock-chart {
    display: flex;
    flex-direction: column;
    width: 100%;
    height: 100%;
    min-height: 320px;
  }

  /* The chart fills the available height; the legend takes its natural height below it. */
  .chart-area {
    flex: 1 1 auto;
    min-height: 0;
    position: relative;
  }

  .nocturnal-legend {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
    justify-content: center;
    align-items: center;
    margin: 0;
    padding: 0.25rem 0 0;
    list-style: none;
    font-size: 0.75rem;
    color: var(--color-base-content);
  }

  .nocturnal-legend li {
    display: flex;
    align-items: center;
    gap: 0.35rem;
  }

  .swatch {
    display: inline-block;
    flex-shrink: 0;
    width: 0.75rem;
    height: 0.75rem;
    border-radius: 2px;
  }

  .swatch-day {
    background-color: var(--color-warning);
    opacity: 0.55;
  }

  .swatch-twilight {
    background-color: var(--color-warning);
    opacity: 0.28;
  }

  .swatch-night {
    background-color: var(--color-secondary);
    opacity: 0.3;
  }

  :global(.clock-radial .hour-bar),
  :global(.clock-linear .hour-bar) {
    transition: opacity 0.12s ease;
  }
</style>
