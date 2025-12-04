<script lang="ts">
  import { cn } from '$lib/utils/cn';

  interface Props {
    title: string;
    value: string | number;
    subtitle?: string;
    iconHtml?: string;
    iconClassName?: string;
    isLoading?: boolean;
    className?: string;
  }

  let {
    title,
    value,
    subtitle,
    iconHtml,
    iconClassName = 'bg-primary/20 text-primary',
    isLoading = false,
    className = '',
  }: Props = $props();
</script>

<div class={cn('card bg-base-100 shadow-sm', className)}>
  <div class="card-body p-3">
    <div class="flex items-center gap-2">
      {#if iconHtml}
        <div
          class={cn(
            'w-8 h-8 rounded-full flex items-center justify-center shrink-0',
            iconClassName
          )}
        >
          {@html iconHtml}
        </div>
      {/if}
      <div class="min-w-0">
        <div class="text-xs text-base-content/60 truncate">{title}</div>
        <div class="text-lg font-bold truncate">
          {isLoading ? '...' : value}
        </div>
        {#if subtitle}
          <div class="text-xs text-base-content/50 truncate">{subtitle}</div>
        {/if}
      </div>
    </div>
  </div>
</div>
