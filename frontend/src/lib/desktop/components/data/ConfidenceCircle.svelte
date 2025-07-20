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
  interface Props {
    confidence: number;
    size?: 'sm' | 'md' | 'lg';
    className?: string;
  }

  let { confidence, className = '' }: Props = $props();

  // Handle both decimal (0-1) and percentage (0-100) formats
  const confidencePercent = $derived(
    confidence <= 1 ? Math.round(confidence * 100) : Math.round(confidence)
  );
  const isMaxConfidence = $derived(confidencePercent === 100);

  function getConfidenceClass(confidence: number): string {
    const percent = confidence <= 1 ? confidence * 100 : confidence;
    if (percent >= 70) return 'confidence-high';
    if (percent >= 40) return 'confidence-medium';
    return 'confidence-low';
  }
</script>

<div
  class="confidence-circle {getConfidenceClass(confidence)} {className}"
  style:--progress="{confidencePercent}%"
>
  <div class="confidence-circle-track"></div>
  <div class="confidence-circle-progress"></div>
  <div class="confidence-circle-text">
    {#if isMaxConfidence}
      <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="3" d="M5 13l4 4L19 7"
        ></path>
      </svg>
    {:else}
      {confidencePercent}%
    {/if}
  </div>
</div>
