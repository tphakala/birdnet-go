<!--
  Status Pill Component

  Purpose: Display semantic status indicators with consistent styling.
  Useful for connection states, health indicators, process states, etc.

  Features:
  - Semantic status variants (success, warning, error, info, neutral)
  - Optional leading dot indicator
  - Size variants (xs, sm, md)
  - Supports custom labels or i18n keys
  - The tint, the text token and --status-fill come from the .badge-status-<variant> classes
    (tailwind.css); the border and dot are drawn from --status-fill below. The neutral pill
    overrides them with a quieter recipe. Text is 4.5:1 or more on its tint over
    base-100/200/300 (see color-contrast.test.ts)

  @component
-->
<script lang="ts" module>
  // Export types for external use
  export type StatusVariant = 'success' | 'warning' | 'error' | 'info' | 'neutral';
  export type StatusSize = 'xs' | 'sm' | 'md';
</script>

<script lang="ts">
  import type { Snippet } from 'svelte';
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
    /** Optional leading icon snippet, rendered in place of the dot */
    leadingIcon?: Snippet;
    /** Additional CSS classes */
    className?: string;
  }

  let {
    variant = 'neutral',
    label,
    size = 'sm',
    showDot = true,
    pulse = false,
    leadingIcon,
    className = '',
  }: Props = $props();

  // Shared status recipe from tailwind.css, as literal classes so they stay greppable.
  // Unknown input gets the neutral look.
  const variantClasses: Record<StatusVariant, string> = {
    success: 'badge-status-success',
    warning: 'badge-status-warning',
    error: 'badge-status-error',
    info: 'badge-status-info',
    neutral: 'badge-status-neutral',
  };

  // Size styles for the pill
  const sizeStyles: Record<StatusSize, string> = {
    xs: 'text-[11px] px-2 py-0.5 gap-1',
    sm: 'text-xs px-2.5 py-1 gap-1.5',
    md: 'text-sm px-3 py-1.5 gap-2',
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
    'status-pill inline-flex items-center font-medium rounded-full border',
    safeGet(variantClasses, variant, variantClasses.neutral),
    safeGet(sizeStyles, size, sizeStyles.sm),
    className
  )}
>
  {#if leadingIcon}
    <span class="flex-shrink-0 flex items-center" aria-hidden="true">
      {@render leadingIcon()}
    </span>
  {:else if showDot}
    <span
      class={cn(
        'status-pill-dot rounded-full flex-shrink-0',
        safeGet(dotSizeStyles, size, dotSizeStyles.sm),
        pulse && 'animate-pulse'
      )}
      aria-hidden="true"
    ></span>
  {/if}
  <span class="leading-none">{label}</span>
</span>

<style>
  /* .badge-status-<variant> (tailwind.css) gives the tint, the text colour and --status-fill.
     The pill adds a border and a dot drawn from the same fill. */
  .status-pill {
    border-color: color-mix(in srgb, var(--status-fill) 30%, transparent);
  }

  .status-pill-dot {
    background-color: var(--status-fill);
  }

  /* The neutral pill is quieter than .badge-status-neutral: a lighter tint and border, text at
     70% of base-content and a faded dot. 70% keeps the text at 4.5:1 or more on the tint over
     base-100, base-200 and base-300 (color-contrast.test.ts). */
  .status-pill:global(.badge-status-neutral) {
    background: color-mix(in srgb, var(--color-base-content) 8%, transparent);
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
    border-color: color-mix(in srgb, var(--color-base-content) 15%, transparent);
  }

  .status-pill:global(.badge-status-neutral) .status-pill-dot {
    background-color: color-mix(in srgb, var(--color-base-content) 40%, transparent);
  }
</style>
