<script lang="ts">
  import FormField from './FormField.svelte';
  import { cn } from '$lib/utils/cn.js';
  import type { HTMLAttributes } from 'svelte/elements';

  interface Props extends HTMLAttributes<HTMLDivElement> {
    label: string;
    value: number;
    onUpdate: (_value: number) => void;
    min: number;
    max: number;
    step?: number;
    showValue?: boolean;
    helpText?: string;
    required?: boolean;
    disabled?: boolean;
    error?: string;
    className?: string;
    unit?: string;
    formatValue?: (_value: number) => string;
  }

  let {
    label,
    value = $bindable(),
    onUpdate,
    min,
    max,
    step = 0.1,
    showValue = true,
    helpText = '',
    required = false,
    disabled = false,
    error,
    className = '',
    unit = '',
    formatValue,
    ...rest
  }: Props = $props();

  let displayValue = $derived(formatValue ? formatValue(value) : `${value}${unit}`);

  function handleChange(newValue: string | number | boolean | string[]) {
    const numValue = typeof newValue === 'number' ? newValue : parseFloat(String(newValue));
    if (!isNaN(numValue)) {
      value = numValue;
      onUpdate(numValue);
    }
  }
</script>

<div class={cn('form-control', className)} {...rest}>
  <div class="flex items-center justify-between mb-2">
    <label class="label-text font-medium">
      {label}
      {#if required}
        <span class="text-error">*</span>
      {/if}
    </label>
    {#if showValue}
      <span class="badge badge-outline badge-sm font-mono">
        {displayValue}
      </span>
    {/if}
  </div>

  <FormField
    type="range"
    name={label.toLowerCase().replace(/\s+/g, '-')}
    bind:value
    {min}
    {max}
    {step}
    {helpText}
    {required}
    {disabled}
    onChange={handleChange}
    inputClassName={cn('range range-primary', { 'range-error': !!error })}
  />

  {#if error}
    <div class="label">
      <span class="label-text-alt text-error">{error}</span>
    </div>
  {/if}
</div>
