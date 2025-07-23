<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Snippet } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';
  import { t } from '$lib/i18n/index.js';

  interface Props extends HTMLAttributes<HTMLDivElement> {
    title?: string;
    description?: string;
    padding?: boolean;
    className?: string;
    hasChanges?: boolean;
    header?: Snippet;
    children?: Snippet;
    footer?: Snippet;
  }

  let {
    title = '',
    description,
    padding = true,
    className = '',
    hasChanges = false,
    header,
    children,
    footer,
    ...rest
  }: Props = $props();
</script>

<div class={cn('card bg-base-100 shadow-xs', className)} {...rest}>
  {#if title || description || header}
    <div class="px-6 py-4">
      {#if header}
        {@render header()}
      {:else}
        <div class="flex items-center gap-2">
          {#if title}
            <h3 class="text-xl font-semibold">{title}</h3>
          {/if}
          {#if hasChanges}
            <span class="badge badge-primary badge-sm" role="status" aria-label={t('settings.card.changedAriaLabel')}>
              {t('settings.card.changed')}
            </span>
          {/if}
        </div>
        {#if description}
          <p class="text-sm text-base-content/70 mt-1">{description}</p>
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
