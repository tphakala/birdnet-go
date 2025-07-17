<script lang="ts">
  import { cn } from '$lib/utils/cn.js';
  import type { Snippet } from 'svelte';

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

  function handleChange(event: Event) {
    const target = event.currentTarget as HTMLInputElement;
    checked = target.checked;
    onchange?.(checked);
  }

  const sizeClasses = {
    xs: 'checkbox-xs',
    sm: 'checkbox-sm',
    md: '',
    lg: 'checkbox-lg',
  };

  const variantClasses = {
    default: '',
    primary: 'checkbox-primary',
    secondary: 'checkbox-secondary',
    accent: 'checkbox-accent',
  };
</script>

<div class={cn('form-control relative', className)} {...rest}>
  <label class="label cursor-pointer justify-start" for={id}>
    <input
      type="checkbox"
      {id}
      bind:checked
      {disabled}
      class={cn('checkbox mr-2', sizeClasses[size], variantClasses[variant])}
      onchange={handleChange}
    />

    {#if children}
      {@render children()}
    {:else if label}
      <span class="label-text">{label}</span>
    {/if}

    {#if tooltip}
      <button
        type="button"
        class="help-icon ml-1 text-info hover:text-info-focus transition-colors"
        onmouseenter={() => (showTooltip = true)}
        onmouseleave={() => (showTooltip = false)}
        onfocus={() => (showTooltip = true)}
        onblur={() => (showTooltip = false)}
        aria-label="Help information"
      >
        ⓘ
      </button>
    {/if}
  </label>

  {#if helpText}
    <div class="label">
      <span class="label-text-alt text-base-content/70">{helpText}</span>
    </div>
  {/if}

  {#if tooltip && showTooltip}
    <div
      class="absolute z-50 p-2 mt-1 text-sm bg-base-300 border border-base-content/20 rounded shadow-lg max-w-xs"
      role="tooltip"
    >
      {tooltip}
    </div>
  {/if}
</div>
