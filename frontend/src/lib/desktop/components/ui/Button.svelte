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
  - ref: Bindable reference to the rendered <button> element (bind:ref), for focus management

  Behaviour:
  - Draws a 2px primary outline on keyboard focus (focus-visible).
  - aria-disabled="true" dims the button and shows a not-allowed cursor but leaves it
    focusable and clickable, so a blocked action can keep a reason reachable through
    aria-describedby; the caller must ignore the click. Native `disabled` also blocks
    pointer events.
-->
<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { safeGet } from '$lib/utils/security';
  import type { Snippet } from 'svelte';
  import type { HTMLButtonAttributes } from 'svelte/elements';

  type ButtonVariant = 'default' | 'primary' | 'success' | 'warning' | 'error' | 'ghost';
  type ButtonSize = 'xs' | 'sm' | 'md' | 'lg';

  interface Props extends Omit<HTMLButtonAttributes, 'class'> {
    /** Merged with the button's own classes, like className. */
    class?: string;
    variant?: ButtonVariant;
    size?: ButtonSize;
    className?: string;
    ref?: HTMLButtonElement;
    children: Snippet;
  }

  let {
    variant = 'default',
    size = 'sm',
    disabled = false,
    type = 'button',
    title,
    className = '',
    class: classProp,
    ref = $bindable(),
    onclick,
    children,
    ...rest
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
      'bg-[var(--color-primary)] text-[var(--color-primary-content)] border border-[var(--color-primary)] hover:bg-[var(--color-primary-hover)] hover:border-[var(--color-primary-hover)] active:bg-[var(--color-primary-hover)] active:border-[var(--color-primary-hover)]',
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
  bind:this={ref}
  {type}
  {disabled}
  {title}
  {...rest}
  class={cn(
    'inline-flex items-center justify-center rounded-lg font-medium transition-[color,background-color,border-color] motion-reduce:transition-none',
    'focus-visible:outline-2 focus-visible:outline-[var(--color-primary)] focus-visible:outline-offset-2',
    // A blocked button stays focusable; the ring needs 3:1, so it fades less while focused
    'aria-disabled:opacity-50 aria-disabled:cursor-not-allowed aria-disabled:focus-visible:opacity-75',
    'disabled:opacity-50 disabled:cursor-not-allowed disabled:pointer-events-none',
    safeGet(sizeClasses, size, ''),
    safeGet(variantClasses, variant, ''),
    classProp,
    className
  )}
  {onclick}
>
  {@render children()}
</button>
