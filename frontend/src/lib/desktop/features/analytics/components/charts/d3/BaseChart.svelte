<!-- Base D3 Chart Component -->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { select } from 'd3-selection';
  import type { Selection } from 'd3-selection';
  import type { Snippet } from 'svelte';

  import { ThemeStore, type ChartTheme } from './utils/theme';
  import { createResponsiveScales } from './utils/scales';

  interface Props {
    width?: number;
    height?: number;
    margin?: { top: number; right: number; bottom: number; left: number };
    className?: string;
    id?: string;
    ariaLabel?: string;
    children?: Snippet<
      [
        {
          svg: Selection<globalThis.SVGSVGElement, unknown, null, undefined>;
          chartGroup: Selection<globalThis.SVGGElement, unknown, null, undefined>;
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
    id,
    ariaLabel,
    children,
    onResize,
    responsive = true,
  }: Props = $props();

  // Generate unique ID on client side to prevent SSR/hydration mismatch
  let chartId = $state<string>('');

  // DOM references
  let containerElement: HTMLDivElement;
  let svgElement: SVGSVGElement;

  // D3 selections
  let svg: Selection<globalThis.SVGSVGElement, unknown, null, undefined>;
  let chartGroup: Selection<globalThis.SVGGElement, unknown, null, undefined>;

  // Observed dimensions from ResizeObserver (null until observed)
  let observedWidth = $state<number | null>(null);
  let observedHeight = $state<number | null>(null);

  // Final dimensions: use observed when available (responsive mode), otherwise use props
  // This pattern avoids $effect for state sync - uses $derived instead
  const containerWidth = $derived(responsive && observedWidth !== null ? observedWidth : width);
  const containerHeight = $derived(responsive && observedHeight !== null ? observedHeight : height);

  // Theme management
  let themeStore: ThemeStore;
  let currentTheme = $state<ChartTheme | null>(null);
  let themeUnsubscribe: (() => void) | null = null;

  // Computed properties
  const dimensions = $derived.by(() => {
    return createResponsiveScales({
      containerWidth,
      containerHeight,
      margin,
    });
  });

  // Initialize chart on mount
  onMount(() => {
    // Generate unique ID on client side to prevent SSR/hydration mismatch
    chartId = id || `chart-${Math.random().toString(36).slice(2, 11)}`;

    // Initialize theme store
    themeStore = new ThemeStore();
    currentTheme = themeStore.theme;

    // Subscribe to theme changes
    themeUnsubscribe = themeStore.subscribe(theme => {
      currentTheme = theme;
    });

    // Initialize D3 selections
    svg = select(svgElement);

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
          observedWidth = newWidth;
          observedHeight = newHeight;

          // Update SVG dimensions (using observed values directly since derived may not have updated yet)
          svg.attr('width', newWidth).attr('height', newHeight);

          // Notify parent component
          onResize?.(newWidth, newHeight);
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

    const dims = dimensions;
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
  aria-label={ariaLabel || 'Data visualization chart'}
>
  <svg
    bind:this={svgElement}
    id={chartId}
    width={containerWidth}
    height={containerHeight}
    viewBox={`0 0 ${containerWidth} ${containerHeight}`}
    preserveAspectRatio="xMidYMid meet"
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
