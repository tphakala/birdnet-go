<script lang="ts">
  import { scaleLinear, scaleBand } from 'd3-scale';
  import { max } from 'd3-array';
  import { t } from '$lib/i18n';
  import type { HourlyCount } from '$lib/types/database';

  interface Props {
    data: HourlyCount[];
    color?: string;
    width?: number;
    height?: number;
  }

  let { data = [], color = '#f59e0b', width = 280, height = 80 }: Props = $props();

  const margin = { top: 4, right: 0, bottom: 16, left: 0 };

  let chartData = $derived.by(() => {
    if (data.length === 0)
      return {
        bars: [] as {
          x: number;
          y: number;
          w: number;
          h: number;
          count: number;
          label: string;
        }[],
        innerW: 0,
        innerH: 0,
      };

    const innerW = width - margin.left - margin.right;
    const innerH = height - margin.top - margin.bottom;

    const xScale = scaleBand<number>()
      .domain(data.map((_, i) => i))
      .range([0, innerW])
      .padding(0.15);

    const maxCount = Math.max(1, max(data, d => d.count) ?? 1);
    const yScale = scaleLinear().domain([0, maxCount]).range([innerH, 0]);

    const bars = data.map((d, i) => {
      const hour = new Date(d.hour);
      return {
        x: xScale(i) ?? 0,
        y: yScale(d.count),
        w: xScale.bandwidth(),
        h: innerH - yScale(d.count),
        count: d.count,
        label: `${hour.getHours()}`,
      };
    });

    return { bars, innerW, innerH };
  });

  // Show hour labels every 4th bar to avoid clutter
  function showLabel(index: number): boolean {
    return index % 4 === 0;
  }
</script>

<svg
  {width}
  {height}
  viewBox="0 0 {width} {height}"
  class="overflow-visible"
  role="img"
  aria-label={t('system.database.dashboard.detectionRate.chartLabel')}
>
  <g transform="translate({margin.left},{margin.top})">
    {#each chartData.bars as bar, i (i)}
      <rect x={bar.x} y={bar.y} width={bar.w} height={bar.h} fill={color} opacity="0.7" rx="1">
        <title
          >{t('system.database.dashboard.detectionRate.detectionsTooltip', {
            count: bar.count,
          })}</title
        >
      </rect>
      {#if showLabel(i)}
        <text
          x={bar.x + bar.w / 2}
          y={chartData.innerH + 12}
          text-anchor="middle"
          fill="currentColor"
          opacity="0.4"
          font-size="8"
        >
          {bar.label}
        </text>
      {/if}
    {/each}
  </g>
</svg>
