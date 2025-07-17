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
  
  Props:
  - detection: Detection - The detection object containing verification status
  - className?: string - Additional CSS classes
  
  Status Types:
  - correct: Green badge for verified correct detections
  - false_positive: Red badge for verified false positives
  - unverified: Gray badge for unverified detections
-->
<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Detection } from '$lib/types/detection.types';

  interface Props {
    detection: Detection;
    className?: string;
  }

  let { detection, className = '' }: Props = $props();

  function getStatusBadgeClass(verified: string): string {
    switch (verified) {
      case 'correct':
        return 'status-badge correct';
      case 'false_positive':
        return 'status-badge false';
      default:
        return 'status-badge unverified';
    }
  }

  function getStatusText(verified: string): string {
    switch (verified) {
      case 'correct':
        return 'correct';
      case 'false_positive':
        return 'false';
      default:
        return 'unverified';
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
    <div class="status-badge locked">locked</div>
  {/if}

  <!-- Comments badge -->
  {#if detection.comments && detection.comments.length > 0}
    <div class="status-badge comment">comment</div>
  {/if}
</div>
