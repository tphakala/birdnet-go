<!--
  NumberField.svelte
  
  A numeric input field with automatic value clamping and validation.
  
  Behavior:
  - Values outside min/max bounds are automatically clamped to the nearest valid value
  - For example: if min=0 and max=100:
    - Entering -10 will automatically adjust to 0
    - Entering 150 will automatically adjust to 100
  - This provides immediate feedback and prevents invalid values from being submitted
  
  Props:
  - min/max: Define valid range; values outside are clamped
  - step: Increment/decrement step size
  - onUpdate: Called with the clamped value after validation
-->
<script lang="ts">
  import FormField from './FormField.svelte';
  import { t } from '$lib/i18n';
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

  // Reactive state for showing clamping feedback
  let wasClamped = $state(false);
  let clampedMessage = $derived(
    wasClamped
      ? min !== undefined && value === min
        ? t('components.forms.numberField.adjustedToMinimum', { value: min })
        : max !== undefined && value === max
          ? t('components.forms.numberField.adjustedToMaximum', { value: max })
          : ''
      : ''
  );

  // Clear clamping message after 3 seconds
  $effect(() => {
    if (wasClamped) {
      const timeout = setTimeout(() => {
        wasClamped = false;
      }, 3000);
      return () => clearTimeout(timeout);
    }
  });

  function clampValue(numValue: number): number {
    // Handle extreme values first - check for NaN and Infinity
    if (isNaN(numValue) || !isFinite(numValue)) {
      // For invalid numbers (NaN, Infinity, -Infinity), use defaults
      wasClamped = true;
      return min !== undefined ? min : max !== undefined ? max : 0;
    }

    // For valid finite numbers, clamp to min/max constraints if specified
    if (min !== undefined && numValue < min) {
      wasClamped = true;
      return min;
    } else if (max !== undefined && numValue > max) {
      wasClamped = true;
      return max;
    } else {
      wasClamped = false;
      return numValue;
    }
  }

  function handleChange(newValue: string | number | boolean | string[]) {
    // Explicitly handle different input types
    if (Array.isArray(newValue)) {
      return; // Arrays are not valid for number fields
    }

    if (typeof newValue === 'boolean') {
      return; // Booleans are not valid for number fields
    }

    // Convert to number with robust validation
    let numValue: number;

    if (typeof newValue === 'number') {
      numValue = newValue;
    } else {
      const stringValue = String(newValue).trim();
      if (stringValue === '' || stringValue === null || stringValue === undefined) {
        return; // Empty values should not update state
      }

      // Special handling for known numeric string values
      if (stringValue === 'NaN' || stringValue === 'Infinity' || stringValue === '-Infinity') {
        numValue = parseFloat(stringValue);
      } else {
        numValue = parseFloat(stringValue);
        // For string inputs that are not valid numbers (except for special cases above),
        // maintain backward compatibility by not calling onUpdate
        if (isNaN(numValue)) {
          return;
        }
      }
    }

    // Apply clamping for all numeric values (including NaN, Infinity, -Infinity)
    const clampedValue = clampValue(numValue);
    value = clampedValue;
    onUpdate(clampedValue);
  }

  function handleBlur() {
    // On blur, ensure the current value is clamped and update if needed
    const currentNumValue = typeof value === 'number' ? value : parseFloat(String(value));
    const clampedValue = clampValue(currentNumValue);

    if (clampedValue !== value) {
      value = clampedValue;
      onUpdate(clampedValue);
    }
  }
</script>

<div class={className} {...rest}>
  <FormField
    type="number"
    name={label
      .toLowerCase()
      .replace(/\s+/g, '-')
      .replace(/[^a-z0-9-]/g, '')}
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
    onBlur={handleBlur}
    inputClassName={error || clampedMessage ? 'input-error' : ''}
  />

  {#if error}
    <div class="label">
      <span class="label-text-alt text-error">{error}</span>
    </div>
  {:else if clampedMessage}
    <div class="label">
      <span
        class="label-text-alt text-warning animate-pulse"
        role="status"
        aria-live="polite"
        aria-atomic="true"
      >
        {clampedMessage}
      </span>
    </div>
  {/if}
</div>
