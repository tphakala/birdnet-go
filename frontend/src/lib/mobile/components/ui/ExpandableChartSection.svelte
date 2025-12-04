<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { navigationIcons } from '$lib/utils/icons';
  import type { Snippet } from 'svelte';

  interface Props {
    title: string;
    expanded?: boolean;
    onToggle?: () => void;
    children?: Snippet;
    className?: string;
  }

  let { title, expanded = false, onToggle, children, className = '' }: Props = $props();
</script>

<div class={cn('card bg-base-100 shadow-sm', className)}>
  <button
    class="w-full flex items-center justify-between p-4"
    onclick={onToggle}
    aria-expanded={expanded}
  >
    <span class="font-medium">{title}</span>
    <span class={cn('transition-transform', expanded && 'rotate-180')}>
      {@html navigationIcons.chevronDown}
    </span>
  </button>

  {#if expanded}
    <div class="px-4 pb-4 pt-0">
      {#if children}
        {@render children()}
      {/if}
    </div>
  {/if}
</div>
