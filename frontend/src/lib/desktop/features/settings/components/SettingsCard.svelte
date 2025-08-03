<!--
  Settings Card Component
  
  Purpose: A flexible card component for settings pages that provides consistent
  styling, header/body/footer sections, and change indicators.
  
  Features:
  - Flexible layout with header, body, and footer sections
  - Visual change indicator badge when hasChanges is true
  - Customizable padding for body section
  - Support for custom header content via snippet
  - Consistent card styling with shadow and theming
  - Semantic HTML structure with proper ARIA labels
  
  Props:
  - title: Card title (optional)
  - description: Card description (optional)
  - padding: Whether to add padding to body (default: true)
  - className: Additional CSS classes (optional)
  - hasChanges: Whether to show change indicator (default: false)
  - header: Custom header content snippet (optional)
  - children: Body content snippet (optional)
  - footer: Footer content snippet (optional)
  
  Performance Optimizations:
  - Memoized render conditions with $derived
  - Efficient class name computation
  - Minimal DOM updates through proper reactivity
  
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

  // PERFORMANCE OPTIMIZATION: Memoize computed values with $derived
  let showHeader = $derived(!!title || !!description || !!header);
  let bodyClasses = $derived(cn(padding ? 'px-6 pb-6' : ''));
  let cardClasses = $derived(cn('card bg-base-100 shadow-xs', className));
</script>

<div class={cardClasses} data-testid="settings-card" {...rest}>
  {#if showHeader}
    <div class="px-6 py-4">
      {#if header}
        {@render header()}
      {:else}
        <div class="flex items-center gap-2">
          {#if title}
            <h3 class="text-xl font-semibold">{title}</h3>
          {/if}
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
        {#if description}
          <p class="text-sm text-base-content/70 mt-1">{description}</p>
        {/if}
      {/if}
    </div>
  {/if}

  <div class={bodyClasses}>
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
