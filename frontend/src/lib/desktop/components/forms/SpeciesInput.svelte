<!--
  SpeciesInput - A reusable form component for species input with autocomplete
  
  Features:
  - Autocomplete with predictions dropdown
  - Keyboard navigation (Enter to add, Escape to cancel, Arrow keys to navigate)
  - Form validation with error states
  - Size variants (xs, sm, md, lg)
  - Label, help text, and tooltip support
  - Customizable button text and icon
  - Accessible with proper ARIA attributes
  
  Usage:
  <SpeciesInput
    bind:value={inputValue}
    label="Add Species"
    placeholder="Type species name..."
    helpText="Search and add species from the database"
    predictions={speciesList}
    size="sm"
    onAdd={handleAddSpecies}
  />
-->
<script lang="ts">
  import { cn } from '$lib/utils/cn.js';

  interface Props {
    value?: string;
    label?: string;
    id?: string;
    placeholder?: string;
    disabled?: boolean;
    required?: boolean;
    helpText?: string;
    tooltip?: string;
    size?: 'xs' | 'sm' | 'md' | 'lg';
    validationMessage?: string;
    predictions?: string[];
    buttonText?: string;
    buttonIcon?: boolean;
    buttonSize?: 'xs' | 'sm' | 'md' | 'lg';
    maxPredictions?: number;
    minCharsForPredictions?: number;
    className?: string;
    onInput?: (_value: string) => void;
    onAdd?: (_value: string) => void;
    onPredictionSelect?: (_prediction: string) => void;
  }

  let {
    value = $bindable(''),
    label,
    id,
    placeholder = 'Add new species',
    disabled = false,
    required = false,
    helpText,
    tooltip,
    size = 'sm',
    validationMessage,
    predictions = [],
    buttonText = 'Add',
    buttonIcon = true,
    buttonSize,
    maxPredictions = 10,
    minCharsForPredictions = 2,
    className = '',
    onInput,
    onAdd,
    onPredictionSelect,
    ...rest
  }: Props = $props();

  // Internal state for managing predictions visibility
  let showPredictions = $state(false);
  let showTooltip = $state(false);
  let touched = $state(false);
  let inputElement: HTMLInputElement;

  // Auto-derive button size from input size if not specified
  let effectiveButtonSize = $derived(buttonSize || size);

  // Validation state
  let isValid = $derived(() => {
    if (!inputElement || !touched) return true;
    return inputElement.validity.valid;
  });

  // Size classes for input and button
  const inputSizeClasses = {
    xs: 'input-xs',
    sm: 'input-sm',
    md: '',
    lg: 'input-lg',
  };

  const buttonSizeClasses = {
    xs: 'btn-xs',
    sm: 'btn-sm',
    md: '',
    lg: 'btn-lg',
  };

  // Filter predictions based on current value
  let filteredPredictions = $derived(
    value.length >= minCharsForPredictions && predictions.length > 0
      ? predictions
          .filter(
            prediction =>
              prediction.toLowerCase().includes(value.toLowerCase()) && prediction !== value
          )
          .slice(0, maxPredictions)
      : []
  );

  // Update predictions visibility
  $effect(() => {
    showPredictions = filteredPredictions.length > 0;
  });

  function handleInput(event: Event) {
    const target = event.target as HTMLInputElement;
    value = target.value;
    touched = false; // Reset touched state on input
    onInput?.(target.value);
  }

  function handleBlur() {
    touched = true;
  }

  function handleInvalid() {
    touched = true;
  }

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === 'Enter') {
      event.preventDefault();
      handleAdd();
    } else if (event.key === 'Escape') {
      event.preventDefault();
      showPredictions = false;
      inputElement?.blur();
    } else if (event.key === 'ArrowDown' && showPredictions) {
      event.preventDefault();
      // Focus first prediction
      const firstPrediction = document.querySelector('.species-prediction-item') as HTMLElement;
      firstPrediction?.focus();
    }
  }

  function handleAdd() {
    if (!value.trim() || disabled) return;

    const trimmedValue = value.trim();
    onAdd?.(trimmedValue);

    // Clear input after successful add
    value = '';
    showPredictions = false;
  }

  function selectPrediction(prediction: string) {
    value = prediction;
    onPredictionSelect?.(prediction);
    showPredictions = false;

    // Defer handleAdd to next event loop to ensure state updates (showPredictions = false) 
    // have propagated before triggering add operation
    setTimeout(() => {
      handleAdd();
    }, 0);
  }

  function handlePredictionKeydown(event: KeyboardEvent, prediction: string, index: number) {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      selectPrediction(prediction);
    } else if (event.key === 'ArrowDown') {
      event.preventDefault();
      const nextItem = document.querySelectorAll('.species-prediction-item')[
        index + 1
      ] as HTMLElement;
      nextItem?.focus();
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      if (index === 0) {
        inputElement?.focus();
      } else {
        const prevItem = document.querySelectorAll('.species-prediction-item')[
          index - 1
        ] as HTMLElement;
        prevItem?.focus();
      }
    } else if (event.key === 'Escape') {
      event.preventDefault();
      showPredictions = false;
      inputElement?.focus();
    }
  }

  // Close predictions when clicking/touching outside
  function handleDocumentClick(event: MouseEvent | TouchEvent) {
    const target = event.target as globalThis.Element;
    if (!target.closest('.form-control')) {
      showPredictions = false;
    }
  }

  // Add document click and touch listeners for mobile support
  $effect(() => {
    document.addEventListener('click', handleDocumentClick);
    document.addEventListener('touchstart', handleDocumentClick);
    return () => {
      document.removeEventListener('click', handleDocumentClick);
      document.removeEventListener('touchstart', handleDocumentClick);
    };
  });
