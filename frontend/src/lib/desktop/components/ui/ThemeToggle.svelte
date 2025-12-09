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
  const iconSizeClass = $derived.by(() => {
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
  <button
    onclick={handleToggle}
    class={cn('btn btn-ghost p-1', `btn-${size}`)}
    aria-label={currentTheme === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'}
  >
    <div class="theme-icon-container">
      {#if currentTheme === 'dark'}
        <Sun class={iconSizeClass} />
      {:else}
        <Moon class={iconSizeClass} />
      {/if}
    </div>
  </button>

  {#if showTooltip && mounted}
    <div
      class="invisible group-hover:visible absolute left-1/2 transform -translate-x-1/2 mt-2 w-auto whitespace-nowrap bg-neutral text-neutral-content text-sm rounded-md px-2 py-1 z-50 shadow-md"
      role="tooltip"
      aria-hidden="true"
    >
      Switch theme
    </div>
  {/if}
</div>

<style>
  /* Smooth transition for theme icon */
  .theme-icon-container {
    display: flex;
    align-items: center;
    justify-content: center;
    transition: transform 0.3s ease-in-out;
  }

  .theme-icon-container:hover {
    transform: rotate(15deg);
  }
</style>
