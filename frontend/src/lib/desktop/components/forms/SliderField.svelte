<script lang="ts">
  import FormField from './FormField.svelte';
  import { cn } from '$lib/utils/cn.js';
  import type { HTMLAttributes } from 'svelte/elements';

  // Counter for unique ID generation across all SliderField instances
  let sliderFieldCounter = 0;

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

  // Generate unique ID for proper label association
  const fieldId = `slider-field-${++sliderFieldCounter}`;

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
  {#if showValue}
    <!-- Custom label with proper association and value badge -->
    <label for={fieldId} class="label">
      <span class="label-text font-medium">
        {label}
        {#if required}
          <span class="text-error">*</span>
        {/if}
      </span>
      <span class="badge badge-outline badge-sm font-mono">
        {displayValue}
      </span>
    </label>
    <FormField
      type="range"
      id={fieldId}
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
  {:else}
    <FormField
      type="range"
      {label}
      id={fieldId}
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
      labelClassName="label-text font-medium"
    />
  {/if}

  {#if error}
    <div class="label">
      <span class="label-text-alt text-error">{error}</span>
    </div>
  {/if}
</div>
