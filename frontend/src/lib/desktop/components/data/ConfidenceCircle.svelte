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

  // Handle both decimal (0-1) and percentage (0-100) formats with validation
  const confidencePercent = $derived(() => {
    // Validate input is a number
    if (typeof confidence !== 'number' || isNaN(confidence)) {
      return 0;
    }

    // Determine if it's decimal (0-1) or percentage (0-100) format
    const isDecimal = confidence <= 1;
    const percent = isDecimal ? confidence * 100 : confidence;
    
    // Clamp to valid range (0-100)
    const clampedPercent = Math.max(0, Math.min(100, percent));
    
    return Math.round(clampedPercent);
  });
  const isMaxConfidence = $derived(confidencePercent === 100);

  function getConfidenceClass(confidence: number): string {
    // Validate input is a number
    if (typeof confidence !== 'number' || isNaN(confidence)) {
      return 'confidence-low';
    }

    // Determine if it's decimal (0-1) or percentage (0-100) format
    const isDecimal = confidence <= 1;
    const percent = isDecimal ? confidence * 100 : confidence;
    
    // Clamp to valid range (0-100)
    const clampedPercent = Math.max(0, Math.min(100, percent));

    if (clampedPercent >= 70) return 'confidence-high';
    if (clampedPercent >= 40) return 'confidence-medium';
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
