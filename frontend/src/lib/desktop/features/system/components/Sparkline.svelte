<script lang="ts">
  import { scaleLinear } from 'd3-scale';
  import { line, area, curveMonotoneX } from 'd3-shape';
  import { extent } from 'd3-array';

  interface Props {
    data?: number[];
    color?: string;
    width?: number;
    height?: number;
  }

  let { data = [], color = 'var(--color-primary)', width = 120, height = 32 }: Props = $props();

  let paths = $derived.by(() => {
    if (data.length < 2) return { line: '', area: '' };

    const padding = 2;
    const [minVal, maxVal] = extent(data) as [number, number];

    const xScale = scaleLinear()
      .domain([0, data.length - 1])
      .range([0, width]);

    const yScale = scaleLinear()
      .domain([minVal, maxVal === minVal ? minVal + 1 : maxVal])
      .range([height - padding, padding]);

    const lineGenerator = line<number>()
      .x((_, i) => xScale(i))
      .y(d => yScale(d))
      .curve(curveMonotoneX);

    const areaGenerator = area<number>()
      .x((_, i) => xScale(i))
      .y0(height)
      .y1(d => yScale(d))
      .curve(curveMonotoneX);

    return {
      line: lineGenerator(data) ?? '',
      area: areaGenerator(data) ?? '',
    };
  });
</script>

<svg {width} {height} viewBox="0 0 {width} {height}" class="overflow-visible">
  {#if paths.line}
    <path d={paths.area} fill={color} opacity="0.08" />
    <path
      d={paths.line}
      fill="none"
      stroke={color}
      stroke-width="1.5"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
  {/if}
</svg>
