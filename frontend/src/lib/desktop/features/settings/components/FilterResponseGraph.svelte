<!--
  Filter Response Graph Component

  Purpose: Plots the frequency response of an equalizer configuration as the
  backend computes it, so the preview matches the filter chain audio uses.

  Features:
  - Curve points come from POST /api/v2/system/audio/equalizer/response
    (debounced; a newer edit always wins over an older response)
  - Modified logarithmic frequency scale optimized for high frequencies (20Hz - 20kHz)
  - Interactive tooltip showing the nearest computed point
  - Flat 0dB curve and label when the equalizer is disabled, empty, or every
    filter is skipped or set to 0 passes
  - Loading, updating, error (with Retry) and skipped-filter states, each with
    visible text; loading, updating and skipped are announced through a polite
    status region, errors through an alert region (both always rendered)
  - Caption with the sample rate the curve was calculated for

  Props:
  - equalizer: Equalizer configuration ({ enabled, filters }) to preview
  - sampleRate: Sample rate to calculate at (optional; omit to use the
    server's configured rate)
  - height: Canvas height (optional, defaults to 400px)

  Width is auto-calculated based on container size for responsive behavior.

  @component
-->
<script module lang="ts">
  // Gain range mapped onto the plot height. The bottom is fixed at MIN_DB. The
  // top starts at BASE_MAX_DB and rises in DB_GRID_STEP steps to hold the drawn
  // peak, up to MAX_TOP_DB; a higher peak is clamped to the top edge.
  /** Gain at the bottom edge of the plot, in dB. */
  export const MIN_DB = -96;
  /** Gain at the top edge of the plot when no drawn point is higher, in dB. */
  export const BASE_MAX_DB = 12;
  /** Highest gain the top edge of the plot can reach, in dB. A display choice: a taller axis flattens the rest of the curve. */
  export const MAX_TOP_DB = 48;
  /** Distance between horizontal grid lines and labels, and the steps of the top edge, in dB. */
  export const DB_GRID_STEP = 12;
  /** Peak gains are rounded to 1 / this many dB, so float noise does not move the axis. */
  const PEAK_STEPS_PER_DB = 10;

  /** The y axis chosen for one response. */
  export interface GainAxis {
    /** Gain at the top edge of the plot, in dB. */
    maxDb: number;
    /** Highest gain among the drawn points, rounded to 0.1 dB; undefined without drawn points. */
    peakDb: number | undefined;
    /** True when the peak is above the highest axis the plot can reach. */
    clipped: boolean;
  }

  /** Chooses the y axis for the points inside the drawn frequency range. */
  export function gainAxis(
    points: ReadonlyArray<{ hz: number; gainDb: number }>,
    minHz: number,
    maxHz: number
  ): GainAxis {
    let peak: number | undefined;
    for (const point of points) {
      if (point.hz < minHz || point.hz > maxHz) continue;
      if (peak === undefined || point.gainDb > peak) peak = point.gainDb;
    }
    if (peak === undefined) return { maxDb: BASE_MAX_DB, peakDb: undefined, clipped: false };
    const peakDb = Math.round(peak * PEAK_STEPS_PER_DB) / PEAK_STEPS_PER_DB;
    const stepped = Math.ceil(peakDb / DB_GRID_STEP) * DB_GRID_STEP;
    return {
      maxDb: Math.min(MAX_TOP_DB, Math.max(BASE_MAX_DB, stepped)),
      peakDb,
      clipped: peakDb > MAX_TOP_DB,
    };
  }

  /** Gains of the horizontal grid lines, from the plot bottom up to maxDb. */
  export function gainGridLines(maxDb: number): number[] {
    const lines: number[] = [];
    for (let db = MIN_DB; db <= maxDb; db += DB_GRID_STEP) lines.push(db);
    return lines;
  }
</script>

<script lang="ts">
  /* global ResizeObserver */
  import { onMount } from 'svelte';
  import { t } from '$lib/i18n';
  import Button from '$lib/desktop/components/ui/Button.svelte';
  import ErrorAlert from '$lib/desktop/components/ui/ErrorAlert.svelte';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';
  import {
    buildEqualizerResponseRequest,
    createEqualizerResponseLoader,
    findNearestPoint,
    type EqualizerResponseFilterInput,
    type EqualizerResponseReady,
    type EqualizerResponseState,
    type EqualizerSkippedFilter,
  } from '$lib/api/equalizer';

  interface Props {
    /** Equalizer configuration to preview. */
    equalizer: { enabled: boolean; filters: EqualizerResponseFilterInput[] };
    /** Sample rate the preview is calculated at; omit to use the server's configured rate. */
    sampleRate?: number;
    height?: number;
  }

  // Make responsive to container width, default to reasonable size
  let { equalizer, sampleRate, height = 400 }: Props = $props();

  /** Font of the frequency and gain tick labels. */
  const AXIS_LABEL_FONT = '11px system-ui, -apple-system, sans-serif';
  /** Font of the axis titles. */
  const AXIS_TITLE_FONT = '12px system-ui, -apple-system, sans-serif';
  /** Font of the label drawn over a flat curve. */
  const FLAT_LABEL_FONT = '13px system-ui, -apple-system, sans-serif';

  let graphState = $state<EqualizerResponseState>({ status: 'loading' });
  let tooltip = $state({ visible: false, x: 0, y: 0, freq: 0, gain: 0 });

  const loader = createEqualizerResponseLoader(next => {
    graphState = next;
    // A stationary cursor would otherwise keep showing a stale gain.
    tooltip.visible = false;
  });

  const requestBody = $derived(buildEqualizerResponseRequest(equalizer, sampleRate));

  $effect(() => {
    loader.request(requestBody);
  });

  /** The response to draw: the current one, or the previous one while updating. */
  const drawable = $derived<EqualizerResponseReady | undefined>(
    graphState.status === 'ready'
      ? { request: graphState.request, response: graphState.response }
      : graphState.status === 'loading'
        ? graphState.previous
        : undefined
  );

  // Frequency range - standard audio range up to 20kHz (human hearing limit)
  const MIN_FREQ = 20;
  const MAX_FREQ = 20000;

  /** The y axis for the response being drawn; it follows the drawn peak. */
  const axis = $derived(gainAxis(drawable?.response.points ?? [], MIN_FREQ, MAX_FREQ));
  const gridLines = $derived(gainGridLines(axis.maxDb));
  const clippedText = $derived(
    drawable && axis.clipped && axis.peakDb !== undefined
      ? t('settings.audio.audioFilters.graph.clipped', { peak: axis.peakDb })
      : ''
  );

  /**
   * Disabled, empty, all-skipped and all-bypassed (passes 0) configurations give
   * a flat 0 dB curve: no filter is both accepted by the backend and switched on.
   */
  const isFlat = $derived.by(() => {
    if (drawable === undefined) return false;
    if (!drawable.response.enabled) return true;
    const skippedIndexes = new Set(drawable.response.skipped.map(entry => entry.index));
    return !drawable.request.equalizer.filters.some(
      (filter, index) => !skippedIndexes.has(index) && (filter.passes ?? 0) !== 0
    );
  });

  // Live regions are rendered empty and get text only after mount, in a later
  // task: a region inserted together with its text is often not announced.
  let announce = $state(false);

  const alertText = $derived(
    announce && graphState.status === 'error' ? t('settings.audio.audioFilters.graph.error') : ''
  );

  const statusText = $derived.by(() => {
    if (!announce) return '';
    switch (graphState.status) {
      case 'loading':
        return graphState.previous
          ? t('settings.audio.audioFilters.graph.updating')
          : t('settings.audio.audioFilters.graph.loading');
      case 'ready': {
        const parts: string[] = [];
        if (graphState.response.skipped.length > 0) {
          parts.push(
            t('settings.audio.audioFilters.graph.skippedHeading', {
              count: graphState.response.skipped.length,
            })
          );
        }
        if (clippedText) parts.push(clippedText);
        return parts.join(' ');
      }
      case 'error':
        return '';
    }
  });

  /** Translated line for one skipped filter, named from the request it was computed for. */
  function skippedMessage(entry: EqualizerSkippedFilter, ready: EqualizerResponseReady): string {
    const filter = ready.request.equalizer.filters.at(entry.index);
    const params = {
      number: entry.index + 1,
      type: filter?.type ?? entry.type,
      frequency: filter?.frequency ?? 0,
      rate: ready.response.sampleRate,
    };
    switch (entry.reason) {
      case 'unknown_type':
        return t('settings.audio.audioFilters.graph.skippedReasons.unknown_type', params);
      case 'invalid_parameters':
        return t('settings.audio.audioFilters.graph.skippedReasons.invalid_parameters', params);
      case 'add_failed':
        return t('settings.audio.audioFilters.graph.skippedReasons.add_failed', params);
      case 'unknown':
        return t('settings.audio.audioFilters.graph.skippedReasons.unknown', params);
    }
  }

  // Container element for measuring width
  let containerElement: HTMLDivElement;

  // Canvas element reference
  let canvas: HTMLCanvasElement;

  // Wrapper that takes focus when Retry removes the error alert, so focus
  // does not fall back to the page body.
  let graphRegion: HTMLDivElement | undefined = $state();

  function handleRetry() {
    loader.retry();
    graphRegion?.focus();
  }

  // Professional margins for proper label spacing
  const margins = {
    top: 40,
    right: 60,
    bottom: 100, // More space for frequency labels and axis title
    left: 100, // Much more space for dB labels
  };

  // Responsive width calculation
  let canvasWidth = $state(800); // Default fallback
  const canvasHeight = $derived(height);

  // Plot area dimensions (excluding margins) - now reactive
  let plotWidth = $derived(canvasWidth - margins.left - margins.right);
  let plotHeight = $derived(canvasHeight - margins.top - margins.bottom);

  // Grid lines optimized for audio engineering work - more detail in bird frequency range
  const freqGridLines = [
    20, 50, 100, 200, 500, 1000, 2000, 3000, 4000, 5000, 6000, 8000, 10000, 12000, 15000, 20000,
  ];

  // Color scheme - reactive state for proper Svelte 5 updates
  let colors = $state({
    background: 'hsl(222, 30%, 15%)', //
    grid: 'hsl(215, 16%, 35%)', // Light blue-grey grid lines
    text: 'hsl(218, 11%, 87%)', // White/light text
    reference: 'hsl(0, 0%, 55%)', // Light reference line
    primary: 'hsl(204, 70%, 63%)', // Bright blue frequency curve
  });

  // Initialize colors - no need to update since we use same colors for both themes
  function updateColors() {
    // Colors are already set in $state above and don't change based on theme
    // This function kept for compatibility with existing effect calls
  }

  // Convert frequency to x position using modified log scale optimized for audio work
  function freqToX(freq: number): number {
    // Modified logarithmic scale that gives more visual space to high frequencies
    // This is similar to what professional audio software uses

    if (freq <= 1000) {
      // Standard log scale for low frequencies (20Hz - 1kHz)
      const logMin = Math.log10(MIN_FREQ);
      const log1k = Math.log10(1000);
      const logFreq = Math.log10(freq);
      const lowEndPortion = 0.3; // 30% of the width for 20Hz-1kHz
      return margins.left + ((logFreq - logMin) / (log1k - logMin)) * plotWidth * lowEndPortion;
    } else {
      // Modified scale for high frequencies (1kHz - 20kHz) with more visual space
      const highFreqStart = 1000;
      const highFreqRange = MAX_FREQ - highFreqStart;
      const freqInHighRange = freq - highFreqStart;

      // Use a gentler logarithmic curve for high frequencies
      const normalizedHighFreq =
        Math.log10(1 + (freqInHighRange / highFreqRange) * 9) / Math.log10(10);

      const lowEndPortion = 0.3;
      const highEndStart = margins.left + plotWidth * lowEndPortion;
      const highEndWidth = plotWidth * (1 - lowEndPortion);

      return highEndStart + normalizedHighFreq * highEndWidth;
    }
  }

  // Convert x position to frequency (inverse of freqToX)
  function xToFreq(x: number): number {
    const plotX = x - margins.left;
    const lowEndPortion = 0.3;
    const lowEndWidth = plotWidth * lowEndPortion;

    if (plotX <= lowEndWidth) {
      // Low frequency range (20Hz - 1kHz)
      const logMin = Math.log10(MIN_FREQ);
      const log1k = Math.log10(1000);
      const ratio = plotX / lowEndWidth;
      const logFreq = logMin + ratio * (log1k - logMin);
      return Math.pow(10, logFreq);
    } else {
      // High frequency range (1kHz - 20kHz)
      const highEndWidth = plotWidth * (1 - lowEndPortion);
      const highRatio = (plotX - lowEndWidth) / highEndWidth;

      // Inverse of the modified high-frequency scale
      const normalizedVal = highRatio;
      const logVal = normalizedVal * Math.log10(10);
      const scaledVal = Math.pow(10, logVal) - 1;
      const freqInHighRange = (scaledVal / 9) * (MAX_FREQ - 1000);

      return 1000 + freqInHighRange;
    }
  }

  // Convert dB to y position within plot area
  function dbToY(db: number): number {
    return margins.top + plotHeight - ((db - MIN_DB) / (axis.maxDb - MIN_DB)) * plotHeight;
  }

  // Update canvas dimensions based on container
  function updateCanvasDimensions() {
    if (containerElement) {
      const containerWidth = containerElement.clientWidth;
      // Use most of the container width while maintaining reasonable limits
      canvasWidth = Math.min(Math.max(containerWidth * 0.95, 600), 1200);
    }
  }

  // Clamp a y position to the plot area
  function clampY(y: number): number {
    return Math.max(margins.top, Math.min(margins.top + plotHeight, y));
  }

  // Draw the axes, grid and labels, plus the curve of `toDraw` when given
  function drawGraph(toDraw: EqualizerResponseReady | undefined, flat: boolean) {
    if (!canvas) return;

    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    // Guard against test environments with incomplete canvas context
    const requiredMethods = [
      'beginPath',
      'stroke',
      'clearRect',
      'fillRect',
      'moveTo',
      'lineTo',
      'createLinearGradient',
    ] as const;
    if (requiredMethods.some(method => typeof ctx[method as keyof typeof ctx] !== 'function')) {
      return;
    }

    // Clear entire canvas
    ctx.clearRect(0, 0, canvasWidth, canvasHeight);

    // Set up styles with anti-aliasing for smooth curves
    ctx.imageSmoothingEnabled = true;
    ctx.lineCap = 'round';
    ctx.lineJoin = 'round';
    ctx.font = AXIS_LABEL_FONT;

    // Draw full canvas background with proper dark mode support
    ctx.fillStyle = colors.background;
    ctx.fillRect(0, 0, canvasWidth, canvasHeight);

    // Add subtle gradient overlay for depth (professional audio software style)
    if (colors.background === '#0d1117') {
      // Dark mode subtle gradient overlay
      const gradient = ctx.createLinearGradient(0, 0, 0, canvasHeight);
      gradient.addColorStop(0, 'rgba(255, 255, 255, 0.015)');
      gradient.addColorStop(0.5, 'rgba(255, 255, 255, 0.005)');
      gradient.addColorStop(1, 'rgba(0, 0, 0, 0.02)');
      ctx.fillStyle = gradient;
      ctx.fillRect(0, 0, canvasWidth, canvasHeight);
    }

    // Draw plot area border
    ctx.strokeStyle = colors.grid;
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.rect(margins.left, margins.top, plotWidth, plotHeight);
    ctx.stroke();

    // Draw grid lines within plot area
    ctx.strokeStyle = colors.grid;
    ctx.lineWidth = 0.5;

    // Vertical frequency grid lines
    for (const freq of freqGridLines) {
      const x = freqToX(freq);
      ctx.beginPath();
      ctx.moveTo(x, margins.top);
      ctx.lineTo(x, margins.top + plotHeight);
      ctx.stroke();
    }

    // Horizontal dB grid lines
    for (const db of gridLines) {
      const y = dbToY(db);
      ctx.beginPath();
      ctx.moveTo(margins.left, y);
      ctx.lineTo(margins.left + plotWidth, y);
      ctx.stroke();
    }

    // Draw 0dB reference line (professional style)
    ctx.strokeStyle = colors.reference;
    ctx.lineWidth = 2;
    ctx.setLineDash([8, 4]); // Dashed line for 0dB reference
    const zeroY = dbToY(0);
    ctx.beginPath();
    ctx.moveTo(margins.left, zeroY);
    ctx.lineTo(margins.left + plotWidth, zeroY);
    ctx.stroke();
    ctx.setLineDash([]); // Reset dash pattern

    // Individual filter curves removed - only show combined response

    // Draw combined response curve with clean professional styling
    ctx.globalAlpha = 1;
    ctx.strokeStyle = colors.primary;
    ctx.lineWidth = 3;

    // No glow effects - keep it clean and professional
    ctx.shadowColor = 'transparent';
    ctx.shadowBlur = 0;

    if (toDraw) {
      // A fresh path: the reference line above left its path open, and a bare
      // stroke would redraw it in curve style.
      ctx.beginPath();
      let started = false;
      for (const point of toDraw.response.points) {
        if (point.hz < MIN_FREQ || point.hz > MAX_FREQ) continue;
        const x = freqToX(point.hz);
        const y = clampY(dbToY(point.gainDb));
        if (started) {
          ctx.lineTo(x, y);
        } else {
          ctx.moveTo(x, y);
          started = true;
        }
      }
      ctx.stroke();
    }

    // Subtle label when the curve is flat
    if (flat) {
      ctx.fillStyle = colors.text;
      ctx.font = FLAT_LABEL_FONT;
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      ctx.globalAlpha = colors.background === '#0d1117' ? 0.4 : 0.5; // Lower opacity in dark mode
      ctx.fillText(
        t('settings.audio.audioFilters.graph.flatResponse'),
        canvasWidth / 2,
        margins.top + plotHeight / 2 - 20
      );
      ctx.globalAlpha = 1;
    }

    // Draw labels (the flat label above may have changed the font)
    ctx.font = AXIS_LABEL_FONT;
    ctx.fillStyle = colors.text;
    ctx.textAlign = 'center';
    ctx.textBaseline = 'top';

    // Frequency labels (positioned below plot area) with better formatting for audio work
    for (const freq of freqGridLines) {
      const x = freqToX(freq);
      let label;
      if (freq >= 1000) {
        const kHz = freq / 1000;
        label = kHz % 1 === 0 ? `${kHz}k` : `${kHz.toFixed(1)}k`;
      } else {
        label = freq.toString();
      }
      ctx.fillText(label, x, margins.top + plotHeight + 20);
    }

    // dB labels (positioned to the left of plot area)
    ctx.textAlign = 'right';
    ctx.textBaseline = 'middle';
    for (const db of gridLines) {
      const y = dbToY(db);
      ctx.fillText(`${db}dB`, margins.left - 10, y);
    }

    // Axes labels with proper positioning
    ctx.fillStyle = colors.text;
    ctx.font = AXIS_TITLE_FONT;
    ctx.textAlign = 'center';
    ctx.textBaseline = 'bottom';
    ctx.fillText(
      t('settings.audio.audioFilters.graph.frequency'),
      canvasWidth / 2,
      canvasHeight - 10
    );

    // Y-axis label with better positioning
    ctx.save();
    ctx.translate(15, canvasHeight / 2);
    ctx.rotate(-Math.PI / 2);
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.fillText(t('settings.audio.audioFilters.graph.gain'), 0, 0);
    ctx.restore();
  }

  // Handle mouse move for tooltip
  function handleMouseMove(event: MouseEvent) {
    const rect = canvas.getBoundingClientRect();
    const x = event.clientX - rect.left;
    const y = event.clientY - rect.top;

    // Only show tooltip when mouse is within plot area
    const nearest =
      drawable &&
      x >= margins.left &&
      x <= margins.left + plotWidth &&
      y >= margins.top &&
      y <= margins.top + plotHeight
        ? findNearestPoint(drawable.response.points, xToFreq(x))
        : undefined;

    if (nearest) {
      tooltip = {
        visible: true,
        x: x, // Use canvas-relative coordinates
        y: y, // Use canvas-relative coordinates
        freq: Math.round(nearest.hz),
        gain: Math.round(nearest.gainDb * 10) / 10,
      };
    } else {
      tooltip.visible = false;
    }
  }

  // Handle mouse leave
  function handleMouseLeave() {
    tooltip.visible = false;
  }

  // Update colors and dimensions when mounted
  onMount(() => {
    const announceTimer = setTimeout(() => {
      announce = true;
    }, 0);
    updateColors();
    updateCanvasDimensions();

    // Listen for theme changes
    const observer = new MutationObserver(() => {
      updateColors();
      drawGraph(drawable, isFlat);
    });

    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['data-theme', 'class'],
    });

    // Listen for window resize - with proper browser support check
    let resizeObserver: ResizeObserver | undefined;
    if (typeof globalThis.ResizeObserver !== 'undefined' && containerElement) {
      const RO = globalThis.ResizeObserver as typeof ResizeObserver;
      resizeObserver = new RO(() => {
        updateCanvasDimensions();
        drawGraph(drawable, isFlat);
      });
      resizeObserver.observe(containerElement);
    }

    return () => {
      clearTimeout(announceTimer);
      observer.disconnect();
      resizeObserver?.disconnect();
      loader.dispose();
    };
  });

  // Redraw when the response or the dimensions change. The y axis is derived
  // from the response, so it needs no read of its own. Every dependency is read
  // here, before drawGraph can return early (an incomplete test canvas, say),
  // so the effect always tracks them.
  $effect(() => {
    const toDraw = drawable;
    const flat = isFlat;
    const width = canvasWidth;
    const heightPx = canvasHeight;
    if (canvas && width > 0 && heightPx > 0) {
      updateColors();
      drawGraph(toDraw, flat);
    }
  });
