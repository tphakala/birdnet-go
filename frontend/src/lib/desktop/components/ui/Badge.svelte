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

  const variantClasses = $derived<Record<BadgeVariant, string>>({
    primary: outline ? 'badge-primary badge-outline' : 'badge-primary',
    secondary: outline ? 'badge-secondary badge-outline' : 'badge-secondary',
    accent: outline ? 'badge-accent badge-outline' : 'badge-accent',
    neutral: outline ? 'badge-outline' : 'badge',
    info: outline ? 'badge-info badge-outline' : 'badge-info',
    success: outline ? 'badge-success badge-outline' : 'badge-success',
    warning: outline ? 'badge-warning badge-outline' : 'badge-warning',
    error: outline ? 'badge-error badge-outline' : 'badge-error',
    ghost: 'badge-ghost',
  });

  const sizeClasses: Record<BadgeSize, string> = {
    xs: 'badge-xs',
    sm: 'badge-sm',
    md: '',
    lg: 'badge-lg',
  };
</script>

<span
  class={cn(
    'badge',
    safeGet(variantClasses, variant, 'badge'),
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
