<script lang="ts">
  import { scaleLinear } from 'd3-scale';
  import { line, area, curveMonotoneX } from 'd3-shape';
  import { extent } from 'd3-array';

  interface Props {
    data?: number[];
    color?: string;
    /** Optional threshold value — renders a dashed horizontal line */
    threshold?: number;
    /** Color for the threshold line */
    thresholdColor?: string;
    /** Internal viewBox width for path calculations */
    viewWidth?: number;
    /** Internal viewBox height for path calculations */
    viewHeight?: number;
  }

  let {
    data = [],
    color = 'var(--color-primary)',
    threshold,
    thresholdColor = '#ef4444',
    viewWidth = 200,
    viewHeight = 40,
  }: Props = $props();

  let paths = $derived.by(() => {
    if (data.length < 2) return { line: '', area: '', thresholdY: undefined as number | undefined };

    const padding = 2;
    let [minVal, maxVal] = extent(data) as [number, number];

    // Extend domain to include threshold so the line is always visible
    if (threshold != null) {
      if (threshold > maxVal) maxVal = threshold;
      if (threshold < minVal) minVal = threshold;
    }

    const xScale = scaleLinear()
      .domain([0, data.length - 1])
      .range([0, viewWidth]);

    const yScale = scaleLinear()
      .domain([minVal, maxVal === minVal ? minVal + 1 : maxVal])
      .range([viewHeight - padding, padding]);

    const lineGenerator = line<number>()
      .x((_, i) => xScale(i))
      .y(d => yScale(d))
      .curve(curveMonotoneX);

    const areaGenerator = area<number>()
      .x((_, i) => xScale(i))
      .y0(viewHeight)
      .y1(d => yScale(d))
      .curve(curveMonotoneX);

    return {
      line: lineGenerator(data) ?? '',
      area: areaGenerator(data) ?? '',
      thresholdY: threshold != null ? yScale(threshold) : undefined,
    };
  });
</script>

<svg
  width="100%"
  height="100%"
  viewBox="0 0 {viewWidth} {viewHeight}"
  preserveAspectRatio="none"
  class="overflow-visible"
>
  {#if paths.line}
    <path d={paths.area} fill={color} opacity="0.08" />
    <path
      d={paths.line}
      fill="none"
      stroke={color}
      stroke-width="1.5"
      stroke-linecap="round"
      stroke-linejoin="round"
      vector-effect="non-scaling-stroke"
    />
    {#if paths.thresholdY != null}
      <line
        x1="0"
        y1={paths.thresholdY}
        x2={viewWidth}
        y2={paths.thresholdY}
        stroke={thresholdColor}
        stroke-width="1"
        stroke-dasharray="4 3"
        opacity="0.6"
        vector-effect="non-scaling-stroke"
      />
    {/if}
  {/if}
</svg>
