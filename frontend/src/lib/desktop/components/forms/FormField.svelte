<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Validator, ValidationResult } from '$lib/utils/validators';
  import type { Snippet } from 'svelte';
  import { t } from '$lib/i18n';

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
    // Radio button specific
    radioValue?: string; // Value for radio button option (required when type is radio)
    // Event handlers
    onChange?: (_value: string | number | boolean | string[]) => void;
    onBlur?: () => void;
    onFocus?: () => void;
    onInput?: (_value: string | number | boolean | string[]) => void;
    onkeydown?: (_event: KeyboardEvent) => void;
    // Snippet-based approach (for UI compatibility)
    children?: Snippet;
    id?: string;
    error?: string | { key: string; params?: Record<string, unknown> };
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
    radioValue,
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
  let error = $state<string | { key: string; params?: Record<string, unknown> } | null>(null);

  // Generate counter suffix once on component creation (not reactive)
  const fieldIdSuffix = ++fieldCounter;

  // Derived IDs - react to prop changes while maintaining stable suffix
  const fieldId = $derived(id || `field-${name || 'field'}-${fieldIdSuffix}`);
  const helpTextId = $derived(helpText ? `${fieldId}-help` : undefined);
  const errorId = $derived(`${fieldId}-error`);

  // Compute aria-describedby based on what's visible
  let describedBy = $derived(
    error && (touched || externalError) ? errorId : helpText ? helpTextId : undefined
  );

  // Update error when external error changes
  $effect(() => {
    if (externalError !== undefined) {
      error = externalError;
    }
  });

  // Computed value for checkbox
  let checkboxValue = $derived(type === 'checkbox' ? Boolean(value) : false);

  // Validation - returns a key or null for reactive translation
  function validate(val: unknown): ValidationResult {
    if (required && !val && val !== 0 && val !== false) {
      return 'common.validation.required'; // Return key instead of translated text
    }

    for (const validator of validators) {
      const result = validator(val);
      if (result !== null) {
        return result;
      }
    }

    return null;
  }

  // Helper function to check if a value is a translation key object
  function isTranslationKey(
    value: unknown
  ): value is { key: string; params?: Record<string, unknown> } {
    return (
      typeof value === 'object' &&
      value !== null &&
      'key' in value &&
      typeof (value as any).key === 'string'
    );
  }

  // Reactive error message that translates on locale change
  let displayError = $derived.by(() => {
    if (!error) return null;

    // Check if error is a translation key object with optional parameters
    if (isTranslationKey(error)) {
      return t(error.key, error.params);
    }

    // Legacy support: If error is a string that looks like a translation key
    // Check for known translation key prefixes to avoid false positives
    if (
      typeof error === 'string' &&
      (error.startsWith('common.') ||
        error.startsWith('settings.') ||
        error.startsWith('forms.') ||
        error.startsWith('validation.'))
    ) {
      return t(error);
    }

    // Otherwise return as-is (for custom validation messages)
    return error;
  });

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
    const baseClasses = 'input  input-sm w-full';
    const errorClasses = error ? 'input-error' : '';

    return cn(baseClasses, errorClasses, inputClassName);
  }
</script>

<div class={cn('form-control min-w-0', className)}>
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
        aria-describedby={describedBy}
        class={cn('textarea  textarea-sm w-full', error && 'textarea-error', inputClassName)}
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
          aria-describedby={describedBy}
          class={cn('select  select-sm w-full', error && 'select-error', inputClassName)}
          onchange={handleChange}
          onblur={handleBlur}
          onfocus={handleFocus}
          {onkeydown}
        >
          {#each options as option (option.value)}
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
          aria-describedby={describedBy}
          class={cn('select  select-sm w-full', error && 'select-error', inputClassName)}
          onchange={handleChange}
          onblur={handleBlur}
          onfocus={handleFocus}
          {onkeydown}
        >
          {#if !required}
            <option value="">{t('forms.labels.selectOption')}</option>
          {/if}
          {#each options as option (option.value)}
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
          aria-describedby={describedBy}
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
      {#if !radioValue}
        <div class="text-error text-sm">{t('forms.errors.radioValueRequired')}</div>
      {:else}
        <label class="label cursor-pointer justify-start gap-2">
          <input
            id={fieldId}
            type="radio"
            {name}
            value={radioValue}
            checked={value === radioValue}
            {required}
            {disabled}
            {readonly}
            aria-describedby={describedBy}
            class={cn('radio', error && 'radio-error', inputClassName)}
            onchange={handleChange}
            onblur={handleBlur}
            onfocus={handleFocus}
            {onkeydown}
          />
          <span class="label-text">{label || placeholder}</span>
        </label>
      {/if}
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
          aria-describedby={describedBy}
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
        aria-describedby={describedBy}
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
      <span id={errorId} class={cn('text-xs text-error', errorClassName)}>{displayError}</span>
    </div>
  {:else if helpText}
    <span id={helpTextId} class="help-text">{helpText}</span>
  {/if}
</div>
