<script lang="ts">
  import { onMount } from 'svelte';
  import { theme } from '$lib/stores/theme';
  import { cn } from '$lib/utils/cn';
  import { Sun, Moon } from '@lucide/svelte';

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
        return 'size-8';
      case 'md':
        return 'size-7';
      default:
        return 'size-6';
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
    <div class="swap-on">
      <Sun class={iconSizeClass()} />
    </div>

    <!-- Moon icon (dark mode) -->
    <div class="swap-off">
      <Moon class={iconSizeClass()} />
    </div>
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
