<script lang="ts">
  import { cn } from '$lib/utils/cn.js';
  import type { Snippet } from 'svelte';
  import { safeGet } from '$lib/utils/security';
  import { Check } from '@lucide/svelte';

  interface Props {
    checked: boolean;
    disabled?: boolean;
    label?: string;
    id?: string;
    className?: string;
    size?: 'xs' | 'sm' | 'md' | 'lg';
    variant?: 'default' | 'primary' | 'secondary' | 'accent';
    helpText?: string;
    tooltip?: string;
    children?: Snippet;
    onchange?: (_checked: boolean) => void;
  }

  let {
    checked = $bindable(),
    disabled = false,
    label,
    id,
    className = '',
    size = 'xs',
    variant = 'primary',
    helpText,
    tooltip,
    children,
    onchange,
    ...rest
  }: Props = $props();

  let showTooltip = $state(false);

  // Generate unique IDs for accessibility
  const helpTextId = `checkbox-help-${Math.random().toString(36).substr(2, 9)}`;
  const tooltipId = `checkbox-tooltip-${Math.random().toString(36).substr(2, 9)}`;

  function handleChange(event: Event) {
    const target = event.currentTarget as HTMLInputElement;
    checked = target.checked;
    onchange?.(checked);
  }

  // Native Tailwind size classes for the checkbox box
  const sizeClasses = {
    xs: 'w-3.5 h-3.5',
    sm: 'w-4 h-4',
    md: 'w-5 h-5',
    lg: 'w-6 h-6',
  };

  // Icon size classes (slightly smaller than box)
  const iconSizeClasses = {
    xs: 'size-2.5',
    sm: 'size-3',
    md: 'size-3.5',
    lg: 'size-4',
  };

  // Variant classes for background when checked
  const variantBgClasses = {
    default: 'bg-[var(--color-base-content)]',
    primary: 'bg-[var(--color-primary)]',
    secondary: 'bg-[var(--color-secondary)]',
    accent: 'bg-[var(--color-accent)]',
  };

  // Variant classes for checkmark color (content color)
  const variantContentClasses = {
    default: 'text-[var(--color-base-100)]',
    primary: 'text-[var(--color-primary-content)]',
    secondary: 'text-[var(--color-secondary-content)]',
    accent: 'text-[var(--color-accent-content)]',
  };
</script>

<div class={cn('relative min-w-0', className)} {...rest}>
  <label class="flex items-center cursor-pointer justify-start py-1" for={id}>
    <!-- Hidden native checkbox for accessibility -->
    <input
      type="checkbox"
      {id}
      bind:checked
      {disabled}
      class="sr-only peer"
      onchange={handleChange}
      aria-describedby={helpText ? helpTextId : undefined}
    />

    <!-- Custom checkbox visual -->
    <span
      class={cn(
        'relative inline-flex items-center justify-center mr-2 shrink-0 border-2 rounded transition-all',
        'border-[var(--border-200)] bg-[var(--color-base-100)]',
        'peer-focus-visible:outline-2 peer-focus-visible:outline-[var(--color-primary)] peer-focus-visible:outline-offset-2',
        'peer-disabled:opacity-50 peer-disabled:cursor-not-allowed',
        safeGet(sizeClasses, size, ''),
        checked && safeGet(variantBgClasses, variant, ''),
        checked && 'border-transparent'
      )}
    >
      {#if checked}
        <Check
          class={cn(
            safeGet(iconSizeClasses, size, ''),
            safeGet(variantContentClasses, variant, '')
          )}
          strokeWidth={3}
        />
      {/if}
    </span>

    {#if children}
      {@render children()}
    {:else if label}
      <span class="text-sm text-[var(--color-base-content)]">{label}</span>
    {/if}

    {#if tooltip}
      <button
        type="button"
        class="ml-1 text-[var(--color-info)] hover:opacity-80 transition-opacity"
        onmouseenter={() => (showTooltip = true)}
        onmouseleave={() => (showTooltip = false)}
        onfocus={() => (showTooltip = true)}
        onblur={() => (showTooltip = false)}
        aria-label="Help information"
        aria-describedby={tooltipId}
      >
        ⓘ
      </button>
    {/if}
  </label>

  {#if helpText}
    <span
      id={helpTextId}
      class="text-xs text-[var(--color-base-content)] opacity-60 mt-1 block pl-5">{helpText}</span
    >
  {/if}

  {#if tooltip && showTooltip}
    <div
      id={tooltipId}
      class="absolute z-50 p-2 mt-1 text-sm bg-[var(--color-base-300)] border border-[var(--color-base-content)]/20 rounded shadow-lg text-[var(--color-base-content)]"
      role="tooltip"
      aria-live="polite"
      style:left="0"
      style:right="auto"
      style:top="calc(100% + 4px)"
      style:max-width="min(300px, calc(100vw - 40px))"
    >
      {tooltip}
    </div>
  {/if}
</div>
