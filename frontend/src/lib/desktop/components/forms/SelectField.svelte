<script lang="ts">
  import { cn } from '$lib/utils/cn.js';
  import type { Snippet } from 'svelte';
  import { Info } from '@lucide/svelte';
  import { safeGet } from '$lib/utils/security';

  interface Option {
    value: string;
    label: string;
    disabled?: boolean;
  }

  interface Props {
    value: string;
    options?: Option[];
    label?: string;
    id?: string;
    placeholder?: string;
    disabled?: boolean;
    required?: boolean;
    helpText?: string;
    tooltip?: string;
    className?: string;
    size?: 'xs' | 'sm' | 'md' | 'lg';
    children?: Snippet;
    onchange?: (_value: string) => void;
  }

  let {
    value = $bindable(),
    options,
    label,
    id,
    placeholder,
    disabled = false,
    required = false,
    helpText,
    tooltip,
    className = '',
    size = 'sm',
    children,
    onchange,
    ...rest
  }: Props = $props();

  let showTooltip = $state(false);

  function handleChange(event: Event) {
    const target = event.currentTarget as HTMLSelectElement;
    value = target.value;
    onchange?.(value);
  }

  const sizeClasses = {
    xs: 'select-xs',
    sm: 'select-sm',
    md: '',
    lg: 'select-lg',
  };
</script>

<div class={cn('form-control relative', className)} {...rest}>
  {#if label}
    <label class="label justify-start" for={id}>
      <span class="label-text">
        {label}
        {#if required}
          <span class="text-error ml-1">*</span>
        {/if}
      </span>

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
          <Info class="size-4" />
        </button>
      {/if}
    </label>
  {/if}

  <select
    {id}
    bind:value
    {disabled}
    {required}
    class={cn('select select-bordered w-full', safeGet(sizeClasses, size, ''))}
    onchange={handleChange}
  >
    {#if placeholder}
      <option value="" selected hidden>{placeholder}</option>
    {/if}

    {#if children}
      {@render children()}
    {:else if options}
      {#each options as option}
        <option value={option.value} disabled={option.disabled}>
          {option.label}
        </option>
      {/each}
    {/if}
  </select>

  {#if helpText}
    <div class="label">
      <span class="label-text-alt text-base-content/70">{helpText}</span>
    </div>
  {/if}

  {#if tooltip && showTooltip}
    <div
      class="absolute top-full left-0 z-tooltip p-2 mt-1 text-sm bg-base-300 border border-base-content/20 rounded shadow-lg max-w-xs"
      role="tooltip"
    >
      {tooltip}
    </div>
  {/if}
</div>
