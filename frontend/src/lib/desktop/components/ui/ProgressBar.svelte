<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { HTMLAttributes } from 'svelte/elements';

  type ProgressSize = 'xs' | 'sm' | 'md' | 'lg';
  type ProgressVariant =
    | 'primary'
    | 'secondary'
    | 'accent'
    | 'info'
    | 'success'
    | 'warning'
    | 'error';

  interface ColorThreshold {
    value: number;
    variant: ProgressVariant;
  }

  interface Props extends HTMLAttributes<HTMLDivElement> {
    value: number;
    max?: number;
    size?: ProgressSize;
    variant?: ProgressVariant;
    showLabel?: boolean;
    labelFormat?: (_value: number, _max: number) => string;
    colorThresholds?: ColorThreshold[];
    striped?: boolean;
    animated?: boolean;
    className?: string;
    barClassName?: string;
  }

  let {
    value = 0,
    max = 100,
    size = 'md',
    variant = 'primary',
    showLabel = false,
    labelFormat = (val, maxVal) => `${Math.round((val / maxVal) * 100)}%`,
    colorThresholds = [],
    striped = false,
    animated = false,
    className = '',
    barClassName = '',
    ...rest
  }: Props = $props();

  // Calculate percentage
  let percentage = $derived(Math.min(Math.max((value / max) * 100, 0), 100));

  // Determine variant based on thresholds
  let currentVariant = $derived(() => {
    if (colorThresholds.length === 0) return variant;

    const percentValue = (value / max) * 100;
    let selectedVariant = variant;

    // Sort thresholds by value ascending
    const sortedThresholds = [...colorThresholds].sort((a, b) => a.value - b.value);

    for (const threshold of sortedThresholds) {
      if (percentValue >= threshold.value) {
        selectedVariant = threshold.variant;
      }
    }

    return selectedVariant;
  });

  const sizeClasses: Record<ProgressSize, string> = {
    xs: 'h-1',
    sm: 'h-2',
    md: 'h-4',
    lg: 'h-6',
  };

  const variantClasses: Record<ProgressVariant, string> = {
    primary: 'bg-primary',
    secondary: 'bg-secondary',
    accent: 'bg-accent',
    info: 'bg-info',
    success: 'bg-success',
    warning: 'bg-warning',
    error: 'bg-error',
  };

  const containerClasses = cn(
    'w-full bg-base-300 rounded-full overflow-hidden relative',
    sizeClasses[size],
    className
  );

  let progressBarClasses = $derived(
    cn(
      'h-full transition-all duration-300 ease-out',
      variantClasses[currentVariant()],
      {
        'bg-stripes': striped,
        'animate-stripes': striped && animated,
      },
      barClassName
    )
  );

  let labelClasses = $derived(
    cn('absolute inset-0 flex items-center justify-center text-xs font-medium', {
      'text-white mix-blend-difference': percentage > 50,
      'text-base-content': percentage <= 50,
    })
  );
</script>

<div
  class={containerClasses}
  role="progressbar"
  aria-valuenow={value}
  aria-valuemin={0}
  aria-valuemax={max}
  aria-label={showLabel ? labelFormat(value, max) : undefined}
  {...rest}
>
  <div class={progressBarClasses} style:width="{percentage}%"></div>
  {#if showLabel}
    <div class={labelClasses}>
      {labelFormat(value, max)}
    </div>
  {/if}
</div>

<style>
  .bg-stripes {
    background-image: linear-gradient(
      45deg,
      rgb(255, 255, 255, 0.15) 25%,
      transparent 25%,
      transparent 50%,
      rgb(255, 255, 255, 0.15) 50%,
      rgb(255, 255, 255, 0.15) 75%,
      transparent 75%,
      transparent
    );
    background-size: 1rem 1rem;
  }

  @keyframes stripes {
    0% {
      background-position: 0 0;
    }

    100% {
      background-position: 1rem 0;
    }
  }

  .animate-stripes {
    animation: stripes 1s linear infinite;
  }
</style>
