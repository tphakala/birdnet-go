<!--
  ConfidenceCircle.svelte
  
  A circular visual indicator that displays AI confidence levels for bird detection results.
  Automatically handles both decimal (0-1) and percentage (0-100) confidence values.
  
  Usage:
  - Detection tables to show confidence at a glance
  - Analytics dashboards for confidence visualization
  - Any location where confidence needs visual representation
  
  Props:
  - confidence: number - The confidence value (0-1 or 0-100)
  - size?: 'sm' | 'md' | 'lg' - Size variant
  - className?: string - Additional CSS classes
-->
<script lang="ts">
  import { alertIcons } from '$lib/utils/icons';

  interface Props {
    confidence: number;
    size?: 'sm' | 'md' | 'lg';
    className?: string;
  }

  let { confidence, className = '' }: Props = $props();

  // Helper function to validate and normalize confidence input
  function normalizeConfidence(value: number): number {
    // Validate input is a number
    if (typeof value !== 'number' || isNaN(value)) {
      return 0;
    }

    // Determine if it's decimal (0-1) or percentage (0-100) format
    const isDecimal = value <= 1;
    const percent = isDecimal ? value * 100 : value;

    // Clamp to valid range (0-100) and round
    return Math.round(Math.max(0, Math.min(100, percent)));
  }

  // Handle both decimal (0-1) and percentage (0-100) formats with validation
  const confidencePercent = $derived(() => normalizeConfidence(confidence));
  const isMaxConfidence = $derived(confidencePercent() === 100);

  function getConfidenceClass(confidence: number): string {
    const clampedPercent = normalizeConfidence(confidence);

    if (clampedPercent >= 70) return 'confidence-high';
    if (clampedPercent >= 40) return 'confidence-medium';
    return 'confidence-low';
  }
</script>

<div
  class="confidence-circle {getConfidenceClass(confidence)} {className}"
  style:--progress="{confidencePercent()}%"
>
  <div class="confidence-circle-track"></div>
  <div class="confidence-circle-progress"></div>
  <div class="confidence-circle-text">
    {#if isMaxConfidence}
      <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="3" d={alertIcons.check}></path>
      </svg>
    {:else}
      {confidencePercent()}%
    {/if}
  </div>
</div>