</script>

<!-- Centered container with proper spacing -->
<div class="w-full flex justify-center py-4" bind:this={containerElement}>
  <div
    class="flex flex-col gap-3 focus:outline-none"
    style:width="{canvasWidth}px"
    tabindex="-1"
    bind:this={graphRegion}
  >
    <div class="relative">
      <canvas
        bind:this={canvas}
        width={canvasWidth}
        height={canvasHeight}
        class="block border border-[var(--color-base-300)] rounded-lg cursor-crosshair shadow-lg"
        style:background-color={colors.background}
        onmousemove={handleMouseMove}
        onmouseleave={handleMouseLeave}
      ></canvas>

      {#if graphState.status === 'loading' && !graphState.previous}
        <div
          class="absolute inset-0 flex items-center justify-center pointer-events-none"
          data-testid="eq-graph-loading"
          aria-hidden="true"
        >
          <div
            class="flex items-center gap-2 px-3 py-2 text-sm rounded-lg bg-[var(--color-base-100)] text-[var(--color-base-content)] shadow"
          >
            <LoadingSpinner size="sm" aria-hidden="true" />
            <span>{t('settings.audio.audioFilters.graph.loading')}</span>
          </div>
        </div>
      {/if}

      {#if tooltip && tooltip.visible && tooltip.x != null && tooltip.y != null}
        <div
          class="absolute z-10 px-3 py-2 text-xs bg-[var(--color-base-300)] border border-[var(--color-base-content)]/20 rounded-lg shadow-lg pointer-events-none"
          style:left="{(tooltip.x ?? 0) + 10}px"
          style:top="{(tooltip.y ?? 0) - 10}px"
          style:transform="translateY(-100%)"
        >
          <div class="font-semibold">{tooltip.freq} Hz</div>
          <div
            class={tooltip.gain > 0
              ? 'text-[var(--color-success)]'
              : tooltip.gain < -12
                ? 'text-[var(--color-error)]'
                : ''}
          >
            {tooltip.gain > 0 ? '+' : ''}{tooltip.gain} dB
          </div>
        </div>
      {/if}
    </div>

    <p class="min-h-5 text-xs text-[var(--color-base-content)]/70">
      {#if graphState.status === 'loading' && graphState.previous}
        <span data-testid="eq-graph-updating" aria-hidden="true">
          {t('settings.audio.audioFilters.graph.updating')}
        </span>
      {:else if graphState.status === 'ready'}
        <span data-testid="eq-graph-sample-rate">
          {t('settings.audio.audioFilters.graph.sampleRate', {
            rate: graphState.response.sampleRate,
          })}
        </span>
      {/if}
    </p>

    <p class="min-h-5 text-xs text-[var(--color-base-content)]/70">
      {#if clippedText}
        <span data-testid="eq-graph-clipped" aria-hidden="true">{clippedText}</span>
      {/if}
    </p>

    {#if graphState.status === 'ready' && graphState.response.skipped.length > 0}
      {@const ready = { request: graphState.request, response: graphState.response }}
      <ErrorAlert type="warning" role="note" data-testid="eq-graph-skipped">
        <span class="block font-medium">
          {t('settings.audio.audioFilters.graph.skippedHeading', {
            count: graphState.response.skipped.length,
          })}
        </span>
        <ul class="mt-1 list-disc ps-5 space-y-1">
          {#each graphState.response.skipped as entry (entry.index)}
            <li>
              {skippedMessage(entry, ready)}
              {#if entry.message && entry.reason !== 'unknown_type'}
                <span class="block text-[var(--color-base-content)]/70">{entry.message}</span>
              {/if}
            </li>
          {/each}
        </ul>
      </ErrorAlert>
    {:else if graphState.status === 'error'}
      <ErrorAlert type="error" role="note" data-testid="eq-graph-error">
        <span class="block">{t('settings.audio.audioFilters.graph.error')}</span>
        <Button className="mt-2" onclick={handleRetry}>
          {t('common.retry')}
        </Button>
      </ErrorAlert>
    {/if}

    <div class="sr-only" role="status" aria-atomic="true">{statusText}</div>
    <div class="sr-only" role="alert">{alertText}</div>
  </div>
</div>

<style>
  canvas {
    /* Enable smooth rendering for professional appearance */
    image-rendering: -webkit-optimize-contrast;
    image-rendering: crisp-edges;
  }
</style>
