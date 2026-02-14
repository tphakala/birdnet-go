<!--
  Filter Response Graph Component

  Purpose: Visualizes frequency response curves for audio filters

  Features:
  - Real-time frequency response visualization
  - Modified logarithmic frequency scale optimized for high frequencies (20Hz - 20kHz)
  - Gain display (-48dB to +12dB)
  - Interactive tooltips on hover
  - Combined response curve (single line representing filter chain)
  - Shows flat 0dB response when no filters are applied
  - Responsive width with professional margins
  - Supports BandReject (notch) filters with width parameter

  Props:
  - filters: Array of filter configurations (can be empty)
  - height: Canvas height (optional, defaults to 400px)

  Width is auto-calculated based on container size for responsive behavior.

  @component
-->
<script lang="ts">
  /* global ResizeObserver */
  import { onMount } from 'svelte';
  import {
    calculateCombinedResponse,
    type FilterConfig,
    MIN_DB,
    MAX_DB,
  } from '$lib/utils/audio/dsp';
  import type { EqualizerFilterType } from '$lib/stores/settings';
  import { t } from '$lib/i18n';

  interface Filter {
    type: EqualizerFilterType;
    frequency: number;
    q?: number;
    width?: number;
    passes?: number;
  }

  interface Props {
    filters: Filter[];
    height?: number;
  }

  // Make responsive to container width, default to reasonable size
  let { filters = [], height = 400 }: Props = $props();

  // Container element for measuring width
  let containerElement: HTMLDivElement;

  // Canvas element reference
  let canvas: HTMLCanvasElement;
  let tooltip = $state({ visible: false, x: 0, y: 0, freq: 0, gain: 0 });

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

  // Frequency range - standard audio range up to 20kHz (human hearing limit)
  const MIN_FREQ = 20;
  const MAX_FREQ = 20000;
  // MIN_DB and MAX_DB imported from DSP utilities

  // Grid lines optimized for audio engineering work - more detail in bird frequency range
  const freqGridLines = [
    20, 50, 100, 200, 500, 1000, 2000, 3000, 4000, 5000, 6000, 8000, 10000, 12000, 15000, 20000,
  ];
  const dbGridLines = [-48, -36, -24, -12, 0, 12];

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

  // Convert local Filter type to FilterConfig for DSP utilities
  function toFilterConfig(filter: Filter): FilterConfig {
    return {
      type: filter.type as FilterConfig['type'],
      frequency: filter.frequency,
      q: filter.q,
      width: filter.width,
      passes: filter.passes,
    };
  }

  // Calculate combined response of all filters using DSP utilities
  function getCombinedResponse(frequency: number): number {
    if (filters.length === 0) {
      return 0; // Flat response when no filters
    }
    try {
      const filterConfigs = filters.map(toFilterConfig);
      return calculateCombinedResponse(filterConfigs, frequency);
    } catch {
      // Fallback to flat response on calculation error
      return 0;
    }
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
    return margins.top + plotHeight - ((db - MIN_DB) / (MAX_DB - MIN_DB)) * plotHeight;
  }

  // Update canvas dimensions based on container
  function updateCanvasDimensions() {
    if (containerElement) {
      const containerWidth = containerElement.clientWidth;
      // Use most of the container width while maintaining reasonable limits
      canvasWidth = Math.min(Math.max(containerWidth * 0.95, 600), 1200);
    }
  }

  // Draw the frequency response graph
  function drawGraph() {
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
    ctx.font = '11px system-ui, -apple-system, sans-serif';

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
    for (const db of dbGridLines) {
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

    // Draw with higher resolution for smooth curve, using alpha fade for extreme values
    const steps = plotWidth * 2; // Higher resolution

    // Draw curve in segments to handle alpha transparency changes
    let lastCanvasX = margins.left;
    let lastY = dbToY(getCombinedResponse(xToFreq(margins.left)));

    for (let step = 1; step <= steps; step++) {
      const plotX = (step / steps) * plotWidth;
      const canvasX = margins.left + plotX;
      const freq = xToFreq(canvasX);
      const gain = getCombinedResponse(freq);

      // Clamp Y to plot bounds - this ensures continuous line even for deep notches
      const y = Math.max(margins.top, Math.min(margins.top + plotHeight, dbToY(gain)));

      // Draw line segment (always draw for continuous curve)
      ctx.beginPath();
      ctx.moveTo(lastCanvasX, lastY);
      ctx.lineTo(canvasX, y);
      ctx.stroke();

      lastCanvasX = canvasX;
      lastY = y;
    }

    // Add subtle text when no filters are present - professional styling
    if (filters.length === 0) {
      ctx.fillStyle = colors.text;
      ctx.font = '13px system-ui, -apple-system, sans-serif';
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

    // Draw labels
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
    for (const db of dbGridLines) {
      const y = dbToY(db);
      ctx.fillText(`${db}dB`, margins.left - 10, y);
    }

    // Axes labels with proper positioning
    ctx.fillStyle = colors.text;
    ctx.font = '12px system-ui, -apple-system, sans-serif';
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
    if (
      x >= margins.left &&
      x <= margins.left + plotWidth &&
      y >= margins.top &&
      y <= margins.top + plotHeight
    ) {
      const freq = Math.round(xToFreq(x));
      const gain = getCombinedResponse(freq);

      tooltip = {
        visible: true,
        x: x, // Use canvas-relative coordinates
        y: y, // Use canvas-relative coordinates
        freq,
        gain: Math.round(gain * 10) / 10,
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
    updateColors();
    updateCanvasDimensions();

    // Listen for theme changes
    const observer = new MutationObserver(() => {
      updateColors();
      drawGraph();
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
        drawGraph();
      });
      resizeObserver.observe(containerElement);
    }

    return () => {
      observer.disconnect();
      resizeObserver?.disconnect();
    };
  });

  // Redraw graph when filters change or dimensions update
  $effect(() => {
    if (canvas) {
      updateColors();
      drawGraph();
    }
  });

  // Update dimensions when container size changes
  $effect(() => {
    // This effect will run when canvasWidth changes
    if (canvas && canvasWidth) {
      drawGraph();
    }
  });
</script>

<!-- Centered container with proper spacing -->
<div class="w-full flex justify-center py-4" bind:this={containerElement}>
  <div class="relative">
    <canvas
      bind:this={canvas}
      width={canvasWidth}
      height={canvasHeight}
      class="border border-base-300 rounded-lg cursor-crosshair shadow-lg"
      style:background-color={colors.background}
      onmousemove={handleMouseMove}
      onmouseleave={handleMouseLeave}
    ></canvas>

    {#if tooltip && tooltip.visible && tooltip.x != null && tooltip.y != null}
      <div
        class="absolute z-10 px-3 py-2 text-xs bg-base-300 border border-base-content/20 rounded-lg shadow-lg pointer-events-none"
        style:left="{(tooltip.x ?? 0) + 10}px"
        style:top="{(tooltip.y ?? 0) - 10}px"
        style:transform="translateY(-100%)"
      >
        <div class="font-semibold">{tooltip.freq} Hz</div>
        <div class={tooltip.gain > 0 ? 'text-success' : tooltip.gain < -12 ? 'text-error' : ''}>
          {tooltip.gain > 0 ? '+' : ''}{tooltip.gain} dB
        </div>
      </div>
    {/if}
  </div>
</div>

<style>
  canvas {
    /* Enable smooth rendering for professional appearance */
    image-rendering: -webkit-optimize-contrast;
    image-rendering: crisp-edges;
  }
</style>
