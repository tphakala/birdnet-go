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

  // CSS variable names for each variant
  const cssVarNames: Record<StatusVariant, string> = {
    success: '--color-success',
    warning: '--color-warning',
    error: '--color-error',
    info: '--color-info',
    neutral: '--color-base-content',
  };

  // Get inline styles for the pill using CSS color-mix
  const getPillStyles = (variant: StatusVariant) => {
    const varName = safeGet(cssVarNames, variant, '--color-base-content');
    return variant === 'neutral'
      ? `background-color: color-mix(in srgb, var(${varName}) 5%, transparent); ` +
          `color: color-mix(in srgb, var(${varName}) 60%, transparent); ` +
          `border-color: color-mix(in srgb, var(${varName}) 10%, transparent);`
      : `background-color: color-mix(in srgb, var(${varName}) 10%, transparent); ` +
          `color: var(${varName}); ` +
          `border-color: color-mix(in srgb, var(${varName}) 20%, transparent);`;
  };

  // Dot color styles using CSS variables
  const getDotStyles = (variant: StatusVariant) => {
    const varName = safeGet(cssVarNames, variant, '--color-base-content');
    return variant === 'neutral'
      ? `background-color: color-mix(in srgb, var(${varName}) 40%, transparent);`
      : `background-color: var(${varName});`;
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
    safeGet(sizeStyles, size, sizeStyles.sm),
    className
  )}
  style={getPillStyles(variant)}
>
  {#if showDot}
    <span
      class={cn(
        'rounded-full flex-shrink-0',
        safeGet(dotSizeStyles, size, dotSizeStyles.sm),
        pulse && 'animate-pulse'
      )}
      style={getDotStyles(variant)}
      aria-hidden="true"
    ></span>
  {/if}
  <span class="leading-none">{label}</span>
</span>
