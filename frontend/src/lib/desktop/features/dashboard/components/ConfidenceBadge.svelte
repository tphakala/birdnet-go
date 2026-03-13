<!--
  ConfidenceBadge.svelte

  A circular badge displaying confidence level with color coding.
  Designed for overlay on spectrogram cards.

  Color Thresholds (uses CSS variables for theme support):
  - ≥90%: success
  - ≥70%: success/warning blend
  - ≥50%: warning
  - ≥30%: warning/error blend
  - <30%: error

  Props:
  - confidence: number - Confidence value (0-1 or 0-100)
  - className?: string - Additional CSS classes
-->
<script lang="ts">
  import { cn } from '$lib/utils/cn';

  interface Props {
    confidence: number;
    className?: string;
  }

  let { confidence, className = '' }: Props = $props();

  // Normalize confidence to percentage
  function normalizeConfidence(value: number): number {
    if (typeof value !== 'number' || isNaN(value)) return 0;
    const isDecimal = value <= 1;
    const percent = isDecimal ? value * 100 : value;
    return Math.round(Math.max(0, Math.min(100, percent)));
  }

  const confidencePercent = $derived(normalizeConfidence(confidence));

  // Get color classes based on confidence level
  function getColorClasses(percent: number): string {
    if (percent >= 90) return 'bg-[var(--color-success)] text-[var(--color-success-content)]';
    if (percent >= 70)
      return 'bg-[color-mix(in_srgb,var(--color-success)_80%,var(--color-warning))] text-white';
    if (percent >= 50) return 'bg-[var(--color-warning)] text-[var(--color-warning-content)]';
    if (percent >= 30)
      return 'bg-[color-mix(in_srgb,var(--color-warning)_60%,var(--color-error))] text-white';
    return 'bg-[var(--color-error)] text-[var(--color-error-content)]';
  }

  const colorClasses = $derived(getColorClasses(confidencePercent));
</script>

<div
  class={cn('confidence-badge', colorClasses, className)}
  title="Confidence: {confidencePercent}%"
  aria-label="Confidence: {confidencePercent}%"
>
  {confidencePercent}%
</div>

<style>
  .confidence-badge {
    display: flex;
    align-items: center;
    justify-content: center;
    min-width: 2.5rem;
    height: 1.5rem;
    padding: 0 0.5rem;
    border-radius: 9999px;
    font-size: 0.75rem;
    font-weight: 600;
    box-shadow: 0 2px 4px rgb(0 0 0 / 0.2);
  }
</style>
