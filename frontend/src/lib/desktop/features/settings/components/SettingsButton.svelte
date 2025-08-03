<!--
  SettingsButton Component
  
  Purpose: A reusable button component that matches the .settings-input-group button styles
  from custom.css. Used for action buttons in settings forms like "Test Connection".
  
  Features:
  - Matches exact styling from custom.css (.settings-input-group button)
  - Auto-sizing width to fit content
  - Loading state support with spinner
  - Disabled state handling
  - CSS-based hover effects for better performance
  - Theme-compatible colors using CSS variables
  
  Props:
  - onclick: Click handler function
  - disabled: Whether button is disabled
  - loading: Whether to show loading spinner
  - loadingText: Text to show when loading (default: from translation)
  - className: Additional CSS classes
  - children: Button content snippet
  
  Performance Optimizations:
  - Replaced inline style manipulation with CSS hover effects
  - Uses $derived for reactive default loading text
  - Minimal re-renders through proper state management
  
  @component
-->
<script lang="ts">
  import { t } from '$lib/i18n';

  interface Props {
    onclick?: () => void;
    disabled?: boolean;
    loading?: boolean;
    loadingText?: string;
    className?: string;
    children?: import('svelte').Snippet;
  }

  let {
    onclick,
    disabled = false,
    loading = false,
    loadingText,
    className = '',
    children,
  }: Props = $props();

  // PERFORMANCE OPTIMIZATION: Use $derived for reactive default loading text
  let defaultLoadingText = $derived(loadingText || t('common.loading'));

  // Combined disabled state for both loading and disabled
  let isDisabled = $derived(disabled || loading);
</script>

<button
  type="button"
  class="settings-button {className}"
  class:settings-button--disabled={isDisabled}
  onclick={() => !isDisabled && onclick?.()}
  disabled={isDisabled}
  aria-busy={loading}
>
  {#if loading}
    <div class="loading loading-spinner loading-sm"></div>
    {defaultLoadingText}
  {:else if children}
    {@render children()}
  {/if}
</button>

<style>
  /* Base button styles matching .settings-input-group button */
  .settings-button {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    flex-shrink: 0;
    height: 2rem;
    min-height: 2rem;
    margin-left: 0.5rem;
    margin-right: 0.5rem;
    padding: 0 0.75rem;
    font-size: 0.875rem;
    line-height: 1.25rem;
    font-weight: 600;
    border: none;
    cursor: pointer;
    white-space: nowrap;
    border-radius: var(--rounded-btn, 0.5rem);
    background-color: var(--fallback-p, oklch(var(--p) / 1));
    color: var(--fallback-pc, oklch(var(--pc) / 1));
    transition: background-color 0.2s ease;
  }

  /* Hover state - using CSS instead of JavaScript for better performance */
  .settings-button:hover:not(.settings-button--disabled) {
    background-color: color-mix(in oklab, var(--fallback-p, oklch(var(--p) / 1)) 90%, black);
  }

  /* Disabled state */
  .settings-button--disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  /* Focus visible for accessibility */
  .settings-button:focus-visible {
    outline: 2px solid var(--fallback-p, oklch(var(--p) / 1));
    outline-offset: 2px;
  }
</style>
