<script lang="ts">
  import { cn } from '$lib/utils/cn';

  interface Props {
    title: string;
    value: string;
    percent: number;
    subtitle?: string;
    iconHtml?: string;
    className?: string;
  }

  let { title, value, percent, subtitle, iconHtml, className = '' }: Props = $props();

  let progressColor = $derived(
    percent > 90 ? 'bg-error' : percent > 70 ? 'bg-warning' : 'bg-primary'
  );
</script>

<div class={cn('card bg-base-100 shadow-sm', className)}>
  <div class="card-body p-4">
    <div class="flex items-center gap-3">
      {#if iconHtml}
        <div class="text-base-content/60">
          {@html iconHtml}
        </div>
      {/if}
      <div class="min-w-0 flex-1">
        <div class="text-sm text-base-content/60">{title}</div>
        <div class="font-semibold">{value}</div>
        {#if subtitle}
          <div class="text-xs text-base-content/50">{subtitle}</div>
        {/if}
      </div>
    </div>
    <div class="mt-2">
      <div class="h-2 w-full overflow-hidden rounded-full bg-base-200">
        <div
          class={cn('h-full rounded-full transition-all', progressColor)}
          style:width="{percent}%"
        ></div>
      </div>
      <div class="mt-1 text-right text-xs text-base-content/50">{percent.toFixed(1)}%</div>
    </div>
  </div>
</div>
