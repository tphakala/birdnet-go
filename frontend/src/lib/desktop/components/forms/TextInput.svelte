<script lang="ts">
  import { cn } from '$lib/utils/cn.js';

  interface Props {
    value: string;
    type?: 'text' | 'email' | 'password' | 'url' | 'tel' | 'search';
    label?: string;
    id?: string;
    placeholder?: string;
    disabled?: boolean;
    required?: boolean;
    readonly?: boolean;
    pattern?: string;
    minlength?: number;
    maxlength?: number;
    helpText?: string;
    tooltip?: string;
    className?: string;
    size?: 'xs' | 'sm' | 'md' | 'lg';
    validationMessage?: string;
    onchange?: (_value: string) => void;
    oninput?: (_value: string) => void;
  }

  let {
    value = $bindable(),
    type = 'text',
    label,
    id,
    placeholder,
    disabled = false,
    required = false,
    readonly = false,
    pattern,
    minlength,
    maxlength = 255,
    helpText,
    tooltip,
    className = '',
    size = 'sm',
    validationMessage,
    onchange,
    oninput,
    ...rest
  }: Props = $props();

  let showTooltip = $state(false);
  let touched = $state(false);
  let inputElement = $state<HTMLInputElement>();

  // Generate unique tooltip ID for accessibility (deterministic for SSR compatibility)
  let tooltipCounter = 0;
  let tooltipId = $derived(id ? `${id}-tooltip` : `tooltip-${++tooltipCounter}`);

  let isValid = $derived(() => {
    if (!inputElement || !touched) return true;
    return inputElement.validity.valid;
  });

  function handleChange(event: Event) {
    const target = event.currentTarget as HTMLInputElement;
    value = target.value;
    onchange?.(value);
  }

  function handleInput(event: Event) {
    const target = event.currentTarget as HTMLInputElement;
    value = target.value;
    touched = false; // Reset touched state on input
    oninput?.(value);
  }

  function handleBlur() {
    touched = true;
  }

  function handleInvalid() {
    touched = true;
  }

  const sizeClasses = {
    xs: 'input-xs',
    sm: 'input-sm',
    md: '',
    lg: 'input-lg',
  };
</script>

<div class={cn('form-control relative', className)} {...rest}>
  {#if label}
    <label class="label justify-start" for={id}>
      <span class="label-text capitalize">
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
          aria-describedby={tooltipId}
        >
          ⓘ
        </button>
      {/if}
    </label>
  {/if}

  <input
    bind:this={inputElement}
    {type}
    {id}
    bind:value
    {placeholder}
    {disabled}
    {required}
    {readonly}
    {pattern}
    {minlength}
    {maxlength}
    class={cn('input input-bordered w-full', sizeClasses[size], !isValid && 'input-error')}
    onchange={handleChange}
    oninput={handleInput}
    onblur={handleBlur}
    oninvalid={handleInvalid}
  />

  {#if !isValid && touched}
    <span class="text-sm text-error mt-1">
      {validationMessage || `Please enter a valid ${label || 'value'}`}
    </span>
  {/if}

  {#if helpText}
    <div class="label">
      <span class="label-text-alt text-base-content/70">{helpText}</span>
    </div>
  {/if}

  {#if tooltip && showTooltip}
    <div
      id={tooltipId}
      class="absolute z-50 p-2 mt-1 text-sm bg-base-300 border border-base-content/20 rounded shadow-lg max-w-xs"
      role="tooltip"
    >
      {tooltip}
    </div>
  {/if}
</div>
