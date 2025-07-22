<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Validator, ValidationResult } from '$lib/utils/validators';
  import type { Snippet } from 'svelte';

  // Module-level counter for consistent SSR-safe IDs
  let fieldCounter = 0;

  type FieldType =
    | 'text'
    | 'email'
    | 'password'
    | 'number'
    | 'tel'
    | 'url'
    | 'date'
    | 'time'
    | 'datetime-local'
    | 'textarea'
    | 'select'
    | 'checkbox'
    | 'radio'
    | 'file'
    | 'color'
    | 'range';

  interface Props {
    type?: FieldType;
    name?: string;
    label?: string;
    value?: string | number | boolean | string[];
    placeholder?: string;
    helpText?: string;
    required?: boolean;
    disabled?: boolean;
    readonly?: boolean;
    validators?: Validator[];
    className?: string;
    inputClassName?: string;
    labelClassName?: string;
    errorClassName?: string;
    // Type-specific props
    options?: Array<{ value: string; label: string; disabled?: boolean }>;
    multiple?: boolean;
    min?: number | string;
    max?: number | string;
    step?: number | string;
    rows?: number;
    cols?: number;
    accept?: string;
    pattern?: string;
    autocomplete?: HTMLInputElement['autocomplete'];
    // Event handlers
    onChange?: (_value: string | number | boolean | string[]) => void;
    onBlur?: () => void;
    onFocus?: () => void;
    onInput?: (_value: string | number | boolean | string[]) => void;
    onkeydown?: (_event: KeyboardEvent) => void;
    // Snippet-based approach (for UI compatibility)
    children?: Snippet;
    id?: string;
    error?: string;
  }

  let {
    type = 'text',
    name,
    label,
    value = $bindable(''),
    placeholder = '',
    helpText = '',
    required = false,
    disabled = false,
    readonly = false,
    validators = [],
    className = '',
    inputClassName = '',
    labelClassName = '',
    errorClassName = '',
    options = [],
    multiple = false,
    min,
    max,
    step,
    rows = 3,
    cols,
    accept,
    pattern,
    autocomplete,
    onChange,
    onBlur,
    onFocus,
    onInput,
    onkeydown,
    // Snippet-based props
    children,
    id,
    error: externalError,
  }: Props = $props();

  // State
  let touched = $state(false);
  let error = $state<string | null>(externalError || null);
  let fieldId = id || `field-${name || 'field'}-${++fieldCounter}`;

  // Update error when external error changes
  $effect(() => {
    if (externalError !== undefined) {
      error = externalError;
    }
  });

  // Computed value for checkbox
  let checkboxValue = $derived(type === 'checkbox' ? Boolean(value) : false);

  // Validation
  function validate(val: unknown): ValidationResult {
    if (required && !val && val !== 0 && val !== false) {
      return 'This field is required';
    }

    for (const validator of validators) {
      const result = validator(val);
      if (result !== null) {
        return result;
      }
    }

    return null;
  }

  // Run validation when value changes (only if no external error)
  $effect(() => {
    if (touched && externalError === undefined) {
      error = validate(value);
    }
  });

  // Update value when checkbox changes
  $effect(() => {
    if (type === 'checkbox' && checkboxValue !== Boolean(value)) {
      value = checkboxValue;
      onChange?.(checkboxValue);
    }
  });

  // Event handlers
  function handleChange(event: Event) {
    const target = event.target as HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement;
    let newValue: string | number | boolean | string[] = target.value;

    if (type === 'checkbox') {
      newValue = (target as HTMLInputElement).checked;
    } else if (type === 'number' || type === 'range') {
      newValue = target.value ? parseFloat(target.value) : '';
    } else if (type === 'select' && multiple) {
      const selectElement = target as HTMLSelectElement;
      newValue = Array.from(selectElement.selectedOptions).map(opt => opt.value);
    } else if (type === 'file') {
      // For file inputs, we don't update the value directly
      // The parent component should handle file selection through onChange
      onChange?.(target.value);
      return;
    }

    value = newValue;
    onChange?.(newValue);
  }

  function handleInput(event: Event) {
    const target = event.target as HTMLInputElement | HTMLTextAreaElement;
    let newValue: string | number = target.value;

    if (type === 'number' || type === 'range') {
      newValue = target.value ? parseFloat(target.value) : '';
    }

    // The value is already updated by bind:value
    // Only call onInput callback, onChange is handled by bind:value
    onInput?.(newValue);
  }

  function handleBlur() {
    touched = true;
    if (externalError === undefined) {
      error = validate(value);
    }
    onBlur?.();
  }

  function handleFocus() {
    onFocus?.();
  }

  // Get input base classes
  function getInputClasses(): string {
    const baseClasses = 'input input-bordered input-sm w-full';
    const errorClasses = error ? 'input-error' : '';

    return cn(baseClasses, errorClasses, inputClassName);
  }
