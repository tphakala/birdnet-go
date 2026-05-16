<!--
  Button.svelte

  A reusable button component with size and variant support.
  Uses native Tailwind with CSS variables for theming.

  Props:
  - variant: Visual style (default, primary, success, warning, error, ghost)
  - size: Button size (xs, sm, md, lg)
  - disabled: Whether the button is disabled
  - type: HTML button type
  - title: Tooltip text
  - className: Additional CSS classes
  - onclick: Click handler
-->
<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { safeGet } from '$lib/utils/security';
  import type { Snippet } from 'svelte';

  type ButtonVariant = 'default' | 'primary' | 'success' | 'warning' | 'error' | 'ghost';
  type ButtonSize = 'xs' | 'sm' | 'md' | 'lg';

  interface Props {
    variant?: ButtonVariant;
    size?: ButtonSize;
    disabled?: boolean;
    type?: 'button' | 'submit' | 'reset';
    title?: string;
    className?: string;
    onclick?: (_e: MouseEvent) => void;
    children: Snippet;
  }

  let {
    variant = 'default',
    size = 'sm',
    disabled = false,
    type = 'button',
    title,
    className = '',
    onclick,
    children,
  }: Props = $props();

  const sizeClasses: Record<ButtonSize, string> = {
    xs: 'px-2 py-1 text-xs gap-1',
    sm: 'px-3 py-1.5 text-xs gap-1.5',
    md: 'px-4 py-2 text-sm gap-2',
    lg: 'px-5 py-2.5 text-sm gap-2',
  };

  const variantClasses: Record<ButtonVariant, string> = {
    default:
      'bg-[var(--color-base-200)] text-[var(--color-base-content)] border border-[var(--color-base-300)] hover:bg-[var(--color-base-300)] active:bg-[var(--color-base-300)]/80',
    primary:
      'bg-[var(--color-primary)] text-[var(--color-primary-content)] border border-[var(--color-primary)] hover:bg-[var(--color-primary)]/85 active:bg-[var(--color-primary)]/70',
    success:
      'bg-[var(--color-success)]/15 text-[var(--color-success)] border border-[var(--color-success)]/25 hover:bg-[var(--color-success)]/25 active:bg-[var(--color-success)]/35',
    warning:
      'bg-[var(--color-warning)]/15 text-[var(--color-warning)] border border-[var(--color-warning)]/25 hover:bg-[var(--color-warning)]/25 active:bg-[var(--color-warning)]/35',
    error:
      'bg-[var(--color-error)]/15 text-[var(--color-error)] border border-[var(--color-error)]/25 hover:bg-[var(--color-error)]/25 active:bg-[var(--color-error)]/35',
    ghost:
      'bg-transparent text-[var(--color-base-content)] hover:bg-[var(--color-base-200)] active:bg-[var(--color-base-300)]',
  };
</script>

<button
  {type}
  {disabled}
  {title}
  class={cn(
    'inline-flex items-center justify-center rounded-lg font-medium transition-colors',
    'disabled:opacity-50 disabled:cursor-not-allowed disabled:pointer-events-none',
    safeGet(sizeClasses, size, ''),
    safeGet(variantClasses, variant, ''),
    className
  )}
  {onclick}
>
  {@render children()}
</button>
