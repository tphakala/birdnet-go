<script lang="ts">
  import { scaleLinear } from 'd3-scale';
  import { line, area, curveMonotoneX } from 'd3-shape';
  import { extent } from 'd3-array';

  interface Props {
    data?: number[];
    color?: string;
    /** Internal viewBox width for path calculations */
    viewWidth?: number;
    /** Internal viewBox height for path calculations */
    viewHeight?: number;
  }

  let {
    data = [],
    color = 'var(--color-primary)',
    viewWidth = 200,
    viewHeight = 40,
  }: Props = $props();

  let paths = $derived.by(() => {
    if (data.length < 2) return { line: '', area: '' };

    const padding = 2;
    const [minVal, maxVal] = extent(data) as [number, number];

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
  {/if}
</svg>
