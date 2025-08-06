<!--
  Inline Slider Component
  
  Purpose: A compact, inline range slider with label and value display, matching the style
  used in settings pages. Provides a consistent, accessible slider interface.
  
  Features:
  - Compact inline layout with label and value display
  - Configurable min, max, step values
  - Optional unit display and value formatting
  - Disabled state support
  - Full accessibility with ARIA attributes
  - Consistent styling with daisyUI range components
  
  Props:
  - label: string - The label text for the slider
  - value: number - The current value (bindable)
  - onUpdate: (value: number) => void - Callback when value changes
  - min: number - Minimum value
  - max: number - Maximum value
  - step?: number - Step increment (default: 1)
  - unit?: string - Unit suffix to display (e.g., 'k', '%')
  - formatValue?: (value: number) => string - Custom value formatter
  - disabled?: boolean - Disable the slider
  - className?: string - Additional CSS classes
  - id?: string - Custom ID for the input element
  - helpText?: string - Help text to display below the slider
  
  @component
-->
<script lang="ts">
  import { cn } from '$lib/utils/cn.js';

  interface Props {
    label: string;
    value: number;
    onUpdate: (_value: number) => void;
    min: number;
    max: number;
    step?: number;
    unit?: string;
    formatValue?: (_value: number) => string;
    disabled?: boolean;
    className?: string;
    id?: string;
    helpText?: string;
  }

  let {
    label,
    value = $bindable(),
    onUpdate,
    min,
    max,
    step = 1,
    unit = '',
    formatValue,
    disabled = false,
    className = '',
    id,
    helpText = '',
  }: Props = $props();

  // Generate unique ID if not provided (browser-compatible)
  const generateId = () => {
    // Use crypto.randomUUID if available (modern browsers)
    if (typeof crypto !== 'undefined' && crypto.randomUUID) {
      return `inline-slider-${crypto.randomUUID()}`;
    }
    // Fallback for older browsers
    return `inline-slider-${Math.random().toString(36).substr(2, 9)}-${Date.now()}`;
  };

  const inputId = id || generateId();

  // Format display value
  let displayValue = $derived(
    formatValue
      ? formatValue(value)
      : (() => {
          // For decimal steps, show appropriate precision
          const decimalPlaces = step < 1 ? step.toString().split('.')[1]?.length || 2 : 0;
          return `${value.toFixed(decimalPlaces)}${unit}`;
        })()
  );

  function handleInput(event: Event) {
    const target = event.currentTarget as HTMLInputElement;
    const newValue = Number(target.value);
    value = newValue;
    onUpdate(newValue);
  }

  function handleChange(event: Event) {
    const target = event.currentTarget as HTMLInputElement;
    const newValue = Number(target.value);
    value = newValue;
    onUpdate(newValue);
  }
</script>

<div class={cn('form-control', className)}>
  <label class="label" for={inputId}>
    <span class="label-text">
      {label}
    </span>
    <span class="label-text font-mono">
      {displayValue}
    </span>
  </label>
  <input
    id={inputId}
    type="range"
    {min}
    {max}
    {step}
    {value}
    {disabled}
    oninput={handleInput}
    onchange={handleChange}
    class={cn('range range-primary range-sm', {
      'opacity-50': disabled,
    })}
    aria-label={label}
    aria-valuemin={min}
    aria-valuemax={max}
    aria-valuenow={value}
    aria-valuetext={displayValue}
    aria-disabled={disabled}
  />
  {#if helpText}
    <div class="label">
      <span class="label-text-alt text-base-content/70">{helpText}</span>
    </div>
  {/if}
</div>
