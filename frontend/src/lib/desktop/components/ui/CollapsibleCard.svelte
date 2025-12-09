<script lang="ts">
  import { untrack } from 'svelte';
  import { cn } from '$lib/utils/cn';
  import type { Snippet } from 'svelte';
  import { ChevronDown } from '@lucide/svelte';

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

  // Use untrack to explicitly capture initial value without creating dependency
  let isOpen = $state(untrack(() => defaultOpen));

  function toggleOpen() {
    isOpen = !isOpen;
  }
</script>

<div class={cn('collapse bg-base-100 shadow-2xs', { 'collapse-open': isOpen }, className)}>
  <button
    type="button"
    class="collapse-title px-6 py-4 min-h-0 text-left w-full cursor-pointer hover:bg-base-200/50 transition-colors"
    onclick={toggleOpen}
    aria-expanded={isOpen}
    aria-controls="collapse-content-{title.toLowerCase().replace(/\s+/g, '-')}"
  >
    <div class="flex items-center gap-2">
      <h3 class="text-xl font-semibold">{title}</h3>
      {#if hasChanges}
        <span class="badge badge-primary badge-sm" role="status" aria-label="Settings changed">
          changed
        </span>
      {/if}
      <!-- Collapse indicator -->
      <div
        class="ml-auto transition-transform duration-200"
        class:rotate-180={isOpen}
        aria-hidden="true"
      >
        <ChevronDown class="size-4" />
      </div>
    </div>
    {#if description}
      <p class="text-sm opacity-70 mt-1" style:color="var(--color-base-content)">{description}</p>
    {/if}
  </button>

  <div
    class="collapse-content px-6 pb-6"
    id="collapse-content-{title.toLowerCase().replace(/\s+/g, '-')}"
  >
    {#if children}
      {@render children()}
    {/if}
  </div>
</div>
