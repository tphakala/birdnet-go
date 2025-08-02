<!--
  StatsCard.svelte
  
  A versatile card component for displaying key statistics and metrics with optional trend indicators.
  Supports multiple visual variants and customizable icons for different statistical contexts.
  
  Usage:
  - Dashboard summary cards
  - Analytics metrics display
  - System performance indicators
  - Key performance indicators (KPIs)
  - Statistical summaries
  
  Features:
  - Multiple color variants (primary, secondary, accent, etc.)
  - Trend indicators with direction and values
  - Custom icon support via snippets
  - Loading state with skeleton animation
  - Responsive design
  - Flexible typography options
  
  Props:
  - title: string - The statistic title/label
  - value: string | number - The main statistical value
  - subtitle?: string - Optional subtitle text
  - trend?: TrendData - Trend information with direction and value
  - variant?: CardVariant - Visual style variant
  - icon?: Snippet - Custom icon snippet
  - loading?: boolean - Loading state indicator
  - className?: string - Additional CSS classes
-->
<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Snippet } from 'svelte';
  import { iconPaths } from '$lib/utils/icons';
  import { safeGet } from '$lib/utils/security';

  type TrendDirection = 'up' | 'down' | 'neutral';
  type CardVariant =
    | 'default'
    | 'primary'
    | 'secondary'
    | 'accent'
    | 'info'
    | 'success'
    | 'warning'
    | 'error';

  interface TrendData {
    direction: TrendDirection;
    value: string | number;
    label?: string;
  }

  interface Props {
    value: string | number;
    label: string;
    subLabel?: string;
    icon?: Snippet;
    trend?: TrendData;
    variant?: CardVariant;
    loading?: boolean;
    href?: string;
    onClick?: () => void;
    className?: string;
    id?: string;
    'data-testid'?: string;
  }

  let {
    value,
    label,
    subLabel,
    icon,
    trend,
    variant = 'default',
    loading = false,
    href,
    onClick,
    className = '',
    id,
    'data-testid': dataTestId,
  }: Props = $props();

  const variantClasses: Record<CardVariant, string> = {
    default: 'bg-base-100',
    primary: 'bg-primary text-primary-content',
    secondary: 'bg-secondary text-secondary-content',
    accent: 'bg-accent text-accent-content',
    info: 'bg-info text-info-content',
    success: 'bg-success text-success-content',
    warning: 'bg-warning text-warning-content',
    error: 'bg-error text-error-content',
  };

  const iconVariantClasses: Record<CardVariant, string> = {
    default: 'bg-base-200 text-base-content',
    primary: 'bg-primary-focus text-primary-content',
    secondary: 'bg-secondary-focus text-secondary-content',
    accent: 'bg-accent-focus text-accent-content',
    info: 'bg-info-content/20 text-info-content',
    success: 'bg-success-content/20 text-success-content',
    warning: 'bg-warning-content/20 text-warning-content',
    error: 'bg-error-content/20 text-error-content',
  };

  const trendClasses: Record<TrendDirection, string> = {
    up: 'text-success',
    down: 'text-error',
    neutral: 'text-base-content/70',
  };

  const cardClasses = cn(
    'card shadow-lg transition-all duration-200',
    safeGet(variantClasses, variant, ''),
    {
      'hover:shadow-xl hover:scale-105 cursor-pointer': !!(href || onClick),
    },
    className
  );

  const isInteractive = href || onClick;

  function handleClick() {
    if (onClick) {
      onClick();
    }
  }

  function renderTrendIcon(direction: TrendDirection) {
    switch (direction) {
      case 'up':
        return iconPaths.trendUp;
      case 'down':
        return iconPaths.trendDown;
      default:
        return iconPaths.trendNeutral;
    }
  }
</script>

{#snippet cardBody()}
  <div class="card-body">
    <div class="flex items-start justify-between">
      <div class="flex-1">
        {#if loading}
          <div class="space-y-2">
            <div class="skeleton h-8 w-24"></div>
            <div class="skeleton h-4 w-32"></div>
            {#if subLabel}
              <div class="skeleton h-3 w-28"></div>
            {/if}
          </div>
        {:else}
          <div class="text-3xl font-bold">{value}</div>
          <div class="text-sm font-medium opacity-90">{label}</div>
          {#if subLabel}
            <div class="text-xs opacity-70 mt-1">{subLabel}</div>
          {/if}
          {#if trend}
            <div class="flex items-center gap-1 mt-2">
              <svg
                class={cn('w-4 h-4', trendClasses[trend.direction])}
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  stroke-width="2"
                  d={renderTrendIcon(trend.direction)}
                />
              </svg>
              <span class={cn('text-sm font-medium', trendClasses[trend.direction])}>
                {trend.value}{trend.label ? ` ${trend.label}` : ''}
              </span>
            </div>
          {/if}
        {/if}
      </div>
      {#if icon && !loading}
        <div class={cn('p-3 rounded-lg', safeGet(iconVariantClasses, variant, ''))}>
          {@render icon()}
        </div>
      {/if}
    </div>
  </div>
{/snippet}

{#if href}
  <a {href} class={cardClasses} {id} data-testid={dataTestId}>
    {@render cardBody()}
  </a>
{:else if isInteractive}
  <button type="button" class={cardClasses} onclick={handleClick} {id} data-testid={dataTestId}>
    {@render cardBody()}
  </button>
{:else}
  <div class={cardClasses} {id} data-testid={dataTestId}>
    {@render cardBody()}
  </div>
{/if}
