<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Snippet } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';

  interface Props extends HTMLAttributes<HTMLDivElement> {
    title?: string;
    description?: string;
    padding?: boolean;
    className?: string;
    header?: Snippet;
    children?: Snippet;
    footer?: Snippet;
  }

  let {
    title = '',
    description,
    padding = true,
    className = '',
    header,
    children,
    footer,
    ...rest
  }: Props = $props();

  // Native Tailwind card styles
  const cardClasses = 'rounded-lg overflow-hidden bg-[var(--color-base-100)] shadow-sm';
</script>

<div class={cn(cardClasses, className)} {...rest}>
  {#if title || description || header}
    <div class="px-6 py-4">
      {#if header}
        {@render header()}
      {:else}
        <div class="flex items-center gap-2">
          {#if title}
            <h3 class="text-xl font-semibold">{title}</h3>
          {/if}
        </div>
        {#if description}
          <p class="text-sm opacity-70 mt-1 text-[var(--color-base-content)]">
            {description}
          </p>
        {/if}
      {/if}
    </div>
  {/if}

  <div class={cn(padding ? 'px-6 pb-6' : '')}>
    {#if children}
      {@render children()}
    {/if}
  </div>

  {#if footer}
    <div class="px-6 pb-6">
      {@render footer()}
    </div>
  {/if}
</div>