</script>

<div class={cn('form-control', className)}>
  {#if label}
    <label for={fieldId} class={cn('label', labelClassName)}>
      <span class="label-text">
        {label}
        {#if required}
          <span class="text-error">*</span>
        {/if}
      </span>
    </label>
  {/if}

  {#if children}
    <!-- Snippet-based content for UI compatibility -->
    {@render children()}
  {:else if name}
    {#if type === 'textarea'}
      <textarea
        id={fieldId}
        {name}
        bind:value
        {placeholder}
        {required}
        {disabled}
        {readonly}
        {rows}
        {cols}
        class={cn(
          'textarea textarea-bordered textarea-sm w-full',
          error && 'textarea-error',
          inputClassName
        )}
        onchange={handleChange}
        oninput={handleInput}
        onblur={handleBlur}
        onfocus={handleFocus}
        {onkeydown}
      ></textarea>
    {:else if type === 'select'}
      {#if multiple}
        <select
          id={fieldId}
          {name}
          bind:value
          {required}
          {disabled}
          multiple
          class={cn(
            'select select-bordered select-sm w-full',
            error && 'select-error',
            inputClassName
          )}
          onchange={handleChange}
          onblur={handleBlur}
          onfocus={handleFocus}
          {onkeydown}
        >
          {#each options as option}
            <option value={option.value} disabled={option.disabled}>
              {option.label}
            </option>
          {/each}
        </select>
      {:else}
        <select
          id={fieldId}
          {name}
          bind:value
          {required}
          {disabled}
          class={cn(
            'select select-bordered select-sm w-full',
            error && 'select-error',
            inputClassName
          )}
          onchange={handleChange}
          onblur={handleBlur}
          onfocus={handleFocus}
          {onkeydown}
        >
          {#if !required}
            <option value="">Choose...</option>
          {/if}
          {#each options as option}
            <option value={option.value} disabled={option.disabled}>
              {option.label}
            </option>
          {/each}
        </select>
      {/if}
    {:else if type === 'checkbox'}
      <label class="label cursor-pointer justify-start gap-2">
        <input
          id={fieldId}
          type="checkbox"
          {name}
          bind:checked={checkboxValue}
          {required}
          {disabled}
          {readonly}
          class={cn('checkbox', error && 'checkbox-error', inputClassName)}
          onchange={handleChange}
          onblur={handleBlur}
          onfocus={handleFocus}
          {onkeydown}
        />
        {#if placeholder || label}
          <span class="label-text">{placeholder || label}</span>
        {/if}
      </label>
    {:else if type === 'radio'}
      <!-- Radio buttons would typically be used in a group, so this is a single radio option -->
      <label class="label cursor-pointer justify-start gap-2">
        <input
          id={fieldId}
          type="radio"
          {name}
          value={placeholder}
          checked={value === placeholder}
          {required}
          {disabled}
          {readonly}
          class={cn('radio', error && 'radio-error', inputClassName)}
          onchange={handleChange}
          onblur={handleBlur}
          onfocus={handleFocus}
          {onkeydown}
        />
        <span class="label-text">{placeholder}</span>
      </label>
    {:else if type === 'range'}
      <div class="w-full">
        <input
          id={fieldId}
          type="range"
          {name}
          bind:value
          {min}
          {max}
          {step}
          {disabled}
          {readonly}
          class={cn('range', error && 'range-error', inputClassName)}
          onchange={handleChange}
          oninput={handleInput}
          onblur={handleBlur}
          onfocus={handleFocus}
          {onkeydown}
        />
        {#if min !== undefined && max !== undefined}
          <div class="w-full flex justify-between text-xs px-2">
            <span>{min}</span>
            <span class="font-medium">{value}</span>
            <span>{max}</span>
          </div>
        {/if}
      </div>
    {:else}
      <input
        id={fieldId}
        {type}
        {name}
        bind:value
        {placeholder}
        {required}
        {disabled}
        {readonly}
        {min}
        {max}
        {step}
        {accept}
        {pattern}
        {autocomplete}
        class={getInputClasses()}
        onchange={handleChange}
        oninput={handleInput}
        onblur={handleBlur}
        onfocus={handleFocus}
        {onkeydown}
      />
    {/if}
  {/if}

  {#if error && (touched || externalError)}
    <div class="label">
      <span class={cn('label-text-alt text-error', errorClassName)}>{error}</span>
    </div>
  {:else if helpText}
    <div class="label">
      <span class="label-text-alt">{helpText}</span>
    </div>
  {/if}
</div>
