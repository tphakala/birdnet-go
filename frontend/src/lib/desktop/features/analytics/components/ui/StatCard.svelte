<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Snippet } from 'svelte';

  interface Props {
    title: string;
    value: string | number;
    subtitle?: string;
    icon?: Snippet;
    iconClassName?: string;
    className?: string;
    valueClassName?: string;
    isLoading?: boolean;
  }

  let {
    title,
    value,
    subtitle,
    icon,
    iconClassName = '',
    className = '',
    valueClassName = '',
    isLoading = false,
  }: Props = $props();
</script>

<div class={cn('card bg-base-100 shadow-xs', className)}>
  <div class="card-body p-4 md:p-6">
    <h2 class="card-title text-lg">{title}</h2>
    <div class="flex items-center gap-3">
      {#if icon}
        <div class={cn('w-12 h-12 rounded-full flex items-center justify-center', iconClassName)}>
          {@render icon()}
        </div>
      {/if}
      <div>
        <div class={cn('text-3xl font-bold', valueClassName)}>
          {isLoading ? '...' : value}
        </div>
        {#if subtitle}
          <div class="text-xs text-base-content/60">{subtitle}</div>
        {/if}
      </div>
    </div>
  </div>
</div>
