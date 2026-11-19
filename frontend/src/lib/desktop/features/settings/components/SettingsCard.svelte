<!--
  Settings Card Component

  Purpose: A reusable card container for settings pages that provides
  consistent styling, optional title/description, and flexible content areas.

  Features:
  - Consistent card styling (background, shadow, padding)
  - Optional title with consistent typography
  - Optional icon before the title
  - Optional description text
  - Divider under the default header
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
  - icon: Optional icon component drawn before the title; decorative (aria-hidden).
    Shown only with a title and never with a custom header snippet

  Snippets:
  - children: Main card content (required)
  - header: Custom header content (replaces the whole default header: title, icon,
    description, change badge and divider)
  - footer: Footer content (e.g., action buttons)

  @component
-->
<script lang="ts" module>
  /**
   * Classes of the default header: spacing and the hairline divider above the body.
   * Exported so a loading skeleton that mirrors the card keeps the same height.
   */
  export const SETTINGS_CARD_HEADER_CLASSES =
    'mx-6 pt-5 pb-3 mb-5 border-b border-[var(--color-base-200)]';
</script>

<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Component, Snippet } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';
  import { t } from '$lib/i18n';

  interface Props extends HTMLAttributes<HTMLDivElement> {
    title?: string;
    description?: string;
    className?: string;
    hasChanges?: boolean;
    padding?: boolean;
    icon?: Component<{ class?: string; 'aria-hidden'?: 'true' }>;
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
    icon: Icon,
    children,
    header,
    footer,
    ...rest
  }: Props = $props();

  // Memoized class names for performance
  let cardClasses = $derived(
    cn('card bg-[var(--color-base-100)] shadow-2xs overflow-visible', className)
  );
  let bodyClasses = $derived(padding ? 'px-6 pb-7' : '');
</script>

<div class={cardClasses} data-testid="settings-card" {...rest}>
  {#if header}
    <div class="px-6 py-5">
      {@render header()}
    </div>
  {:else if title || description || hasChanges}
    <div class={SETTINGS_CARD_HEADER_CLASSES} data-testid="settings-card-header">
      <div class="flex items-center justify-between gap-3">
        <div class="min-w-0">
          {#if title}
            <h3 class="flex items-center gap-2 text-lg font-semibold">
              {#if Icon}
                <Icon class="size-5 shrink-0" aria-hidden="true" />
              {/if}
              {title}
            </h3>
          {/if}
          {#if description}
            <p class="text-sm text-[color:var(--color-base-content)] opacity-80 mt-1">
              {description}
            </p>
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
    <div class="px-6 pb-6 pt-4 border-t border-[var(--color-base-200)]">
      {@render footer()}
    </div>
  {/if}
</div>
