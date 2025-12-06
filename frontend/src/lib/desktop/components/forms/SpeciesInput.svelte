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
  import { Plus } from '@lucide/svelte';
  import { safeGet } from '$lib/utils/security';
  import { Z_INDEX } from '$lib/utils/z-index';

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
  let portalDropdown: HTMLDivElement | null = null;

  // Generate unique ID for this instance using timestamp and counter
  // This ensures no collisions even with multiple instances created simultaneously
  let idCounter = 0;
  if (typeof window !== 'undefined') {
    // Use a type assertion for the counter property
    const win = window as Window & { __speciesInputCounter?: number };
    if (!win.__speciesInputCounter) {
      win.__speciesInputCounter = 0;
    }
    idCounter = ++win.__speciesInputCounter;
  }
  const instanceId = `species-predictions-${Date.now()}-${idCounter}`;

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

  // Update predictions visibility and manage portal dropdown
  $effect(() => {
    const shouldShow = filteredPredictions.length > 0;

    if (shouldShow && !portalDropdown && inputElement) {
      createPortalDropdown();
    } else if (!shouldShow && portalDropdown) {
      destroyPortalDropdown();
    }

    if (shouldShow && portalDropdown) {
      updatePortalDropdown();
    }

    showPredictions = shouldShow;
  });

  // Event delegation handlers
  function handlePortalClick(event: MouseEvent) {
    const button = (event.target as HTMLElement).closest('.species-prediction-item');
    if (button) {
      const prediction = button.getAttribute('data-prediction');
      if (prediction) {
        selectPrediction(prediction);
      }
    }
  }

  function handlePortalKeydown(event: KeyboardEvent) {
    const button = (event.target as HTMLElement).closest('.species-prediction-item');
    if (button) {
      const prediction = button.getAttribute('data-prediction');
      const index = parseInt(button.getAttribute('data-index') || '0', 10);
      if (prediction !== null) {
        handlePredictionKeydown(event, prediction, index);
      }
    }
  }

  // Create dropdown element attached to document.body
  function createPortalDropdown() {
    if (!inputElement || portalDropdown) return;

    portalDropdown = document.createElement('div');
    portalDropdown.id = instanceId;
    portalDropdown.className =
      'bg-base-100 border border-base-300 rounded-lg shadow-lg max-h-60 overflow-y-auto';
    portalDropdown.style.position = 'absolute';
    portalDropdown.style.zIndex = Z_INDEX.PORTAL_DROPDOWN.toString();
    portalDropdown.setAttribute('role', 'listbox');
    portalDropdown.setAttribute('aria-label', 'Species suggestions');

    // Event delegation to prevent memory leaks
    portalDropdown.addEventListener('click', handlePortalClick);
    portalDropdown.addEventListener('keydown', handlePortalKeydown);

    document.body.appendChild(portalDropdown);
    updatePortalPosition();
  }

  // Update portal dropdown content and position
  function updatePortalDropdown() {
    if (!portalDropdown) return;

    // Optimize by reusing existing elements
    const existingButtons = portalDropdown.querySelectorAll('.species-prediction-item');
    const predictionsCount = filteredPredictions.length;
    const existingCount = existingButtons.length;

    // Update existing buttons
    for (let i = 0; i < Math.min(predictionsCount, existingCount); i++) {
      // eslint-disable-next-line security/detect-object-injection
      const button = existingButtons[i] as HTMLButtonElement;
      // eslint-disable-next-line security/detect-object-injection
      button.textContent = filteredPredictions[i];
      // eslint-disable-next-line security/detect-object-injection
      button.setAttribute('data-prediction', filteredPredictions[i]);
      button.setAttribute('data-index', i.toString());
      button.style.display = 'block';
    }

    // Add new buttons if needed
    if (predictionsCount > existingCount) {
      for (let i = existingCount; i < predictionsCount; i++) {
        const button = document.createElement('button');
        button.type = 'button';
        button.className =
          'species-prediction-item w-full text-left px-4 py-2 hover:bg-base-200 focus:bg-base-200 focus:outline-none border-none bg-transparent text-sm';
        // eslint-disable-next-line security/detect-object-injection
        button.textContent = filteredPredictions[i];
        button.setAttribute('role', 'option');
        button.setAttribute('aria-selected', 'false');
        button.setAttribute('tabindex', '-1');
        // eslint-disable-next-line security/detect-object-injection
        button.setAttribute('data-prediction', filteredPredictions[i]);
        button.setAttribute('data-index', i.toString());
        portalDropdown.appendChild(button);
      }
    }

    // Hide excess buttons
    if (existingCount > predictionsCount) {
      for (let i = predictionsCount; i < existingCount; i++) {
        // eslint-disable-next-line security/detect-object-injection
        (existingButtons[i] as HTMLElement).style.display = 'none';
      }
    }

    updatePortalPosition();
  }

  // Update portal dropdown position with smart positioning
  function updatePortalPosition() {
    if (!portalDropdown || !inputElement) return;

    const rect = inputElement.getBoundingClientRect();
    const dropdownHeight = Math.min(240, filteredPredictions.length * 40); // Estimate height
    const viewportHeight = window.innerHeight;
    const spaceBelow = viewportHeight - rect.bottom;
    const spaceAbove = rect.top;

    // Determine if we should position above or below
    if (spaceBelow < dropdownHeight + 8 && spaceAbove > spaceBelow) {
      // Position above the input
      // Position from top, but calculate to appear above the input
      const topPosition = rect.top + window.scrollY - dropdownHeight - 4;
      portalDropdown.style.top = `${topPosition}px`;
      portalDropdown.style.bottom = 'auto';
      // Add class for styling (shadow direction, etc.)
      portalDropdown.classList.add('dropdown-above');
      portalDropdown.classList.remove('dropdown-below');
    } else {
      // Position below the input (default)
      portalDropdown.style.top = `${rect.bottom + window.scrollY + 4}px`;
      portalDropdown.style.bottom = 'auto';
      portalDropdown.classList.add('dropdown-below');
      portalDropdown.classList.remove('dropdown-above');
    }

    // Horizontal viewport boundary detection
    let leftPosition = rect.left + window.scrollX;
    const dropdownWidth = rect.width;
    const viewportWidth = window.innerWidth;

    // Check if dropdown would go off-screen on the right
    if (rect.left + dropdownWidth > viewportWidth) {
      // Align dropdown to right edge of viewport with small margin
      leftPosition = viewportWidth - dropdownWidth - 8 + window.scrollX;
    }

    // Check if dropdown would go off-screen on the left
    if (rect.left < 0) {
      // Align dropdown to left edge of viewport with small margin
      leftPosition = 8 + window.scrollX;
    }

    portalDropdown.style.left = `${leftPosition}px`;
    portalDropdown.style.width = `${dropdownWidth}px`;
  }

  // Clean up portal dropdown
  function destroyPortalDropdown() {
    if (portalDropdown) {
      // Remove event listeners to prevent memory leaks
      portalDropdown.removeEventListener('click', handlePortalClick);
      portalDropdown.removeEventListener('keydown', handlePortalKeydown);

      // Check if element is actually in the DOM before removing
      if (portalDropdown.parentNode === document.body) {
        document.body.removeChild(portalDropdown);
      }
      portalDropdown = null;
    }
  }

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
      // Immediately destroy portal dropdown for testing consistency
      destroyPortalDropdown();
      inputElement?.blur();
    } else if (event.key === 'ArrowDown' && showPredictions && portalDropdown) {
      event.preventDefault();
      // Focus first prediction in portal dropdown
      const firstPrediction = portalDropdown.querySelector(
        '.species-prediction-item'
      ) as HTMLElement;
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
      if (portalDropdown) {
        const items = portalDropdown.querySelectorAll('.species-prediction-item');
        const nextItem = items[index + 1] as HTMLElement;
        nextItem?.focus();
      }
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      if (index === 0) {
        inputElement?.focus();
      } else if (portalDropdown) {
        const items = portalDropdown.querySelectorAll('.species-prediction-item');
        const prevItem = items[index - 1] as HTMLElement;
        prevItem?.focus();
      }
    } else if (event.key === 'Escape') {
      event.preventDefault();
      showPredictions = false;
      // Immediately destroy portal dropdown for testing consistency
      destroyPortalDropdown();
      inputElement?.focus();
    }
  }

  // Close predictions when clicking/touching outside
  function handleDocumentClick(event: MouseEvent | TouchEvent) {
    const target = event.target as globalThis.Element;
    if (!target.closest('.form-control') && !target.closest(`#${instanceId}`)) {
      showPredictions = false;
    }
  }

  // Add document click and touch listeners for mobile support, plus scroll/resize for positioning
  $effect(() => {
    document.addEventListener('click', handleDocumentClick);
    document.addEventListener('touchstart', handleDocumentClick);
    window.addEventListener('scroll', updatePortalPosition, { passive: true });
    window.addEventListener('resize', updatePortalPosition);

    return () => {
      document.removeEventListener('click', handleDocumentClick);
      document.removeEventListener('touchstart', handleDocumentClick);
      window.removeEventListener('scroll', updatePortalPosition);
      window.removeEventListener('resize', updatePortalPosition);
      // Clean up any remaining portal dropdown
      destroyPortalDropdown();
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
          safeGet(inputSizeClasses, size, ''),
          !isValid && 'input-error'
        )}
        oninput={handleInput}
        onkeydown={handleKeydown}
        onblur={handleBlur}
        oninvalid={handleInvalid}
        onfocus={() => {
          if (filteredPredictions.length > 0) {
            showPredictions = true;
            if (portalDropdown) {
              updatePortalPosition();
            }
          }
        }}
        autocomplete="off"
        role="combobox"
        aria-expanded={showPredictions}
        aria-haspopup="listbox"
        aria-controls={instanceId}
        aria-label={label || placeholder}
      />
      <button
        type="button"
        class={cn('btn btn-primary join-item', safeGet(buttonSizeClasses, effectiveButtonSize, ''))}
        onclick={handleAdd}
        disabled={disabled || !value.trim()}
        aria-label="Add species"
      >
        {#if buttonIcon}
          <Plus class="size-4" />
        {/if}
        {buttonText}
      </button>
    </div>

    <!-- Predictions Dropdown is now rendered as a portal to document.body via JavaScript -->
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
      class="absolute p-2 mt-1 text-sm bg-base-300 border border-base-content/20 rounded shadow-lg max-w-xs"
      style:z-index={Z_INDEX.PORTAL_TOOLTIP}
      role="tooltip"
    >
      {tooltip}
    </div>
  {/if}

  <!-- Screen reader announcement for dropdown state changes -->
  <div class="sr-only" role="status" aria-live="polite" aria-atomic="true">
    {#if showPredictions && filteredPredictions.length > 0}
      {filteredPredictions.length} species suggestions available. Use arrow keys to navigate.
    {:else if showPredictions && filteredPredictions.length === 0}
      No species suggestions available.
    {/if}
  </div>
</div>

<style>
  .species-prediction-item:focus {
    /* Custom focus styles for better visibility */
    background-color: hsl(var(--b2));
    outline: 2px solid hsl(var(--p));
    outline-offset: -2px;
  }

  /* Container positioning - portal dropdown is rendered to document.body */
  .species-input-container {
    position: relative;
  }

  /* Ensure long species names don't break layout */
  .species-prediction-item {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  /* Dropdown positioning classes for portal */
  :global(.dropdown-above) {
    /* Shadow pointing down when dropdown is above */
    box-shadow:
      0 4px 6px -1px rgb(0 0 0 / 0.1),
      0 2px 4px -2px rgb(0 0 0 / 0.1);
  }

  :global(.dropdown-below) {
    /* Shadow pointing up when dropdown is below (default) */
    box-shadow:
      0 10px 15px -3px rgb(0 0 0 / 0.1),
      0 4px 6px -2px rgb(0 0 0 / 0.05);
  }
</style>
