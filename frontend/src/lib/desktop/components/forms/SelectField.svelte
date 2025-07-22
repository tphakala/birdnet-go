<script lang="ts">
  import { cn } from '$lib/utils/cn.js';
  import type { Snippet } from 'svelte';

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
          <svg
            class="w-4 h-4"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <circle cx="12" cy="12" r="10"></circle>
            <path d="M9,9h0a3,3,0,0,1,5.12,2.12h0A3,3,0,0,1,16,14"></path>
            <circle cx="12" cy="17" r=".5"></circle>
          </svg>
        </button>
      {/if}
    </label>
  {/if}

  <select
    {id}
    bind:value
    {disabled}
    {required}
    class={cn('select select-bordered w-full', sizeClasses[size])}
    onchange={handleChange}
  >
    {#if placeholder}
      <option value="" disabled>{placeholder}</option>
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
