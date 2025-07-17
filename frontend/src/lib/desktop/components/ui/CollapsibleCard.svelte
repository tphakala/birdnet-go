<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Snippet } from 'svelte';

  interface Props {
    title: string;
    description?: string;
    defaultOpen?: boolean;
    className?: string;
    hasChanges?: boolean;
    children?: Snippet;
  }

  let {
    title,
    description,
    defaultOpen = true,
    className = '',
    hasChanges = false,
    children,
  }: Props = $props();

  let isOpen = $state(defaultOpen);
</script>

<div class={cn('collapse collapse-open bg-base-100 shadow-xs', className)}>
  <input type="checkbox" bind:checked={isOpen} class="min-h-0" />

  <div class="collapse-title px-6 py-4 min-h-0">
    <div class="flex items-center gap-2">
      <h3 class="text-xl font-semibold">{title}</h3>
      {#if hasChanges}
        <span class="badge badge-primary badge-sm" role="status" aria-label="Settings changed">
          changed
        </span>
      {/if}
    </div>
    {#if description}
      <p class="text-sm text-base-content/70 mt-1">{description}</p>
    {/if}
  </div>

  <div class="collapse-content px-6 pb-6">
    {#if children}
      {@render children()}
    {/if}
  </div>
</div>
