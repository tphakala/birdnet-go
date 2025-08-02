<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Snippet } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';
  import { navigationIcons } from '$lib/utils/icons';

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

  let isOpen = $state(defaultOpen);

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

<div class={cn('collapse bg-base-100 shadow-sm', className)} {...rest}>
  <!-- Hidden checkbox for DaisyUI compatibility -->
  <input type="checkbox" class="sr-only" aria-hidden="true" bind:checked={isOpen} />
  <button
    type="button"
    class={cn('collapse-title text-xl font-medium w-full text-left', titleClassName)}
    onclick={toggleOpen}
    onkeydown={handleKeydown}
    aria-expanded={isOpen}
    aria-controls="{title}-content"
  >
    <div class="flex items-center justify-between">
      <span>{title}</span>
      <div class={cn('transition-transform duration-200', isOpen ? 'rotate-180' : '')}>
        {@html navigationIcons.chevronDown}
      </div>
    </div>
  </button>
  <div id="{title}-content" class={cn('collapse-content', contentClassName)} aria-hidden={!isOpen}>
    {#if children}
      {@render children()}
    {/if}
  </div>
</div>
