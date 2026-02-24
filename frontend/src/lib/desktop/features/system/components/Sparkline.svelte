<script lang="ts">
  interface Props {
    data?: number[];
    color?: string;
    width?: number;
    height?: number;
  }

  let { data = [], color = 'var(--color-primary)', width = 120, height = 32 }: Props = $props();

  let pathD = $derived.by(() => {
    if (data.length < 2) return '';
    const min = Math.min(...data);
    const max = Math.max(...data);
    const range = max - min || 1;
    const stepX = width / (data.length - 1);
    const padding = 2;
    const chartHeight = height - padding * 2;

    return data
      .map((v, i) => {
        const x = i * stepX;
        const y = padding + chartHeight - ((v - min) / range) * chartHeight;
        return `${i === 0 ? 'M' : 'L'}${x.toFixed(1)},${y.toFixed(1)}`;
      })
      .join(' ');
  });

  let areaD = $derived.by(() => {
    if (!pathD) return '';
    return `${pathD} L${width},${height} L0,${height} Z`;
  });
</script>

<svg {width} {height} viewBox="0 0 {width} {height}" class="overflow-visible">
  {#if pathD}
    <path d={areaD} fill={color} opacity="0.08" />
    <path
      d={pathD}
      fill="none"
      stroke={color}
      stroke-width="1.5"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
  {/if}
</svg>
