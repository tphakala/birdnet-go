<script lang="ts">
  import { scaleLinear } from 'd3-scale';
  import { line, area, curveMonotoneX } from 'd3-shape';
  import { extent } from 'd3-array';

  interface Dataset {
    data: number[];
    color: string;
  }

  interface Props {
    data?: number[];
    color?: string;
    datasets?: Dataset[];
    threshold?: number;
    thresholdColor?: string;
    viewWidth?: number;
    viewHeight?: number;
    decorative?: boolean;
    emptyLabel?: string;
  }

  let {
    data = [],
    color = 'var(--color-primary)',
    datasets,
    threshold,
    thresholdColor = '#ef4444',
    viewWidth = 200,
    viewHeight = 40,
    decorative = false,
    emptyLabel,
  }: Props = $props();

  let effectiveDatasets = $derived.by((): Dataset[] => {
    if (datasets && datasets.length > 0) return datasets;
    if (data.length > 0) return [{ data, color }];
    return [];
  });

  interface PathResult {
    line: string;
    area: string;
    color: string;
  }

  let rendered = $derived.by((): { paths: PathResult[]; thresholdY: number | undefined } => {
    if (effectiveDatasets.length === 0) {
      return { paths: [], thresholdY: undefined };
    }

    const padding = 2;

    // Shared X domain: align all datasets to the right edge (most recent point)
    const maxLen = Math.max(0, ...effectiveDatasets.map(ds => ds.data.length));

    let globalMin = Infinity;
    let globalMax = -Infinity;
    for (const ds of effectiveDatasets) {
      if (ds.data.length < 2) continue;
      const [mn, mx] = extent(ds.data) as [number, number];
      if (mn < globalMin) globalMin = mn;
      if (mx > globalMax) globalMax = mx;
    }
    if (threshold != null) {
      if (threshold > globalMax) globalMax = threshold;
      if (threshold < globalMin) globalMin = threshold;
    }
    if (!isFinite(globalMin)) globalMin = 0;
    if (!isFinite(globalMax) || globalMax === globalMin) globalMax = globalMin + 1;

    const yScale = scaleLinear()
      .domain([globalMin, globalMax])
      .range([viewHeight - padding, padding]);

    const xScale = scaleLinear()
      .domain([0, maxLen - 1])
      .range([0, viewWidth]);

    const paths: PathResult[] = [];
    for (const ds of effectiveDatasets) {
      if (ds.data.length < 2) continue;

      const offset = maxLen - ds.data.length;

      const lineGenerator = line<number>()
        .x((_, i) => xScale(offset + i))
        .y(d => yScale(d))
        .curve(curveMonotoneX);

      const areaGenerator = area<number>()
        .x((_, i) => xScale(offset + i))
        .y0(viewHeight)
        .y1(d => yScale(d))
        .curve(curveMonotoneX);

      paths.push({
        line: lineGenerator(ds.data) ?? '',
        area: areaGenerator(ds.data) ?? '',
        color: ds.color,
      });
    }

    return {
      paths,
      thresholdY: threshold != null ? yScale(threshold) : undefined,
    };
  });

  // Show a subtle placeholder when a label is provided and nothing renders yet
  // (e.g. during the SSE startup window before the first data points arrive).
  let showEmpty = $derived(emptyLabel != null && rendered.paths.length === 0);
</script>

<div class="relative w-full h-full">
  <svg
    width="100%"
    height="100%"
    viewBox="0 0 {viewWidth} {viewHeight}"
    preserveAspectRatio="none"
    class="overflow-visible"
    aria-hidden={decorative ? 'true' : undefined}
  >
    {#each rendered.paths as p, i (i)}
      <path d={p.area} fill={p.color} opacity="0.08" />
      <path
        d={p.line}
        fill="none"
        stroke={p.color}
        stroke-width="1.5"
        stroke-linecap="round"
        stroke-linejoin="round"
        vector-effect="non-scaling-stroke"
      />
    {/each}
    {#if rendered.thresholdY != null}
      <line
        x1="0"
        y1={rendered.thresholdY}
        x2={viewWidth}
        y2={rendered.thresholdY}
        stroke={thresholdColor}
        stroke-width="1"
        stroke-dasharray="4 3"
        opacity="0.6"
        vector-effect="non-scaling-stroke"
      />
    {/if}
  </svg>
  {#if showEmpty}
    <div
      class="absolute inset-0 flex items-center justify-center text-xs text-base-content/60 pointer-events-none"
    >
      {emptyLabel}
    </div>
  {/if}
</div>
