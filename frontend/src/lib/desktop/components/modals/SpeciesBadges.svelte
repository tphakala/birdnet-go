<!--
  SpeciesBadges.svelte
  
  Reusable component for displaying species status and lock badges.
  Extracted from ReviewModal to reduce duplication and improve maintainability.
  
  Features:
  - Verification status badge (correct/false positive/not reviewed)
  - Lock status badge (when detection is locked)
  - Responsive sizing
  - Proper internationalization
  
  Props:
  - detection: Detection object with review and lock status
  - size?: 'sm' | 'md' | 'lg' - Badge size variant
  - className?: string - Additional CSS classes
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import { CircleCheck, X } from '@lucide/svelte';
  import type { Detection } from '$lib/types/detection.types';

  interface Props {
    detection: Detection;
    size?: 'sm' | 'md' | 'lg';
    className?: string;
  }

  let { detection, size = 'md', className = '' }: Props = $props();

  function getStatusBadgeClass(verified?: string): string {
    switch (verified) {
      case 'correct':
        return 'badge-success';
      case 'false_positive':
        return 'badge-error';
      default:
        return 'badge-ghost';
    }
  }

  function getStatusText(verified?: string): string {
    switch (verified) {
      case 'correct':
        return t('common.review.status.verifiedCorrect');
      case 'false_positive':
        return t('common.review.status.falsePositive');
      default:
        return t('common.review.status.notReviewed');
    }
  }

  // Size classes for responsive badges
  const badgeSize = $derived.by(() => {
    switch (size) {
      case 'sm':
        return 'badge-sm';
      case 'lg':
        return 'badge-lg';
      default:
        return 'badge-md';
    }
  });

  const iconSize = $derived.by(() => {
    switch (size) {
      case 'sm':
        return 'w-2 h-2';
      case 'lg':
        return 'w-4 h-4';
      default:
        return 'w-3 h-3';
    }
  });

  const gapSize = $derived.by(() => {
    switch (size) {
      case 'sm':
        return 'gap-1';
      case 'lg':
        return 'gap-3';
      default:
        return 'gap-2';
    }
  });
</script>

<div class={`flex items-center gap-2 flex-wrap ${className}`}>
  <!-- Verification Status Badge -->
  <span class={`badge ${badgeSize} ${gapSize} ${getStatusBadgeClass(detection.verified)}`}>
    {#if detection.verified === 'correct'}
      <CircleCheck class={iconSize} />
    {:else if detection.verified === 'false_positive'}
      <X class={iconSize} />
    {/if}
    {getStatusText(detection.verified)}
  </span>

  <!-- Lock Status Badge -->
  {#if detection.locked}
    <span class={`badge ${badgeSize} badge-warning gap-1`}>
      <svg
        xmlns="http://www.w3.org/2000/svg"
        fill="none"
        viewBox="0 0 24 24"
        stroke-width="2"
        stroke="currentColor"
        class={iconSize}
      >
        <path
          stroke-linecap="round"
          stroke-linejoin="round"
          d="M16.5 10.5V6.75a4.5 4.5 0 10-9 0v3.75m-.75 11.25h10.5a2.25 2.25 0 002.25-2.25v-6.75a2.25 2.25 0 00-2.25-2.25H6.75a2.25 2.25 0 00-2.25 2.25v6.75a2.25 2.25 0 002.25 2.25z"
        />
      </svg>
      {t('detections.status.locked')}
    </span>
  {/if}
</div>
