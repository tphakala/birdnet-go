<script lang="ts">
  import { untrack } from 'svelte';
  import { cn } from '$lib/utils/cn';
  import type { Snippet } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';
  import { ChevronDown } from '@lucide/svelte';

  interface Props extends HTMLAttributes<HTMLDivElement> {
    title: string;
    defaultOpen?: boolean;
    className?: string;
    titleClassName?: string;
    contentClassName?: string;
    children?: Snippet;
  }

  let {
    title,
    defaultOpen = false,
    className = '',
    titleClassName = '',
    contentClassName = '',
    children,
    ...rest
  }: Props = $props();

  // Use untrack to explicitly capture initial value without creating dependency
  let isOpen = $state(untrack(() => defaultOpen));

  // Slugify title for valid HTML id attribute
  let slugifiedTitle = $derived(
    title
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/(^-|-$)/g, '')
  );

  function toggleOpen() {
    isOpen = !isOpen;
  }

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      toggleOpen();
    }
  }
</script>

<div class={cn('collapse bg-base-100 shadow-xs', className)} {...rest}>
  <!-- Hidden checkbox for DaisyUI compatibility -->
  <input type="checkbox" class="sr-only" aria-hidden="true" tabindex="-1" bind:checked={isOpen} />
  <button
    type="button"
    class={cn('collapse-title text-xl font-medium w-full text-left', titleClassName)}
    onclick={toggleOpen}
    onkeydown={handleKeydown}
    aria-expanded={isOpen}
    aria-controls="{slugifiedTitle}-content"
  >
    <div class="flex items-center justify-between">
      <span>{title}</span>
      <div class={cn('transition-transform duration-200', isOpen ? 'rotate-180' : '')}>
        <ChevronDown class="size-5" />
      </div>
    </div>
  </button>
  <div
    id="{slugifiedTitle}-content"
    class={cn('collapse-content', contentClassName)}
    aria-hidden={!isOpen}
  >
    {#if children}
      {@render children()}
    {/if}
  </div>
</div>
