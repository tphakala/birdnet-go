<script lang="ts">
  import { onMount } from 'svelte';
  import { theme } from '$lib/stores/theme';
  import { cn } from '$lib/utils/cn';

  interface Props {
    className?: string;
    showTooltip?: boolean;
    size?: 'sm' | 'md' | 'lg';
  }

  let { className = '', showTooltip = true, size = 'sm' }: Props = $props();

  let currentTheme = $state<'light' | 'dark'>('light');
  let mounted = $state(false);

  // Subscribe to theme store
  $effect(() => {
    const unsubscribe = theme.subscribe(value => {
      currentTheme = value;
    });

    return unsubscribe;
  });

  // Initialize theme on mount
  onMount(() => {
    const cleanup = theme.initialize();
    mounted = true;

    return () => {
      if (cleanup) cleanup();
    };
  });

  // Handle toggle
  function handleToggle() {
    theme.toggle();
  }

  // Get icon size classes
  const iconSizeClass = $derived(() => {
    switch (size) {
      case 'lg':
        return 'w-8 h-8';
      case 'md':
        return 'w-7 h-7';
      default:
        return 'w-6 h-6';
    }
  });
</script>

<div class={cn('relative group', !showTooltip && 'md:block', className)}>
  <label class={cn('swap swap-rotate btn btn-ghost p-1', `btn-${size}`)}>
    <input
      type="checkbox"
      class="theme-controller"
      checked={currentTheme === 'dark'}
      onchange={handleToggle}
      aria-label="Toggle dark mode"
    />

    <!-- Sun icon (light mode) -->
    <svg
      class={cn('swap-on fill-current', iconSizeClass())}
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      aria-hidden="true"
    >
      <path
        d="M5.64,17l-.71.71a1,1,0,0,0,0,1.41,1,1,0,0,0,1.41,0l.71-.71A1,1,0,0,0,5.64,17ZM5,12a1,1,0,0,0-1-1H3a1,1,0,0,0,0,2H4A1,1,0,0,0,5,12Zm7-7a1,1,0,0,0,1-1V3a1,1,0,0,0-2,0V4A1,1,0,0,0,12,5ZM5.64,7.05a1,1,0,0,0,.7.29,1,1,0,0,0,.71-.29,1,1,0,0,0,0-1.41l-.71-.71A1,1,0,0,0,4.93,6.34Zm12,.29a1,1,0,0,0,.7-.29l.71-.71a1,1,0,1,0-1.41-1.41L17,5.64a1,1,0,0,0,0,1.41A1,1,0,0,0,17.66,7.34ZM21,11H20a1,1,0,0,0,0,2h1a1,1,0,0,0,0-2Zm-9,8a1,1,0,0,0-1,1v1a1,1,0,0,0,2,0V20A1,1,0,0,0,12,19ZM18.36,17A1,1,0,0,0,17,18.36l.71.71a1,1,0,0,0,1.41,0,1,1,0,0,0,0-1.41ZM12,6.5A5.5,5.5,0,1,0,17.5,12,5.51,5.51,0,0,0,12,6.5Zm0,9A3.5,3.5,0,1,1,15.5,12,3.5,3.5,0,0,1,12,15.5Z"
      />
    </svg>

    <!-- Moon icon (dark mode) -->
    <svg
      class={cn('swap-off fill-current', iconSizeClass())}
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      aria-hidden="true"
    >
      <path
        d="M21.64,13a1,1,0,0,0-1.05-.14,8.05,8.05,0,0,1-3.37.73A8.15,8.15,0,0,1,9.08,5.49a8.59,8.59,0,0,1,.25-2A1,1,0,0,0,8,2.36,10.14,10.14,0,1,0,22,14.05,1,1,0,0,0,21.64,13Zm-9.5,6.69A8.14,8.14,0,0,1,7.08,5.22v.27A10.15,10.15,0,0,0,17.22,15.63a9.79,9.79,0,0,0,2.1-.22A8.11,8.11,0,0,1,12.14,19.73Z"
      />
    </svg>
  </label>

  {#if showTooltip && mounted}
    <div
      class="invisible group-hover:visible absolute left-1/2 transform -translate-x-1/2 mt-2 w-auto whitespace-nowrap bg-gray-900 text-gray-50 text-sm rounded px-2 py-1 z-50 shadow-md"
      role="tooltip"
      aria-hidden="true"
    >
      Switch theme
    </div>
  {/if}
</div>

<style>
  /* Ensure smooth transition between icons */
  .swap-rotate .swap-on,
  .swap-rotate .swap-off {
    transition:
      transform 0.3s ease-in-out,
      opacity 0.3s ease-in-out;
  }
</style>
