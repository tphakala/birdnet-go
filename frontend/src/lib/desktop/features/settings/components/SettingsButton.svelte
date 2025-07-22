<!--
  SettingsButton Component
  
  A reusable button component that matches the .settings-input-group button styles
  from custom.css. Used for action buttons in settings forms like "Test Connection".
  
  Features:
  - Matches exact styling from custom.css (.settings-input-group button)
  - Auto-sizing width to fit content
  - Loading state support with spinner
  - Disabled state handling
  - CSS variable-based colors for theme compatibility
  - Hover effects matching original design
  
  Props:
  - onclick: Click handler function
  - disabled: Whether button is disabled
  - loading: Whether to show loading spinner
  - loadingText: Text to show when loading (default: "Loading...")
  - className: Additional CSS classes
-->
<script lang="ts">
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
    loadingText = 'Loading...',
    className = '',
    children,
  }: Props = $props();
</script>

<button
  type="button"
  class="settings-input-group-button flex-shrink-0 h-8 min-h-8 ml-2 mr-2 px-3 text-sm leading-5 font-semibold border-none cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed whitespace-nowrap {className}"
  style:border-radius="var(--rounded-btn, 0.5rem)"
  style:background-color="var(--fallback-p, oklch(var(--p)/1))"
  style:color="var(--fallback-pc, oklch(var(--pc)/1))"
  onmouseenter={e =>
    !disabled &&
    !loading &&
    (e.currentTarget.style.backgroundColor =
      'color-mix(in oklab, var(--fallback-p, oklch(var(--p)/1)) 90%, black)')}
  onmouseleave={e =>
    !disabled &&
    !loading &&
    (e.currentTarget.style.backgroundColor = 'var(--fallback-p, oklch(var(--p)/1))')}
  onclick={() => onclick && onclick()}
  {disabled}
>
  {#if loading}
    <div class="loading loading-spinner loading-sm"></div>
    {loadingText}
  {:else if children}
    {@render children()}
  {/if}
</button>
