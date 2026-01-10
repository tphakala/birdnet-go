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
  - size?: 'sm' | 'md' | 'lg' | 'xl' - Size variant
  - className?: string - Additional CSS classes
-->
<script lang="ts">
  import { Check } from '@lucide/svelte';
  import { safeGet } from '$lib/utils/security';

  interface Props {
    confidence: number;
    size?: 'sm' | 'md' | 'lg' | 'xl';
    className?: string;
  }

  let { confidence, size = 'md', className = '' }: Props = $props();

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
  const confidencePercent = $derived.by(() => normalizeConfidence(confidence));

  function getConfidenceClass(confidence: number): string {
    const clampedPercent = normalizeConfidence(confidence);

    if (clampedPercent >= 70) return 'confidence-high';
    if (clampedPercent >= 40) return 'confidence-medium';
    return 'confidence-low';
  }

  // Size configuration
  const sizeConfig = {
    sm: { size: 32, track: 4, fontSize: '0.625rem', iconSize: 12 },
    md: { size: 42, track: 5, fontSize: '0.75rem', iconSize: 16 },
    lg: { size: 56, track: 6, fontSize: '0.875rem', iconSize: 20 },
    xl: { size: 72, track: 8, fontSize: '1rem', iconSize: 24 },
  };

  const config = $derived(safeGet(sizeConfig, size, sizeConfig.md));
</script>

<div
  class="confidence-circle {getConfidenceClass(confidence)} {className}"
  style:--progress="{confidencePercent}%"
  style:width="{config.size}px"
  style:height="{config.size}px"
  style:min-width="{config.size}px"
  style:min-height="{config.size}px"
  style:font-size={config.fontSize}
>
  <div
    class="confidence-circle-track"
    style:top="{config.track}px"
    style:left="{config.track}px"
    style:right="{config.track}px"
    style:bottom="{config.track}px"
  ></div>
  <div class="confidence-circle-progress"></div>
  <div class="confidence-circle-text" style:font-size={config.fontSize}>
    {confidencePercent}%
  </div>
</div>

<style>
  .confidence-circle {
    position: relative;
    flex-shrink: 0;
    border-radius: 50%;
    background: var(--lighter-color, #f3f4f6);
  }

  :global([data-theme='dark']) .confidence-circle {
    background: var(--darker-color, rgb(17 24 39));
  }

  .confidence-circle-track {
    position: absolute;
    background: var(--lighter-color, #f3f4f6);
    border-radius: 50%;
    z-index: 2;
  }

  :global([data-theme='dark']) .confidence-circle-track {
    background: var(--darker-color, rgb(17 24 39));
  }

  .confidence-circle-progress {
    position: absolute;
    inset: 0;
    border-radius: 50%;
    transform-origin: center;
    transform: rotate(180deg);
    background: conic-gradient(currentcolor var(--progress), transparent 0);
    transition: all 0.3s ease;
    z-index: 1;
  }

  .confidence-circle-text {
    position: absolute;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    font-weight: 600;
    color: currentcolor;
    z-index: 3;
    white-space: nowrap;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 100%;
    height: 100%;
  }

  /* Confidence level color schemes */
  .confidence-circle :global(.confidence-high) {
    color: #059669;

    --lighter-color: #ecfdf5;
    --darker-color: rgb(6 78 59 / 0.2);
  }

  .confidence-circle :global(.confidence-medium) {
    color: #d97706;

    --lighter-color: #fffbeb;
    --darker-color: rgb(120 53 15 / 0.2);
  }

  .confidence-circle :global(.confidence-low) {
    color: #dc2626;

    --lighter-color: #fef2f2;
    --darker-color: rgb(127 29 29 / 0.2);
  }

  /* Dark theme adjustments */
  :global([data-theme='dark']) .confidence-circle :global(.confidence-high) {
    color: #34d399;

    --darker-color: rgb(6 78 59);
  }

  :global([data-theme='dark']) .confidence-circle :global(.confidence-medium) {
    color: #fbbf24;

    --darker-color: rgb(120 53 15);
  }

  :global([data-theme='dark']) .confidence-circle :global(.confidence-low) {
    color: #f87171;

    --darker-color: rgb(127 29 29);
  }
</style>
