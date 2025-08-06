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
  - size?: 'xs' | 'sm' | 'md' - Size variant (default: 'xs')
  - className?: string - Additional CSS classes
  - id?: string - Custom ID for the input element
  
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
    size?: 'xs' | 'sm' | 'md';
    className?: string;
    id?: string;
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
    size = 'xs',
    className = '',
    id,
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

  // Size classes for the range input
  const sizeClasses: Record<'xs' | 'sm' | 'md', string> = {
    xs: 'range-xs',
    sm: 'range-sm',
    md: '',
  };

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

<div class={cn('inline-slider', className)}>
  <label class="label py-1" for={inputId}>
    <span class={cn('label-text', size === 'xs' ? 'text-xs' : size === 'sm' ? 'text-sm' : '')}>
      {label}
    </span>
    <span
      class={cn(
        'label-text-alt font-mono',
        size === 'xs' ? 'text-xs' : size === 'sm' ? 'text-sm' : ''
      )}
    >
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
    class={cn('range range-primary', sizeClasses[size as keyof typeof sizeClasses], {
      'opacity-50': disabled,
    })}
    aria-label={label}
    aria-valuemin={min}
    aria-valuemax={max}
    aria-valuenow={value}
    aria-valuetext={displayValue}
    aria-disabled={disabled}
  />
</div>

<style>
  .inline-slider {
    width: 100%;
  }
</style>
