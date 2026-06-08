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
  import { confidenceColorClasses } from '$lib/desktop/features/dashboard/utils/confidenceColors';

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
  const colorClasses = $derived(confidenceColorClasses(confidencePercent));
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
