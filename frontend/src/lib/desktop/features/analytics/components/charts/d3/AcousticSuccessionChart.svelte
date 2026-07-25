<!--
  Acoustic succession streamgraph: one stacked band per species showing its raw hour-of-day
  detection counts (24 buckets), laid out with d3.stackOffsetWiggle + curveBasis so the band width
  is detection volume and the bands show the diel acoustic handover (dawn-chorus species -> daytime
  -> dusk -> night). Top-N species by volume only (the server ranks; the note says so), so the
  stream's thickness is the combined activity of those top species, not the whole soundscape.

  There is no y-axis (the wiggle baseline is meaningless). Bands are identified by an inline label at
  their thickest hour (when it fits) plus a hover tooltip; the scientific name is the stable D3 stack
  key and the localized common name is the label (re-localized here so it tracks the visitor locale,
  matching the sibling charts). Species color comes from utils/speciesColor.ts getSpeciesColor, a
  shared page-scoped map so a species keeps the same color across the sibling charts.
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { select, type Selection } from 'd3-selection';
  import {
    stack as d3Stack,
    stackOffsetWiggle,
    stackOrderInsideOut,
    area as d3Area,
    curveBasis,
    type SeriesPoint,
  } from 'd3-shape';
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
  import { getSpeciesColor, registerChart } from './utils/speciesColor';
  import { SUCCESSION_HOURS, readableTextColor, type SuccessionSeries } from './utils/succession';
  // peakIndex is the shared argmax for per-species distribution charts (same helper the ridgeline
  // uses); reused here rather than duplicated.
  import { peakIndex } from './utils/ridgeline';

  interface Props {
    /** One row per species: { scientificName, commonName, counts[24], total }. */
    series: SuccessionSeries[];
    width?: number;
    height?: number;
    /** i18n key for the chart's accessible label. */
    ariaLabelKey?: string;
    /** i18n key for the x-axis title. */
    axisLabelKey?: string;
    /** Optional i18n key for a caption above the plot; receives { count } = number of bands. */
    noteKey?: string;
    /** i18n key for the tooltip's detection-count label. */
    totalLabelKey?: string;
    /** i18n key for the tooltip's peak-hour label. */
    peakLabelKey?: string;
    /** i18n key for the screen-reader summary; receives { count, species, time }. */
    summaryKey?: string;
  }

  let {
    series,
    width = 800,
    height = 400,
    ariaLabelKey = 'analytics.advanced.charts.succession.ariaLabel',
    axisLabelKey = 'analytics.advanced.charts.succession.axisTime',
    noteKey,
    totalLabelKey = 'analytics.advanced.charts.succession.tooltipDetections',
    peakLabelKey = 'analytics.advanced.charts.succession.tooltipPeak',
    summaryKey = 'analytics.advanced.charts.succession.summary',
  }: Props = $props();

  // Layout / style constants.
  const MARGIN = { top: 12, right: 16, bottom: 40, left: 16 };
  const BAND_FILL_OPACITY = 0.85; // adjacent (not overlapping) bands read as solid
  const BAND_STROKE_WIDTH = 0.75;
  const BAND_STROKE_WIDTH_HOVER = 2;
  const MIN_LABEL_HEIGHT = 13; // px; below this a band is too thin to fit an inline label
  const LABEL_MAX_CHARS = 16;
  const X_TICK_STEP = 3; // label every 3rd hour on the 24-hour axis
  const X_AXIS_LABEL_OFFSET = 34;
  const MIN_Y_SPAN = 1e-9; // floor below which the stacked domain is collapsed (all-zero)

  const hourFmt = createHourAxisFormatter();

  let tooltip: ChartTooltip | null = null;
  let chartContainer: HTMLDivElement | null = null;

  // Localized rows. Computed in a $derived (not the parent mapProps) so the draw $effect re-runs when
  // the per-visitor dictionary loads or the UI locale switches; the scientific name stays the stable
  // stack key, only the label changes.
  const rows = $derived(
    series.map(s => ({
      scientificName: s.scientificName,
      label: localizeSpeciesName(s.scientificName, s.commonName),
      counts: s.counts,
      total: s.total ?? s.counts.reduce((sum, c) => sum + (Number.isFinite(c) ? c : 0), 0),
    }))
  );

  // Screen-reader summary so the streamgraph is not opaque to assistive tech (spec section 7).
  const summary = $derived.by(() => {
    if (rows.length === 0) return '';
    const top = rows[0];
    const pk = peakIndex(top.counts);
    return t(summaryKey, {
      count: rows.length,
      species: top.label,
      time: pk >= 0 ? hourFmt(pk) : '',
    });
  });

  let chartContext = $state<{
    svg: Selection<globalThis.SVGSVGElement, unknown, null, undefined>;
    chartGroup: Selection<globalThis.SVGGElement, unknown, null, undefined>;
    innerWidth: number;
    innerHeight: number;
    theme: ChartTheme;
  } | null>(null);

  // Persistent legend: a real-DOM swatch + localized name per band. A streamgraph has no species
  // y-axis, and the hover tooltip plus the thick-band inline labels do not identify a band on touch
  // or by keyboard (this UI is tablet/desktop), so a thin band would otherwise be an unlabeled color.
  // Colors come from the same shared getSpeciesColor map the bands use (keyed on scientific name), so
  // a swatch matches its band exactly. Depends on the BaseChart theme, so it is empty until the chart
  // mounts.
  const legendItems = $derived.by(() => {
    const theme = chartContext?.theme;
    if (!theme || rows.length === 0) return [];
    return rows.map(r => ({
      scientificName: r.scientificName,
      label: r.label,
      color: getSpeciesColor(r.scientificName, theme),
    }));
  });

  function truncateLabel(label: string): string {
    return label.length > LABEL_MAX_CHARS ? `${label.slice(0, LABEL_MAX_CHARS - 1)}…` : label;
  }

  // Force a palette color to full opacity for the crisp band edge; the fill keeps the translucent
  // original. d3-color returns null for anything it can't parse (e.g. oklch tokens), so keep input.
  function opaqueStroke(fill: string): string {
    const parsed = d3Color(fill);
    if (!parsed) return fill;
    parsed.opacity = 1;
    return parsed.formatRgb();
  }

  function showBandTooltip(event: MouseEvent, row: (typeof rows)[number], fill: string): void {
    const pk = peakIndex(row.counts);
    const items = [{ label: t(totalLabelKey), value: String(row.total), color: fill }];
    if (pk >= 0) {
      items.push({ label: t(peakLabelKey), value: hourFmt(pk), color: fill });
    }
    tooltip?.show({ title: row.label, items, x: event.clientX, y: event.clientY });
  }

  function drawChart(context: NonNullable<typeof chartContext>): void {
    const { chartGroup, innerWidth, innerHeight, theme } = context;
    // A redraw removes the hovered path, so its mouseleave would never fire; hide first.
    tooltip?.hide();
    chartGroup.selectAll('*').remove();
    if (innerWidth <= 0 || innerHeight <= 0 || rows.length === 0) return;

    const names = rows.map(r => r.scientificName);
    const colorByName = new Map(names.map(n => [n, getSpeciesColor(n, theme)]));
    const rowByName = new Map(rows.map(r => [r.scientificName, r]));

    // d3.stack over the 24 hour indices; value(hourIdx, name) reads the species' count at that hour
    // via a Map lookup and Array.at (no variable bracket indexing, so no detect-object-injection lint).
    const hourIndices = Array.from({ length: SUCCESSION_HOURS }, (_, h) => h);
    const stackGen = d3Stack<number, string>()
      .keys(names)
      .value((hourIdx, name) => {
        const c = rowByName.get(name)?.counts.at(hourIdx) ?? 0;
        return Number.isFinite(c) ? Math.max(0, c) : 0;
      })
      .offset(stackOffsetWiggle)
      .order(stackOrderInsideOut);
    const layers = stackGen(hourIndices);

    // y domain spans the wiggle baseline's full extent. A collapsed (all-zero) domain has nothing to
    // draw and would make the y scale degenerate, so bail out (the card shows its empty state).
    let yMin = Infinity;
    let yMax = -Infinity;
    for (const layer of layers) {
      for (const p of layer) {
        if (p[0] < yMin) yMin = p[0];
        if (p[1] > yMax) yMax = p[1];
      }
    }
    if (!Number.isFinite(yMin) || !Number.isFinite(yMax) || yMax - yMin < MIN_Y_SPAN) return;

    // nice: false - the hour domain is exact; rounding it outward would leave the final hour short
    // of the right edge and open a gap the bands never reach.
    const xScale = createLinearScale({
      domain: [0, SUCCESSION_HOURS - 1],
      range: [0, innerWidth],
      nice: false,
    });
    const yScale = scaleLinear().domain([yMin, yMax]).range([innerHeight, 0]);

    // X axis: sampled hour ticks along the bottom. No y-axis (the wiggle baseline is meaningless).
    const xAxis = createAxis({
      scale: xScale as unknown as AxisScale<AxisDomain>,
      orientation: 'bottom',
      tickFormat: (d: AxisDomain) => hourFmt(Number(d)),
    });
    xAxis.tickValues(hourAxisTickValues(SUCCESSION_HOURS - 1, X_TICK_STEP));
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

    const gen = d3Area<SeriesPoint<number>>()
      .x((_d, i) => xScale(i))
      .y0(d => yScale(d[0]))
      .y1(d => yScale(d[1]))
      .curve(curveBasis);

    for (const layer of layers) {
      const name = layer.key;
      const row = rowByName.get(name);
      if (!row) continue;
      const fill = colorByName.get(name) ?? theme.primary;

      const group = chartGroup.append('g').attr('class', 'stream-band').attr('data-species', name);

      group
        .append('path')
        .attr('class', 'stream-area')
        .attr('d', gen(layer) ?? '')
        .style('fill', fill)
        .style('fill-opacity', BAND_FILL_OPACITY)
        .style('stroke', opaqueStroke(fill))
        .style('stroke-width', BAND_STROKE_WIDTH)
        .on('mouseenter', function (event: MouseEvent) {
          select(this).style('stroke-width', BAND_STROKE_WIDTH_HOVER);
          showBandTooltip(event, row, fill);
        })
        .on('mousemove', function (event: MouseEvent) {
          showBandTooltip(event, row, fill);
        })
        .on('mouseleave', function () {
          select(this).style('stroke-width', BAND_STROKE_WIDTH);
          tooltip?.hide();
        });

      // Inline label at the band's thickest hour, when the band is tall enough to fit text there.
      // Iterate with entries() and keep the winning point so the band is read by value (no variable
      // bracket indexing into the layer, which the object-injection lint flags).
      let peakIdx = 0;
      let peakPoint = layer[0];
      let maxThickness = -Infinity;
      for (const [i, point] of layer.entries()) {
        const thickness = point[1] - point[0];
        if (thickness > maxThickness) {
          maxThickness = thickness;
          peakIdx = i;
          peakPoint = point;
        }
      }
      const thicknessPx = yScale(peakPoint[0]) - yScale(peakPoint[1]);
      if (thicknessPx >= MIN_LABEL_HEIGHT) {
        const midY = (yScale(peakPoint[0]) + yScale(peakPoint[1])) / 2;
        // Anchor toward the inside at the edges so the label does not clip off the canvas.
        const anchor = peakIdx <= 1 ? 'start' : peakIdx >= SUCCESSION_HOURS - 2 ? 'end' : 'middle';
        const textColor = readableTextColor(fill);
        const halo = textColor === '#111827' ? '#f9fafb' : '#111827';
        const labelText = group
          .append('text')
          .attr('class', 'stream-label')
          .attr('x', xScale(peakIdx))
          .attr('y', midY)
          .attr('text-anchor', anchor)
          .attr('dominant-baseline', 'middle')
          .style('font-size', theme.axis.fontSize)
          .style('font-family', theme.axis.fontFamily)
          .style('fill', textColor)
          .style('stroke', halo)
          .style('stroke-width', '3px')
          .style('paint-order', 'stroke')
          .style('stroke-linejoin', 'round')
          .style('pointer-events', 'none')
          .text(truncateLabel(row.label));
        labelText.append('title').text(row.label);
      }
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

<div class="acoustic-succession-chart" bind:this={chartContainer}>
  {#if noteKey && rows.length > 0}
    <p class="succession-note">{t(noteKey, { count: rows.length })}</p>
  {/if}
  <div class="succession-plot">
    <BaseChart {width} {height} margin={MARGIN} responsive={true} ariaLabel={t(ariaLabelKey)}>
      {#snippet children(context)}
        {((chartContext = context), '')}
      {/snippet}
    </BaseChart>
  </div>
  {#if legendItems.length > 0}
    <ul class="succession-legend">
      {#each legendItems as item (item.scientificName)}
        <li class="succession-legend-item">
          <span
            class="succession-legend-swatch"
            style:background-color={item.color}
            aria-hidden="true"
          ></span>
          <span class="succession-legend-label">{item.label}</span>
        </li>
      {/each}
    </ul>
  {/if}
  {#if summary}
    <p class="sr-only" data-testid="succession-summary">{summary}</p>
  {/if}
</div>

<style>
  .acoustic-succession-chart {
    display: flex;
    flex-direction: column;
    width: 100%;
    height: 100%;
    min-height: 280px;
  }

  .succession-note {
    flex: none;
    margin: 0 0 0.25rem;
    font-size: 0.75rem;
    color: var(--text-muted, rgba(0, 0, 0, 0.6));
  }

  /* The plot fills the space left after the note and legend, so all three fit the card's fixed
     height without overflow. */
  .succession-plot {
    flex: 1 1 0;
    min-height: 0;
  }

  .succession-legend {
    flex: none;
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem 0.75rem;
    margin: 0.25rem 0 0;
    padding: 0;
    list-style: none;
  }

  .succession-legend-item {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    font-size: 0.75rem;
    color: var(--text-muted, rgba(0, 0, 0, 0.7));
  }

  .succession-legend-swatch {
    flex: none;
    width: 0.7rem;
    height: 0.7rem;
    border-radius: 2px;
  }

  :global(.stream-area) {
    transition: stroke-width 0.12s ease;
  }
</style>
