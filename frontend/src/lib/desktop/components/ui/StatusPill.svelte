<!--
  Status Pill Component

  Purpose: Display semantic status indicators with consistent styling.
  Useful for connection states, health indicators, process states, etc.

  Features:
  - Semantic status variants (success, warning, error, info, neutral)
  - Optional leading dot indicator
  - Size variants (xs, sm, md)
  - Supports custom labels or i18n keys
  - Accessible with proper color contrast

  @component
-->
<script lang="ts" module>
  // Export types for external use
  export type StatusVariant = 'success' | 'warning' | 'error' | 'info' | 'neutral';
  export type StatusSize = 'xs' | 'sm' | 'md';
</script>

<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { safeGet } from '$lib/utils/security';

  interface Props {
    /** The semantic status variant */
    variant?: StatusVariant;
    /** The label text to display */
    label: string;
    /** Size of the pill */
    size?: StatusSize;
    /** Show a leading dot indicator */
    showDot?: boolean;
    /** Pulse animation for the dot (useful for connecting/loading states) */
    pulse?: boolean;
    /** Additional CSS classes */
    className?: string;
  }

  let {
    variant = 'neutral',
    label,
    size = 'sm',
    showDot = true,
    pulse = false,
    className = '',
  }: Props = $props();

  // Variant styles for the pill container
  const variantStyles: Record<StatusVariant, string> = {
    success: 'bg-success/10 text-success border-success/20',
    warning: 'bg-warning/10 text-warning border-warning/20',
    error: 'bg-error/10 text-error border-error/20',
    info: 'bg-info/10 text-info border-info/20',
    neutral: 'bg-base-content/5 text-base-content/60 border-base-content/10',
  };

  // Dot color styles
  const dotStyles: Record<StatusVariant, string> = {
    success: 'bg-success',
    warning: 'bg-warning',
    error: 'bg-error',
    info: 'bg-info',
    neutral: 'bg-base-content/40',
  };

  // Size styles for the pill
  const sizeStyles: Record<StatusSize, string> = {
    xs: 'text-[10px] px-1.5 py-0.5 gap-1',
    sm: 'text-xs px-2 py-1 gap-1.5',
    md: 'text-sm px-2.5 py-1.5 gap-2',
  };

  // Dot sizes
  const dotSizeStyles: Record<StatusSize, string> = {
    xs: 'size-1.5',
    sm: 'size-2',
    md: 'size-2.5',
  };
</script>

<span
  class={cn(
    'inline-flex items-center font-medium rounded-full border',
    safeGet(variantStyles, variant, variantStyles.neutral),
    safeGet(sizeStyles, size, sizeStyles.sm),
    className
  )}
>
  {#if showDot}
    <span
      class={cn(
        'rounded-full flex-shrink-0',
        safeGet(dotStyles, variant, dotStyles.neutral),
        safeGet(dotSizeStyles, size, dotSizeStyles.sm),
        pulse && 'animate-pulse'
      )}
      aria-hidden="true"
    ></span>
  {/if}
  <span class="leading-none">{label}</span>
</span>
