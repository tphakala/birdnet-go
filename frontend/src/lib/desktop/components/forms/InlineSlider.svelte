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
  - size?: 'xs' | 'sm' | 'md' | 'lg' - Size variant for the slider (default: 'sm')
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
    size?: 'xs' | 'sm' | 'md' | 'lg';
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
    size = 'sm',
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

  // Constants for clarity
  const DEFAULT_DECIMAL_PLACES = 2;

  // Size classes for the range input
  const sizeClasses = {
    xs: 'range-xs',
    sm: 'range-sm',
    md: 'range-md',
    lg: 'range-lg',
  };

  // Calculate decimal places for value formatting
  // If step is less than 1, determine precision from step value
  // Otherwise, use whole numbers (0 decimal places)
  const getDecimalPlaces = (stepValue: number): number => {
    if (stepValue >= 1) return 0;

    const stepString = stepValue.toString();
    const decimalPart = stepString.split('.')[1];
    return decimalPart?.length ?? DEFAULT_DECIMAL_PLACES;
  };

  // Format display value
  let displayValue = $derived(
    formatValue
      ? formatValue(value)
      : (() => {
          const decimalPlaces = getDecimalPlaces(step);
          return `${value.toFixed(decimalPlaces)}${unit}`;
        })()
  );

  // Ensure value stays within configured bounds
  function clampValue(val: number): number {
    return Math.min(Math.max(val, min), max);
  }

  // Single handler for both input and change events since they have identical logic
  function handleInputChange(event: Event) {
    const target = event.currentTarget as HTMLInputElement;
    const rawValue = Number(target.value);
    const newValue = clampValue(rawValue);
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
    oninput={handleInputChange}
    onchange={handleInputChange}
    class={cn('range range-primary', sizeClasses[size], {
      'opacity-50': disabled,
      'mt-1': size === 'sm',
      'mb-1': size === 'sm',
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
