<!--
  Species ridgeline (joyplot): one overlapping area row per species showing a normalized
  per-index distribution. Built generically so it is reused across per-species distribution charts:
  the who-sings-when hour-of-day ridgeline (#1159) and the confidence distribution (#1162). The
  index meaning (hour 0..23 vs confidence bin) is supplied by the caller via xTickFormat / axisLabel.

  Each ridge is a d3-shape area with curveBasis; a shared amplitude scale maps the global-max
  density to a fixed pixel height, so a species with concentrated activity rises taller than an
  all-day one. Species color comes from utils/speciesColor.ts getSpeciesColor (rgba, so it is never
  fed to a d3 color interpolator), a shared page-scoped map so a species keeps the same color across
  the sibling charts. The scientific name is the stable D3 key; the localized common name is
  the row label (re-localized here so it tracks the visitor's locale, matching the sibling charts).
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { select, type Selection } from 'd3-selection';
  import { area as d3Area, curveBasis } from 'd3-shape';
  import { scaleLinear } from 'd3-scale';
  import { color as d3Color } from 'd3-color';
  import type { AxisDomain, AxisScale } from 'd3-axis';

  import { t } from '$lib/i18n';
  import { localizeSpeciesName } from '$lib/utils/speciesDisplay';
  import BaseChart from './BaseChart.svelte';
  import { createLinearScale } from './utils/scales';
  import {
    createAxis,
    styleAxis,
    addAxisLabel,
    createHourAxisFormatter,
    hourAxisTickValues,
  } from './utils/axes';
  import { ChartTooltip } from './utils/interactions';
  import type { ChartTheme } from './utils/theme';
  import { fitTextNode } from './utils/labels';
  import { getSpeciesColor, registerChart } from './utils/speciesColor';
  import {
    ridgelineLayout,
    peakIndex,
    RIDGE_OVERLAP,
    type RidgelineSeries,
  } from './utils/ridgeline';

  interface Props {
    /** One row per species: { scientificName, commonName, density[], total? }. */
    series: RidgelineSeries[];
    width?: number;
    height?: number;
    /** i18n key for the chart's accessible label. */
    ariaLabelKey?: string;
    /** i18n key for the x-axis title. */
    axisLabelKey?: string;
    /** Maps a density index to its x-tick / peak label (e.g. hour -> "6:00"). Defaults to "{i}:00". */
    xTickFormat?: (_index: number) => string;
    /** Label every Nth x index (default 3, suitable for a 24-hour axis). */
    xTickStep?: number;
    /** Optional i18n key for a caption above the plot; receives { count } = number of ridges. */
    noteKey?: string;
    /** i18n key for the tooltip's detection-count label. */
    totalLabelKey?: string;
    /** i18n key for the tooltip's peak-position label. */
    peakLabelKey?: string;
    /** i18n key for the screen-reader summary; receives { count, species, time }. */
    summaryKey?: string;
  }

  let {
    series,
    width = 800,
    height = 400,
    ariaLabelKey = 'analytics.advanced.charts.ridgeline.ariaLabel',
    axisLabelKey = 'analytics.advanced.charts.ridgeline.axisTime',
    xTickFormat,
    xTickStep = 3,
    noteKey,
    totalLabelKey = 'analytics.advanced.charts.ridgeline.tooltipDetections',
    peakLabelKey = 'analytics.advanced.charts.ridgeline.tooltipPeak',
    summaryKey = 'analytics.advanced.charts.ridgeline.summary',
  }: Props = $props();

  // Layout / style constants.
  const MARGIN = { top: 24, right: 16, bottom: 44, left: 120 };
  // RIDGE_OVERLAP is imported from utils/ridgeline so the rendering and the layout math stay in sync.
  const RIDGE_FILL_OPACITY = 0.55; // translucent so overlapping ridges blend
  const RIDGE_STROKE_WIDTH = 1.5;
  const RIDGE_STROKE_WIDTH_HOVER = 2.5;
  const LABEL_GAP = 10; // px between the left edge and a row label
  // Labels are end-anchored at x = -LABEL_GAP, so they grow left into the margin; this is all the
  // room they have before the SVG viewport clips them.
  const LABEL_BUDGET_PX = MARGIN.left - LABEL_GAP;
  const X_AXIS_LABEL_OFFSET = 34;
  const MIN_DENSITY_DOMAIN = 1e-6; // floor so an all-zero set never yields a degenerate amp domain

  let tooltip: ChartTooltip | null = null;
  let chartContainer: HTMLDivElement | null = null;

  // Default x-axis tick formatter: station-local hour-of-day, zero-padded ("06:00"), via the shared
  // hour formatter so labels match the sibling time-of-day chart. This is the #1159 use; the
  // confidence-distribution reuse (#1162) passes its own xTickFormat for bin labels.
  const defaultHourFormat = createHourAxisFormatter();
  const tickFmt = $derived(xTickFormat ?? defaultHourFormat);

  // Localized rows. Computed in a $derived (not in the parent mapProps) so the draw $effect re-runs
  // when the per-visitor dictionary loads or the UI locale switches; the scientific name stays the
  // stable key, only the label changes.
  const rows = $derived(
    series.map(s => ({
      scientificName: s.scientificName,
      label: localizeSpeciesName(s.scientificName, s.commonName),
      density: s.density,
      total: s.total ?? 0,
    }))
  );

  // Screen-reader summary so the ridgeline is not opaque to assistive tech (spec section 7).
  const summary = $derived.by(() => {
    if (rows.length === 0) return '';
    const top = rows[0];
    const pk = peakIndex(top.density);
    return t(summaryKey, {
      count: rows.length,
      species: top.label,
      time: pk >= 0 ? tickFmt(pk) : '',
    });
  });

  let chartContext = $state<{
    svg: Selection<SVGSVGElement, unknown, null, undefined>;
    chartGroup: Selection<globalThis.SVGGElement, unknown, null, undefined>;
    innerWidth: number;
    innerHeight: number;
    theme: ChartTheme;
  } | null>(null);

  // Force a palette color to full opacity for the crisp ridge top line; the fill keeps the
  // translucent original so overlaps blend. d3-color robustly parses the rgba palette strings and
  // returns null for anything it can't (e.g. oklch theme tokens), in which case we keep the input.
  function opaqueStroke(color: string): string {
    const parsed = d3Color(color);
    if (!parsed) return color;
    parsed.opacity = 1;
    return parsed.formatRgb();
  }

  function showRowTooltip(event: MouseEvent, row: (typeof rows)[number], color: string): void {
    const pk = peakIndex(row.density);
    const items = [{ label: t(totalLabelKey), value: String(row.total), color }];
    if (pk >= 0) {
      items.push({ label: t(peakLabelKey), value: tickFmt(pk), color });
    }
    tooltip?.show({ title: row.label, items, x: event.clientX, y: event.clientY });
  }

  function drawChart(context: NonNullable<typeof chartContext>): void {
    const { chartGroup, innerWidth, innerHeight, theme } = context;
    // A redraw removes the hovered path, so its mouseleave would never fire; hide first.
    tooltip?.hide();
    chartGroup.selectAll('*').remove();
    if (innerWidth <= 0 || innerHeight <= 0 || rows.length === 0) return;

    // layout.rows[i] aligns with rows[i]: both derive from `series` in order (rows is a 1:1
    // localized map of series), so layoutRow.index indexes `rows` and `series` alike.
    const layout = ridgelineLayout(series, innerHeight, RIDGE_OVERLAP);

    // x: density index -> px. All series share the same length (24 hours / N bins); use the widest.
    const xLen = Math.max(1, ...rows.map(r => r.density.length));
    // nice: false - the bin domain is exact; rounding it outward would leave the last bin short of
    // the right edge and open a gap the ridges never reach.
    const xScale = createLinearScale({
      domain: [0, xLen - 1],
      range: [0, innerWidth],
      nice: false,
    });

    // Shared amplitude scale: global-max density -> layout.amplitude px above the baseline.
    const ampScale = scaleLinear()
      .domain([0, Math.max(MIN_DENSITY_DOMAIN, layout.maxDensity)])
      .range([0, layout.amplitude]);

    // X axis: sampled hour/bin ticks along the bottom.
    const xAxis = createAxis({
      scale: xScale as unknown as AxisScale<AxisDomain>,
      orientation: 'bottom',
      tickFormat: (d: AxisDomain) => tickFmt(Number(d)),
    });
    const step = Math.max(1, xTickStep);
    xAxis.tickValues(hourAxisTickValues(xLen - 1, step));
    const xAxisGroup = chartGroup
      .append('g')
      .attr('class', 'x-axis')
      .attr('transform', `translate(0,${innerHeight})`)
      .call(xAxis);
    styleAxis(xAxisGroup, theme.axis);

    addAxisLabel(
      chartGroup,
      {
        text: t(axisLabelKey),
        orientation: 'bottom',
        offset: X_AXIS_LABEL_OFFSET,
        width: innerWidth,
        height: innerHeight,
      },
      theme.axis
    );

    // Ridges, drawn top row first so lower (front) ridges overlap onto the ones above them.
    for (const layoutRow of layout.rows) {
      const row = rows[layoutRow.index];
      const color = getSpeciesColor(row.scientificName, theme);
      const baseline = layoutRow.baseline;

      const gen = d3Area<number>()
        .x((_d, j) => xScale(j))
        .y0(baseline)
        .y1(d => baseline - ampScale(d))
        .curve(curveBasis);

      const group = chartGroup
        .append('g')
        .attr('class', 'ridge')
        .attr('data-species', row.scientificName);

      group
        .append('path')
        .attr('class', 'ridge-area')
        .attr('d', gen(row.density) ?? '')
        .style('fill', color)
        .style('fill-opacity', RIDGE_FILL_OPACITY)
        .style('stroke', opaqueStroke(color))
        .style('stroke-width', RIDGE_STROKE_WIDTH)
        .on('mouseenter', function (event: MouseEvent) {
          select(this).style('stroke-width', RIDGE_STROKE_WIDTH_HOVER);
          showRowTooltip(event, row, color);
        })
        .on('mousemove', function (event: MouseEvent) {
          showRowTooltip(event, row, color);
        })
        .on('mouseleave', function () {
          select(this).style('stroke-width', RIDGE_STROKE_WIDTH);
          tooltip?.hide();
        });

      // Row label in the left margin, anchored at the baseline; fitted to the margin by measured
      // width, with the full name in a native <title> (appended after fitting, which sets the text).
      const labelText = group
        .append('text')
        .attr('class', 'ridge-label')
        .attr('x', -LABEL_GAP)
        .attr('y', baseline)
        .attr('text-anchor', 'end')
        .attr('dominant-baseline', 'middle')
        .style('fill', theme.text)
        .style('font-size', theme.axis.fontSize)
        .style('font-family', theme.axis.fontFamily);
      fitTextNode(labelText.node(), row.label, LABEL_BUDGET_PX);
      labelText.append('title').text(row.label);
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

  // Reference-count this chart so the shared species→color map clears when the
  // last patterns chart unmounts (fresh colors per page view; no session growth).
  onMount(() => registerChart());

  onDestroy(() => {
    tooltip?.destroy();
  });
</script>

<div class="species-ridgeline-chart" bind:this={chartContainer}>
  {#if noteKey && rows.length > 0}
    <p class="ridgeline-note">{t(noteKey, { count: rows.length })}</p>
  {/if}
  <BaseChart {width} {height} margin={MARGIN} responsive={true} ariaLabel={t(ariaLabelKey)}>
    {#snippet children(context)}
      {((chartContext = context), '')}
    {/snippet}
  </BaseChart>
  {#if summary}
    <p class="sr-only" data-testid="ridgeline-summary">{summary}</p>
  {/if}
</div>

<style>
  .species-ridgeline-chart {
    width: 100%;
    height: 100%;
    min-height: 400px;
  }

  .ridgeline-note {
    margin: 0 0 0.25rem;
    font-size: 0.75rem;
    color: var(--text-muted, rgba(0, 0, 0, 0.6));
  }

  :global(.ridge-area) {
    transition: stroke-width 0.12s ease;
  }
</style>
