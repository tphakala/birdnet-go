<!--
  Arrival/departure phenology (Tier-2, design spec / issue #1196).

  One horizontal residency bar per species (a Gantt): the bar spans the species' first in-range
  detection to its last, on a shared date x-axis, so you can read seasonal arrival/departure timing
  at a glance. Species are the top-N by detection volume; the backend sorts them by arrival (first
  seen) so the chart reads top-to-bottom in arrival order. The last-seen edge is rendered through the
  end of that calendar day (+1 day), which also gives a single-day species a visible, full-day bar.
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { select } from 'd3-selection';
  import type { AxisDomain, AxisScale } from 'd3-axis';

  import BaseChart from './BaseChart.svelte';
  import { parseLocalDateString } from '$lib/utils/date';
  import { localizeSpeciesName } from '$lib/utils/speciesDisplay';
  import { createTimeScale, createBandScale, createSpeciesColorScale } from './utils/scales';
  import {
    createAxis,
    styleAxis,
    addAxisLabel,
    createDateAxisFormatter,
    pickDateRangeBucket,
  } from './utils/axes';
  import { ChartTooltip, type TooltipData } from './utils/interactions';
  import { type ChartTheme } from './utils/theme';
  import { residencyDays, type PhenologyData, type PhenologyDatum } from './utils/phenology';
  import { t } from '$lib/i18n';

  interface Props {
    data: PhenologyData;
    width?: number;
    height?: number;
    ariaLabel?: string;
  }

  let { data, width = 800, height = 400, ariaLabel }: Props = $props();

  // Layout / behavior constants.
  // Left margin is wide for species labels; they are truncated past LABEL_MAX_CHARS (full name in the
  // tooltip), so the margin does not need to fit the longest name.
  const MARGIN = { top: 16, right: 24, bottom: 48, left: 140 };
  const MAX_X_TICKS = 8;
  const X_AXIS_LABEL_OFFSET = 38;
  const BAND_PADDING = 0.3;
  const BAR_RADIUS = 2;
  const BAR_IDLE_OPACITY = 0.85;
  // Even a single-day bar must be visible; on a wide (year) range one day is only a pixel or two.
  const MIN_BAR_WIDTH = 3;
  const LABEL_MAX_CHARS = 20;

  interface PlottedRow extends PhenologyDatum {
    firstObj: Date;
    // Exclusive end: the day after lastSeen, so the bar covers the whole last calendar day.
    endObj: Date;
  }

  let tooltip: ChartTooltip | null = null;
  let chartContainer: HTMLDivElement | null = null;
  // The bar a focus or tap tooltip is open for; every hide path clears it so a scroll never
  // reopens a tooltip that was dismissed. Bar data is looked up here because d3 handlers get it
  // per event, not on scroll.
  let anchoredBar: globalThis.Element | null = null;
  const barData = new WeakMap<globalThis.Element, PlottedRow>();
  // Set by Escape or a tap outside so leaving a hovered bar does not reopen the focused bar's
  // tooltip; the next focus or tap on a bar clears it.
  let focusTooltipDismissed = false;

  // Add one calendar day in local time (DST-safe: setDate respects the timezone, unlike +86_400_000ms
  // which drifts by an hour across a DST boundary and misaligns the SVG rect).
  function nextDay(d: Date): Date {
    const out = new Date(d);
    out.setDate(out.getDate() + 1);
    return out;
  }

  // Truncate a long species label so it fits the left margin; the full name stays in the tooltip.
  function truncateLabel(name: string): string {
    return name.length > LABEL_MAX_CHARS ? `${name.slice(0, LABEL_MAX_CHARS - 1)}…` : name;
  }

  // Rows with valid parsed dates, preserving the server's arrival order. The name is localized here,
  // in a $derived, so the chart redraws when the per-visitor dictionary loads or the UI locale
  // switches (same chain as the sibling charts: visitor dictionary, payload name, scientific name).
  const parsedRows = $derived.by<PlottedRow[]>(() => {
    const out: PlottedRow[] = [];
    for (const r of data.rows) {
      const first = parseLocalDateString(r.firstSeen);
      const last = parseLocalDateString(r.lastSeen);
      if (!first || !last || isNaN(first.getTime()) || isNaN(last.getTime())) continue;
      out.push({
        ...r,
        commonName: localizeSpeciesName(r.scientificName, r.commonName),
        firstObj: first,
        endObj: nextDay(last),
      });
    }
    return out;
  });

  // Screen-reader summary so the chart is not opaque to assistive tech (spec section 7).
  const summary = $derived.by(() => {
    if (parsedRows.length === 0) return '';
    return t('analytics.advanced.charts.phenology.summary', {
      species: parsedRows.length,
    });
  });

  // The tooltip content for a bar: the same four label/value pairs for hover, focus and tap.
  function barTooltipItems(d: PlottedRow): TooltipData['items'] {
    return [
      { label: t('analytics.advanced.charts.phenology.tooltipFirst'), value: d.firstSeen },
      { label: t('analytics.advanced.charts.phenology.tooltipLast'), value: d.lastSeen },
      {
        label: t('analytics.advanced.charts.phenology.tooltipResidency'),
        value: t('analytics.advanced.charts.phenology.residencyDays', {
          days: residencyDays(d.firstSeen, d.lastSeen),
        }),
      },
      { label: t('analytics.advanced.charts.phenology.tooltipCount'), value: String(d.count) },
    ];
  }

  // Accessible name for a focused bar, composed from the tooltip's own label and value strings.
  function barAriaLabel(d: PlottedRow): string {
    const [first, last, residency, count] = barTooltipItems(d);
    return `${d.commonName}: ${first.label} ${first.value}, ${last.label} ${last.value}, ${residency.label} ${residency.value}, ${count.label} ${count.value}`;
  }

  function showBarTooltip(d: PlottedRow, x: number, y: number): void {
    tooltip?.show({ title: d.commonName, items: barTooltipItems(d), x, y });
  }

  // Focus and tap have no pointer position, so anchor the tooltip to the bar's right edge.
  function showBarTooltipAtBar(el: globalThis.Element, d: PlottedRow): void {
    const r = el.getBoundingClientRect();
    anchoredBar = el;
    showBarTooltip(d, r.right, r.top + r.height / 2);
  }

  function hideTooltip(): void {
    anchoredBar = null;
    tooltip?.hide();
  }

  function restoreBarOpacity(): void {
    if (chartContainer) {
      select(chartContainer).selectAll('.phenology-bars rect').style('opacity', BAR_IDLE_OPACITY);
    }
  }

  function dismissTooltip(): void {
    hideTooltip();
    restoreBarOpacity();
  }

  // The tooltip is fixed to the viewport. A focus that scrolls its bar into view fires a scroll
  // event right after the tooltip opened, so a bar-anchored tooltip follows its bar while the bar
  // is on screen; any other tooltip, or one whose bar left the viewport, is dismissed.
  function onWindowScroll(): void {
    const el = anchoredBar;
    const d = el ? barData.get(el) : undefined;
    if (el && d && el.isConnected) {
      const r = el.getBoundingClientRect();
      if (
        r.bottom >= 0 &&
        r.top <= window.innerHeight &&
        r.right >= 0 &&
        r.left <= window.innerWidth
      ) {
        showBarTooltipAtBar(el, d);
        return;
      }
    }
    dismissTooltip();
  }

  // A tap elsewhere may not blur an SVG element on every touch browser, so hide on any pointerdown
  // outside the bars.
  function onDocumentPointerDown(event: Event): void {
    const target = event.target;
    if (
      target instanceof globalThis.Element &&
      target.closest('.phenology-bars') &&
      chartContainer?.contains(target)
    ) {
      return;
    }
    focusTooltipDismissed = true;
    dismissTooltip();
  }

  let chartContext = $state<{
    svg: import('d3-selection').Selection<SVGSVGElement, unknown, null, undefined>;
    chartGroup: import('d3-selection').Selection<globalThis.SVGGElement, unknown, null, undefined>;
    innerWidth: number;
    innerHeight: number;
    theme: ChartTheme;
  } | null>(null);

  function drawChart(context: NonNullable<typeof chartContext>): void {
    const { chartGroup, innerWidth, innerHeight, theme } = context;
    // A redraw removes the hovered bar, so its mouseleave would never fire; hide first.
    hideTooltip();
    chartGroup.selectAll('*').remove();
    if (innerWidth <= 0 || innerHeight <= 0) return;

    const rows = parsedRows;
    // ChartCard owns the empty / not-enough-data state; bail before drawing axes on an empty set.
    if (rows.length === 0) return;

    // X domain spans the earliest first-seen to the latest last-seen (rendered +1 day, inclusive of
    // that calendar day). createTimeScale pads a collapsed (single-date) domain, so the scale is never
    // zero-width even if every species was seen on one day.
    let minDate = rows[0].firstObj;
    let maxDate = rows[0].endObj;
    for (const r of rows) {
      if (r.firstObj < minDate) minDate = r.firstObj;
      if (r.endObj > maxDate) maxDate = r.endObj;
    }
    const xScale = createTimeScale({ domain: [minDate, maxDate], range: [0, innerWidth] });

    // Y band: one row per species, keyed by scientific name (stable, unique), in arrival order.
    const scientificNames = rows.map(r => r.scientificName);
    const yScale = createBandScale({
      domain: scientificNames,
      range: [0, innerHeight],
      padding: BAND_PADDING,
    });
    const bandwidth = yScale.bandwidth();
    const colorScale = createSpeciesColorScale(scientificNames);
    const commonByScientific = new Map(rows.map(r => [r.scientificName, r.commonName]));

    // X axis: locale-aware date labels, bucketed by the span (week / month / year).
    const dateFmt = createDateAxisFormatter(pickDateRangeBucket([minDate, maxDate]));
    const xAxis = createAxis({
      scale: xScale as unknown as AxisScale<AxisDomain>,
      orientation: 'bottom',
      tickCount: MAX_X_TICKS,
      tickFormat: (d: AxisDomain) => dateFmt(d as Date),
    });
    const xAxisGroup = chartGroup
      .append('g')
      .attr('class', 'x-axis')
      .attr('transform', `translate(0,${innerHeight})`)
      .call(xAxis);
    styleAxis(xAxisGroup, theme.axis);

    // Y axis: species labels (d3-axis centers band ticks). Truncate long names; tooltip has the full.
    const yAxis = createAxis({
      scale: yScale as unknown as AxisScale<AxisDomain>,
      orientation: 'left',
      tickFormat: (d: AxisDomain) => truncateLabel(commonByScientific.get(String(d)) ?? String(d)),
    });
    const yAxisGroup = chartGroup.append('g').attr('class', 'y-axis').call(yAxis);
    styleAxis(yAxisGroup, theme.axis);
    // The tick label is truncated past LABEL_MAX_CHARS. Attach the full common name as a native
    // <title> on each tick so the complete name is recoverable via touch long-press and assistive tech.
    yAxisGroup
      .selectAll<globalThis.SVGGElement, string>('.tick')
      .append('title')
      .text(d => commonByScientific.get(String(d)) ?? String(d));

    addAxisLabel(
      chartGroup,
      {
        text: t('analytics.advanced.charts.phenology.axisDate'),
        orientation: 'bottom',
        offset: X_AXIS_LABEL_OFFSET,
        width: innerWidth,
        height: innerHeight,
      },
      theme.axis
    );

    // Residency bars: one rect per species from first-seen to last-seen (+1 day), min width so a
    // single-day species stays visible.
    chartGroup
      .append('g')
      .attr('class', 'phenology-bars')
      .selectAll('rect')
      .data(rows)
      .enter()
      .append('rect')
      .attr('x', (d: PlottedRow) => xScale(d.firstObj))
      .attr('y', (d: PlottedRow) => yScale(d.scientificName) ?? 0)
      .attr('width', (d: PlottedRow) =>
        Math.max(MIN_BAR_WIDTH, xScale(d.endObj) - xScale(d.firstObj))
      )
      .attr('height', bandwidth)
      .attr('rx', BAR_RADIUS)
      .style('fill', (d: PlottedRow) => colorScale(d.scientificName))
      .style('opacity', BAR_IDLE_OPACITY)
      .attr('tabindex', 0)
      .attr('role', 'img')
      .attr('aria-label', (d: PlottedRow) => barAriaLabel(d))
      .each(function (d: PlottedRow) {
        barData.set(this, d);
      })
      .on('mouseenter', function (event: MouseEvent, d: PlottedRow) {
        select(this).style('opacity', 1);
        anchoredBar = null;
        showBarTooltip(d, event.clientX, event.clientY);
      })
      .on('mouseleave', function () {
        select(this).style('opacity', BAR_IDLE_OPACITY);
        // Hover is over, but a bar that still has focus keeps its focus tooltip.
        const focused = document.activeElement;
        const focusedData = focused ? barData.get(focused) : undefined;
        if (focused && focusedData && !focusTooltipDismissed) {
          select(focused).style('opacity', 1);
          showBarTooltipAtBar(focused, focusedData);
        } else {
          hideTooltip();
        }
      })
      .on('focus', function (_event: FocusEvent, d: PlottedRow) {
        focusTooltipDismissed = false;
        select(this).style('opacity', 1);
        showBarTooltipAtBar(this, d);
      })
      .on('blur', function () {
        select(this).style('opacity', BAR_IDLE_OPACITY);
        hideTooltip();
      })
      .on('click', function (_event: MouseEvent, d: PlottedRow) {
        focusTooltipDismissed = false;
        // A tap on touch browsers may not focus an SVG element by itself.
        this.focus();
        select(this).style('opacity', 1);
        showBarTooltipAtBar(this, d);
      })
      .on('keydown', function (event: KeyboardEvent) {
        if (event.key === 'Escape') {
          focusTooltipDismissed = true;
          dismissTooltip();
        }
      });
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
    document.addEventListener('pointerdown', onDocumentPointerDown, {
      capture: true,
      passive: true,
    });
    window.addEventListener('scroll', onWindowScroll, { capture: true, passive: true });
  });

  onDestroy(() => {
    document.removeEventListener('pointerdown', onDocumentPointerDown, { capture: true });
    window.removeEventListener('scroll', onWindowScroll, { capture: true });
    tooltip?.destroy();
  });
</script>

<div class="phenology-chart" bind:this={chartContainer}>
  <BaseChart
    {width}
    {height}
    margin={MARGIN}
    responsive={true}
    containerRole="group"
    ariaLabel={ariaLabel ?? t('analytics.advanced.charts.phenology.ariaLabel')}
  >
    {#snippet children(context)}
      {((chartContext = context), '')}
    {/snippet}
  </BaseChart>
  {#if summary}
    <p class="sr-only" data-testid="phenology-summary">{summary}</p>
  {/if}
</div>

<style>
  .phenology-chart {
    width: 100%;
    height: 100%;
    min-height: 320px;
  }

  :global(.phenology-bars rect) {
    transition: opacity 0.12s ease;
  }

  /* Stroke rather than outline: outline support on SVG elements is uneven. The text colour
     contrasts with every bar fill in both themes; the primary colour vanishes on blue bars. */
  :global(.phenology-bars rect:focus-visible) {
    outline: none;
    stroke: var(--color-base-content);
    stroke-width: 2px;
  }
</style>
