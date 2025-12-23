<!--
  AnimatedCounter Component

  A YouTube-style animated counter that slides new values up from below
  when the count increases. Designed for use in heatmap cells where
  real-time SSE updates need visual feedback.

  Features:
  - Smooth slide-up animation when value changes
  - Configurable animation duration and easing
  - Respects prefers-reduced-motion preference
  - Clips animation within container bounds

  Props:
  - value: The number to display
  - duration: Animation duration in ms (default: 300)
-->

<script lang="ts">
  import { onMount } from 'svelte';
  import { fly } from 'svelte/transition';
  import { cubicOut } from 'svelte/easing';

  interface Props {
    value: number;
    duration?: number;
  }

  let { value, duration = 300 }: Props = $props();

  // Check for reduced motion preference - reactive to runtime changes
  let prefersReducedMotion = $state(false);

  onMount(() => {
    const mediaQuery = window.matchMedia('(prefers-reduced-motion: reduce)');
    prefersReducedMotion = mediaQuery.matches;

    const handleChange = () => {
      prefersReducedMotion = mediaQuery.matches;
    };

    mediaQuery.addEventListener('change', handleChange);
    return () => mediaQuery.removeEventListener('change', handleChange);
  });

  // Animation parameters - slide up from below
  const animationParams = $derived({
    y: prefersReducedMotion ? 0 : 20,
    duration: prefersReducedMotion ? 0 : duration,
    easing: cubicOut,
  });
</script>

<span class="counter-wrapper">
  {#key value}
    <span
      class="counter-value"
      in:fly={animationParams}
      out:fly={{ ...animationParams, y: prefersReducedMotion ? 0 : -20 }}
    >
      {value}
    </span>
  {/key}
</span>

<style>
  .counter-wrapper {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    overflow: hidden;
    position: relative;
    width: 100%;
    height: 100%;
  }

  .counter-value {
    /* Absolute positioning so old and new values stack during transition */
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
  }
</style>