</script>

<div class={cn('form-control relative species-input-container', className)} {...rest}>
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
        >
          ⓘ
        </button>
      {/if}
    </label>
  {/if}

  <!-- Input container with relative positioning for dropdown -->
  <div class="relative">
    <div class="join w-full">
      <input
        bind:this={inputElement}
        type="text"
        {id}
        bind:value
        {placeholder}
        {disabled}
        {required}
        class={cn(
          'input input-bordered join-item flex-1',
          inputSizeClasses[size],
          !isValid && 'input-error'
        )}
        oninput={handleInput}
        onkeydown={handleKeydown}
        onblur={handleBlur}
        oninvalid={handleInvalid}
        onfocus={() => {
          if (filteredPredictions.length > 0) {
            showPredictions = true;
          }
        }}
        autocomplete="off"
        role="combobox"
        aria-expanded={showPredictions}
        aria-haspopup="listbox"
        aria-controls="species-predictions-list"
        aria-label={label || placeholder}
      />
      <button
        type="button"
        class={cn('btn btn-primary join-item', buttonSizeClasses[effectiveButtonSize])}
        onclick={handleAdd}
        disabled={disabled || !value.trim()}
        aria-label="Add species"
      >
        {#if buttonIcon}
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-5 w-5"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M12 6v6m0 0v6m0-6h6m-6 0H6"
            />
          </svg>
        {/if}
        {buttonText}
      </button>
    </div>

    <!-- Predictions Dropdown - positioned relative to input container -->
    {#if showPredictions && filteredPredictions.length > 0}
      <div
        id="species-predictions-list"
        class="dropdown-menu bg-base-100 border border-base-300 rounded-lg shadow-lg mt-1 max-h-60 overflow-y-auto min-w-0"
        style:min-width="100%"
        role="listbox"
        aria-label="Species suggestions"
      >
        {#each filteredPredictions as prediction, index}
          <button
            type="button"
            class="species-prediction-item w-full text-left px-4 py-2 hover:bg-base-200 focus:bg-base-200 focus:outline-none border-none bg-transparent text-sm"
            onclick={() => selectPrediction(prediction)}
            onkeydown={e => handlePredictionKeydown(e, prediction, index)}
            role="option"
            aria-selected="false"
            tabindex="-1"
          >
            {prediction}
          </button>
        {/each}
      </div>
    {/if}
  </div>

  <!-- Validation Message -->
  {#if !isValid && touched}
    <span class="text-sm text-error mt-1">
      {validationMessage || `Please enter a valid ${label || 'species'}`}
    </span>
  {/if}

  <!-- Help Text -->
  {#if helpText}
    <div class="label">
      <span class="label-text-alt text-base-content/70">{helpText}</span>
    </div>
  {/if}

  <!-- Tooltip -->
  {#if tooltip && showTooltip}
    <div
      class="absolute z-50 p-2 mt-1 text-sm bg-base-300 border border-base-content/20 rounded shadow-lg max-w-xs"
      role="tooltip"
    >
      {tooltip}
    </div>
  {/if}
</div>

<style>
  .species-prediction-item:focus {
    /* Custom focus styles for better visibility */
    background-color: hsl(var(--b2));
    outline: 2px solid hsl(var(--p));
    outline-offset: -2px;
  }

  /* Ensure the dropdown doesn't get cut off - use specific class instead of global overrides */
  .species-input-container {
    position: relative;
    z-index: 50; /* Reasonable z-index instead of 9999 */
  }
  
  .species-input-container .dropdown-menu {
    position: absolute;
    top: 100%;
    left: 0;
    right: 0;
    z-index: 51;
  }

  /* Dropdown positioning handled by .dropdown-menu class above */
    /* Use viewport units to prevent cutoff on small screens */
    max-width: 100vw;
    box-shadow:
      0 10px 15px -3px rgba(0, 0, 0, 0.1),
      0 4px 6px -2px rgba(0, 0, 0, 0.05);
  }

  /* Ensure long species names don't break layout */
  .species-prediction-item {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
</style>
