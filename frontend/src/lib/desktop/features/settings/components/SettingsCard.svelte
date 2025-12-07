<!--
  Settings Card Component

  Purpose: A reusable card container for settings pages that provides
  consistent styling, optional title/description, and flexible content areas.

  Features:
  - Consistent card styling (background, shadow, padding)
  - Optional title with consistent typography
  - Optional description text
  - Optional header slot for custom header content
  - Optional footer slot for action buttons
  - Optional change indicator badge
  - Customizable padding
  - Customizable via additional CSS classes

  Props:
  - title: Optional card title
  - description: Optional description text below title
  - className: Additional CSS classes for the card container
  - hasChanges: Shows a "Changed" badge when true
  - padding: Controls body padding (default: true)

  Snippets:
  - children: Main card content (required)
  - header: Custom header content (replaces title/description)
  - footer: Footer content (e.g., action buttons)

  @component
-->
<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Snippet } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';
  import { t } from '$lib/i18n';

  interface Props extends HTMLAttributes<HTMLDivElement> {
    title?: string;
    description?: string;
    className?: string;
    hasChanges?: boolean;
    padding?: boolean;
    children?: Snippet;
    header?: Snippet;
    footer?: Snippet;
  }

  let {
    title,
    description,
    className,
    hasChanges = false,
    padding = true,
    children,
    header,
    footer,
    ...rest
  }: Props = $props();

  // Memoized class names for performance
  let cardClasses = $derived(cn('card bg-base-100 shadow-2xs', className));
  let headerClasses = $derived(cn('px-6 py-4'));
  let bodyClasses = $derived(padding ? 'px-6 pb-7' : '');
</script>

<div class={cardClasses} data-testid="settings-card" {...rest}>
  {#if header}
    <div class="px-6 py-5">
      {@render header()}
    </div>
  {:else if title || description || hasChanges}
    <div class={headerClasses}>
      <div class="flex items-center justify-between">
        <div>
          {#if title}
            <h3 class="text-lg font-semibold">{title}</h3>
          {/if}
          {#if description}
            <p class="text-sm text-base-content/80 mt-1">{description}</p>
          {/if}
        </div>
        {#if hasChanges}
          <span
            class="badge badge-primary badge-sm"
            role="status"
            aria-label={t('settings.card.changedAriaLabel')}
          >
            {t('settings.card.changed')}
          </span>
        {/if}
      </div>
    </div>
  {/if}

  <div class={bodyClasses}>
    {#if children}
      {@render children()}
    {/if}
  </div>

  {#if footer}
    <div class="px-6 pb-6 pt-4 border-t border-base-200">
      {@render footer()}
    </div>
  {/if}
</div>
