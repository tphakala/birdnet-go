<script lang="ts">
  import FormField from './FormField.svelte';
  import type { HTMLAttributes } from 'svelte/elements';

  interface Props extends HTMLAttributes<HTMLDivElement> {
    label: string;
    value: number;
    onUpdate: (_value: number) => void;
    min?: number;
    max?: number;
    step?: number;
    placeholder?: string;
    helpText?: string;
    required?: boolean;
    disabled?: boolean;
    error?: string;
    className?: string;
  }

  let {
    label,
    value = $bindable(),
    onUpdate,
    min,
    max,
    step = 1,
    placeholder = '',
    helpText = '',
    required = false,
    disabled = false,
    error,
    className = '',
    ...rest
  }: Props = $props();

  function handleChange(newValue: string | number | boolean | string[]) {
    const numValue = typeof newValue === 'number' ? newValue : parseFloat(String(newValue));
    if (!isNaN(numValue)) {
      value = numValue;
      onUpdate(numValue);
    }
  }
</script>

<div class={className} {...rest}>
  <FormField
    type="number"
    name={label.toLowerCase().replace(/\s+/g, '-')}
    {label}
    bind:value
    {min}
    {max}
    {step}
    {placeholder}
    {helpText}
    {required}
    {disabled}
    onChange={handleChange}
    inputClassName={error ? 'input-error' : ''}
  />

  {#if error}
    <div class="label">
      <span class="label-text-alt text-error">{error}</span>
    </div>
  {/if}
</div>
