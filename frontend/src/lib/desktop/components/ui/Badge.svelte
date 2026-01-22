<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Snippet } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';
  import { safeGet } from '$lib/utils/security';

  type BadgeVariant =
    | 'primary'
    | 'secondary'
    | 'accent'
    | 'neutral'
    | 'info'
    | 'success'
    | 'warning'
    | 'error'
    | 'ghost';

  type BadgeSize = 'xs' | 'sm' | 'md' | 'lg';

  interface Props extends HTMLAttributes<HTMLElement> {
    variant?: BadgeVariant;
    size?: BadgeSize;
    text?: string;
    outline?: boolean;
    className?: string;
    children?: Snippet;
  }

  let {
    variant = 'neutral',
    size = 'md',
    text = '',
    outline = false,
    className = '',
    children,
    ...rest
  }: Props = $props();

  // Base badge styles using native Tailwind
  const baseClasses =
    'inline-flex items-center justify-center font-medium rounded-full whitespace-nowrap';

  // Variant classes using CSS custom properties for theme support
  const variantClasses = $derived<Record<BadgeVariant, string>>({
    primary: outline
      ? 'bg-transparent border border-[var(--color-primary)] text-[var(--color-primary)]'
      : 'bg-[var(--color-primary)] text-[var(--color-primary-content)]',
    secondary: outline
      ? 'bg-transparent border border-[var(--color-secondary)] text-[var(--color-secondary)]'
      : 'bg-[var(--color-secondary)] text-[var(--color-secondary-content)]',
    accent: outline
      ? 'bg-transparent border border-[var(--color-accent)] text-[var(--color-accent)]'
      : 'bg-[var(--color-accent)] text-[var(--color-accent-content)]',
    neutral: outline
      ? 'bg-transparent border border-current'
      : 'bg-[var(--color-base-300)] text-[var(--color-base-content)]',
    info: outline
      ? 'bg-transparent border border-[var(--color-info)] text-[var(--color-info)]'
      : 'bg-[var(--color-info)] text-[var(--color-info-content)]',
    success: outline
      ? 'bg-transparent border border-[var(--color-success)] text-[var(--color-success)]'
      : 'bg-[var(--color-success)] text-[var(--color-success-content)]',
    warning: outline
      ? 'bg-transparent border border-[var(--color-warning)] text-[var(--color-warning)]'
      : 'bg-[var(--color-warning)] text-[var(--color-warning-content)]',
    error: outline
      ? 'bg-transparent border border-[var(--color-error)] text-[var(--color-error)]'
      : 'bg-[var(--color-error)] text-[var(--color-error-content)]',
    ghost: 'bg-black/5 dark:bg-white/5 text-[var(--color-base-content)]',
  });

  // Size classes using native Tailwind
  const sizeClasses: Record<BadgeSize, string> = {
    xs: 'px-1 text-[0.625rem] leading-[0.875rem]',
    sm: 'px-1.5 py-px text-[0.6875rem] leading-[0.9375rem]',
    md: 'px-2 py-0.5 text-xs leading-4',
    lg: 'px-2.5 py-1 text-sm leading-[1.125rem]',
  };
</script>

<span
  class={cn(
    baseClasses,
    safeGet(variantClasses, variant, ''),
    safeGet(sizeClasses, size, ''),
    className
  )}
  {...rest}
>
  {#if children}
    {@render children()}
  {:else}
    {text}
  {/if}
</span>
