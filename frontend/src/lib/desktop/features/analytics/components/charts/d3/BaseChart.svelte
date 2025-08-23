<!-- Base D3 Chart Component -->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import * as d3 from 'd3';
  import type { Snippet } from 'svelte';

  import { ThemeStore, type ChartTheme } from './utils/theme';
  import { createResponsiveScales } from './utils/scales';

  interface Props {
    width?: number;
    height?: number;
    margin?: { top: number; right: number; bottom: number; left: number };
    className?: string;
    id?: string;
    children?: Snippet<
      [
        {
          svg: d3.Selection<globalThis.SVGSVGElement, unknown, null, undefined>;
          chartGroup: d3.Selection<globalThis.SVGGElement, unknown, null, undefined>;
          innerWidth: number;
          innerHeight: number;
          theme: ChartTheme;
        },
      ]
    >;
    onResize?: (_width: number, _height: number) => void;
    responsive?: boolean;
  }

  let {
    width = 800,
    height = 400,
    margin = { top: 20, right: 20, bottom: 40, left: 60 },
    className = '',
    id = `chart-${Math.random().toString(36).substr(2, 9)}`,
    children,
    onResize,
    responsive = true,
  }: Props = $props();

  // DOM references
  let containerElement: HTMLDivElement;
  let svgElement: SVGSVGElement;

  // D3 selections
  let svg: d3.Selection<globalThis.SVGSVGElement, unknown, null, undefined>;
  let chartGroup: d3.Selection<globalThis.SVGGElement, unknown, null, undefined>;

  // Reactive dimensions
  let containerWidth = $state(width);
  let containerHeight = $state(height);

  // Theme management
  let themeStore: ThemeStore;
  let currentTheme = $state<ChartTheme | null>(null);
  let themeUnsubscribe: (() => void) | null = null;

  // Computed properties
  const dimensions = $derived(() => {
    return createResponsiveScales({
      containerWidth,
      containerHeight,
      margin,
    });
  });

  // Initialize chart on mount
  onMount(() => {
    // Initialize theme store
    themeStore = new ThemeStore();
    currentTheme = themeStore.theme;

    // Subscribe to theme changes
    themeUnsubscribe = themeStore.subscribe(theme => {
      currentTheme = theme;
    });

    // Initialize D3 selections
    svg = d3.select(svgElement);

    // Create main chart group with margins
    chartGroup = svg
      .append('g')
      .attr('class', 'chart-group')
      .attr('transform', `translate(${margin.left},${margin.top})`);

    // Setup responsive behavior
    if (responsive) {
      setupResizeObserver();
    }

    // No cleanup needed - handled in onDestroy
  });

  // Cleanup on destroy
  onDestroy(() => {
    themeUnsubscribe?.();
    themeStore?.destroy();
    if (resizeObserver) {
      resizeObserver.disconnect();
    }
  });

  // Resize observer for responsive behavior
  let resizeObserver: globalThis.ResizeObserver | null = null;

  function setupResizeObserver(): void {
    if (!containerElement || !responsive) return;

    resizeObserver = new globalThis.ResizeObserver(entries => {
      for (const entry of entries) {
        const { width: newWidth, height: newHeight } = entry.contentRect;

        if (newWidth > 0 && newHeight > 0) {
          containerWidth = newWidth;
          containerHeight = newHeight;

          // Update SVG dimensions
          svg.attr('width', containerWidth).attr('height', containerHeight);

          // Notify parent component
          onResize?.(containerWidth, containerHeight);
        }
      }
    });

    resizeObserver.observe(containerElement);
  }

  // Update chart group transform when margin changes
  $effect(() => {
    if (chartGroup) {
      chartGroup.attr('transform', `translate(${margin.left},${margin.top})`);
    }
  });

  // Apply theme changes to SVG
  $effect(() => {
    if (svg && currentTheme) {
      svg.style('background-color', currentTheme.background).style('color', currentTheme.text);
    }
  });

  // Provide chart context to children
  const chartContext = $derived.by(() => {
    // CRITICAL: Force $derived execution and dependency tracking without logging
    void {
      hasSvg: !!svg,
      hasChartGroup: !!chartGroup,
      hasTheme: !!currentTheme,
    };

    if (!svg || !chartGroup || !currentTheme) {
      return null;
    }

    const dims = dimensions();
    const context = {
      svg,
      chartGroup,
      innerWidth: dims.innerWidth,
      innerHeight: dims.innerHeight,
      theme: currentTheme,
    };

    return context;
  });
</script>

<div
  bind:this={containerElement}
  class="chart-container {className}"
  style:width={responsive ? '100%' : `${width}px`}
  style:height={responsive ? '100%' : `${height}px`}
  style:min-height="200px"
  role="img"
  aria-label="Data visualization chart"
>
  <svg
    bind:this={svgElement}
    {id}
    width={containerWidth}
    height={containerHeight}
    class="chart-svg"
  >
    <!-- Render children with chart context -->
    {#if children}
      {@const context = chartContext}
      {#if context}
        {@render children(context)}
      {/if}
    {/if}
  </svg>
</div>

<style>
  .chart-container {
    position: relative;
    overflow: hidden;
  }

  .chart-svg {
    display: block;
    font-family:
      system-ui,
      -apple-system,
      sans-serif;
  }

  :global(.chart-svg .axis-label) {
    font-weight: 600;
  }

  :global(.chart-svg .grid line) {
    shape-rendering: crispedges;
  }

  :global(.chart-svg .domain) {
    shape-rendering: crispedges;
  }

  :global(.chart-svg .tick line) {
    shape-rendering: crispedges;
  }

  /* Smooth transitions for theme changes */
  :global(.chart-svg *) {
    transition:
      fill 0.3s ease,
      stroke 0.3s ease,
      opacity 0.3s ease;
  }
</style>
