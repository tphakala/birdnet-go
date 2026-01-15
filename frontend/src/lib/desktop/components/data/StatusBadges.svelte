<!--
  StatusBadges.svelte
  
  A component that displays verification status badges for bird detection records.
  Provides visual indicators for detection verification states (correct, false positive, unverified).
  
  Usage:
  - Detection rows and tables
  - Detection detail views
  - Administrative detection management
  - Review interfaces
  
  Features:
  - Color-coded status badges
  - Handles multiple verification states
  - Consistent styling with design system
  - Responsive display
  - Size variants for different contexts
  
  Props:
  - detection: Detection - The detection object containing verification status
  - className?: string - Additional CSS classes
  - size?: 'sm' | 'md' | 'lg' - Badge size variant (default: 'md')
  
  Status Types:
  - correct: Green badge for verified correct detections
  - false_positive: Red badge for verified false positives
  - unverified: Gray badge for unverified detections
-->
<script module lang="ts">
  // Module-level constants (shared across all instances)
  const sizeClasses: Record<string, string> = {
    sm: 'status-badge-sm',
    md: 'status-badge-md',
    lg: 'status-badge-lg',
  };

  const statusBadgeClassMap: Record<string, string> = {
    correct: 'status-badge correct',
    false_positive: 'status-badge false-positive',
  };

  const DEFAULT_STATUS_BADGE_CLASS = 'status-badge unverified';
</script>

<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { t } from '$lib/i18n';
  import type { Detection } from '$lib/types/detection.types';
  import { safeGet } from '$lib/utils/security';

  type Size = 'sm' | 'md' | 'lg';
  type VerificationStatus = Detection['verified'];

  interface Props {
    detection: Detection;
    className?: string;
    size?: Size;
  }

  let { detection, className = '', size = 'md' }: Props = $props();

  // Derive size class once to avoid duplication
  const sizeClass = $derived(safeGet(sizeClasses, size, ''));

  function getStatusBadgeClass(verified: VerificationStatus): string {
    const baseClass = safeGet(statusBadgeClassMap, verified, DEFAULT_STATUS_BADGE_CLASS);
    return cn(baseClass, sizeClass);
  }

  function getStatusText(verified: VerificationStatus): string {
    switch (verified) {
      case 'correct':
        return t('common.review.status.verifiedCorrect');
      case 'false_positive':
        return t('common.review.status.falsePositive');
      default:
        return t('common.review.status.notReviewed');
    }
  }
</script>

<div class={cn('flex flex-wrap gap-1', className)}>
  <!-- Verification status badge -->
  <div class={getStatusBadgeClass(detection.verified)}>
    {getStatusText(detection.verified)}
  </div>

  <!-- Locked badge -->
  {#if detection.locked}
    <div class={cn('status-badge locked', sizeClass)}>
      {t('common.review.status.locked')}
    </div>
  {/if}

  <!-- Comments badge -->
  {#if detection.comments && detection.comments.length > 0}
    <div class={cn('status-badge comment', sizeClass)}>
      {t('common.review.comment')}
    </div>
  {/if}
</div>

<style>
  .status-badge {
    /* Default badge color (unverified) */
    --badge-color: var(--color-secondary);

    display: inline-flex;
    align-items: center;
    justify-content: center;
    padding: 0.25rem 0.75rem;
    border-radius: 9999px;
    font-size: 0.75rem;
    font-weight: 500;
    white-space: nowrap;
    border: 1px solid;

    /* Apply badge color consistently */
    color: var(--badge-color);
    border-color: var(--badge-color);
    background-color: color-mix(in srgb, var(--badge-color) 10%, transparent);
  }

  /* Size variants */
  .status-badge-sm {
    padding: 0.125rem 0.5rem;
    font-size: 0.625rem; /* 10px - smaller for compact contexts */
  }

  .status-badge-md {
    padding: 0.25rem 0.75rem;
    font-size: 0.75rem; /* 12px - default size */
  }

  .status-badge-lg {
    padding: 0.5rem 1rem;
    font-size: 0.875rem; /* 14px - larger for emphasis */
  }

  /* Status-specific badge colors */
  .status-badge.correct {
    --badge-color: var(--color-success);
  }

  .status-badge.false-positive {
    --badge-color: var(--color-error);
  }

  .status-badge.locked {
    --badge-color: var(--color-warning);
  }

  .status-badge.comment {
    --badge-color: var(--color-primary);
  }
</style>
