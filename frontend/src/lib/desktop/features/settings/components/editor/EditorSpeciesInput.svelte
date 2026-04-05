<!--
  EditorSpeciesInput - Species autocomplete input with portal dropdown for editor panels.

  Features:
  - Pure Tailwind styling (no DaisyUI)
  - Autocomplete with portal dropdown (correct z-index via document.body append)
  - Keyboard navigation (ArrowDown/Up to navigate, Enter/Space to select, Escape to close)
  - Click-outside handler to close dropdown
  - Smart above/below positioning with viewport boundary detection
  - Screen reader announcements for suggestions
  - Proper ARIA attributes

  @component
-->
<script lang="ts">
  import { Z_INDEX } from '$lib/utils/z-index';
  import { t } from '$lib/i18n';

  interface Props {
    value?: string;
    label?: string;
    predictions?: string[];
    placeholder?: string;
    disabled?: boolean;
    id?: string;
    onInput?: (_value: string) => void;
    onPredictionSelect?: (_prediction: string) => void;
  }

  let {
    value = $bindable(''),
    label,
    predictions = [],
    placeholder = '',
    disabled = false,
    id,
    onInput,
    onPredictionSelect,
  }: Props = $props();

  let showPredictions = $state(false);
  let manuallyDismissed = $state(false);
  let inputElement: HTMLInputElement;
  let portalDropdown: HTMLDivElement | null = null;

  // Generate a unique ID per instance to avoid conflicts with multiple instances
  let idCounter = 0;
  if (typeof window !== 'undefined') {
    const win = window as Window & { __editorSpeciesInputCounter?: number };
    if (!win.__editorSpeciesInputCounter) {
      win.__editorSpeciesInputCounter = 0;
    }
    idCounter = ++win.__editorSpeciesInputCounter;
  }
  const instanceId = `editor-species-predictions-${Date.now()}-${idCounter}`;

  const fieldId = $derived(
    id ||
      `editor-species-${
        label
          ? label
              .toLowerCase()
              .replace(/\s+/g, '-')
              .replace(/[^a-z0-9-]/g, '')
          : idCounter
      }`
  );

  // All predictions are pre-filtered by the parent; show them as-is
  let filteredPredictions = $derived(predictions ?? []);

  // Manage portal lifecycle based on prediction visibility
  $effect(() => {
    const hasPredictions = filteredPredictions.length > 0;

    if (!hasPredictions) {
      // No predictions — always close and reset dismissed flag
      if (portalDropdown) destroyPortalDropdown();
      showPredictions = false;
      manuallyDismissed = false;
      return;
    }

    // Has predictions but user manually dismissed — stay closed
    if (manuallyDismissed) return;

    if (!portalDropdown && inputElement) {
      createPortalDropdown();
    }
    if (portalDropdown) {
      updatePortalDropdown();
    }
    showPredictions = true;
  });

  // ── Portal event delegation ──────────────────────────────────────────────

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
      const index = parseInt(button.getAttribute('data-index') ?? '0', 10);
      if (prediction !== null) {
        handlePredictionKeydown(event, prediction, index);
      }
    }
  }

  // ── Portal creation / update / destruction ───────────────────────────────

  function createPortalDropdown() {
    if (!inputElement || portalDropdown) return;

    portalDropdown = document.createElement('div');
    portalDropdown.id = instanceId;
    portalDropdown.className =
      'bg-[var(--color-base-100)] border border-[var(--color-base-300)] rounded-lg shadow-lg max-h-60 overflow-y-auto';
    portalDropdown.style.position = 'absolute';
    portalDropdown.style.zIndex = Z_INDEX.PORTAL_DROPDOWN.toString();
    portalDropdown.setAttribute('role', 'listbox');
    portalDropdown.setAttribute('aria-label', label || placeholder || 'Species suggestions');

    portalDropdown.addEventListener('click', handlePortalClick);
    portalDropdown.addEventListener('keydown', handlePortalKeydown);

    document.body.appendChild(portalDropdown);
    updatePortalPosition();
  }

  function updatePortalDropdown() {
    if (!portalDropdown) return;

    const existingButtons = portalDropdown.querySelectorAll('.species-prediction-item');
    const predictionsCount = filteredPredictions.length;
    const existingCount = existingButtons.length;

    // Update existing buttons in place to avoid DOM churn
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

    // Append new buttons when list grows
    if (predictionsCount > existingCount) {
      for (let i = existingCount; i < predictionsCount; i++) {
        const button = document.createElement('button');
        button.type = 'button';
        button.className =
          'species-prediction-item w-full text-left px-4 py-2 hover:bg-[var(--color-base-200)] focus:bg-[var(--color-base-200)] focus:outline-hidden border-none bg-transparent text-sm';
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

    // Hide surplus buttons when list shrinks
    if (existingCount > predictionsCount) {
      for (let i = predictionsCount; i < existingCount; i++) {
        // eslint-disable-next-line security/detect-object-injection
        (existingButtons[i] as HTMLElement).style.display = 'none';
      }
    }

    updatePortalPosition();
  }

  function updatePortalPosition() {
    if (!portalDropdown || !inputElement) return;

    const rect = inputElement.getBoundingClientRect();
    const dropdownHeight = Math.min(240, filteredPredictions.length * 40);
    const viewportHeight = window.innerHeight;
    const spaceBelow = viewportHeight - rect.bottom;
    const spaceAbove = rect.top;

    // Prefer below; flip above only when there is more room above
    if (spaceBelow < dropdownHeight + 8 && spaceAbove > spaceBelow) {
      const topPosition = rect.top + window.scrollY - dropdownHeight - 4;
      portalDropdown.style.top = `${topPosition}px`;
      portalDropdown.style.bottom = 'auto';
      portalDropdown.classList.add('dropdown-above');
      portalDropdown.classList.remove('dropdown-below');
    } else {
      portalDropdown.style.top = `${rect.bottom + window.scrollY + 4}px`;
      portalDropdown.style.bottom = 'auto';
      portalDropdown.classList.add('dropdown-below');
      portalDropdown.classList.remove('dropdown-above');
    }

    const dropdownWidth = rect.width;
    const viewportWidth = window.innerWidth;
    let leftPosition = rect.left + window.scrollX;

    if (rect.left + dropdownWidth > viewportWidth) {
      leftPosition = viewportWidth - dropdownWidth - 8 + window.scrollX;
    }
    if (rect.left < 0) {
      leftPosition = 8 + window.scrollX;
    }

    portalDropdown.style.left = `${leftPosition}px`;
    portalDropdown.style.width = `${dropdownWidth}px`;
  }

  function destroyPortalDropdown() {
    if (portalDropdown) {
      portalDropdown.removeEventListener('click', handlePortalClick);
      portalDropdown.removeEventListener('keydown', handlePortalKeydown);
      if (portalDropdown.parentNode === document.body) {
        document.body.removeChild(portalDropdown);
      }
      portalDropdown = null;
    }
  }

  // ── Input handlers ───────────────────────────────────────────────────────

  function handleInput(event: Event) {
    const target = event.target as HTMLInputElement;
    value = target.value;
    manuallyDismissed = false; // Re-show suggestions on new input
    onInput?.(target.value);
  }

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault();
      showPredictions = false;
      destroyPortalDropdown();
      inputElement?.blur();
    } else if (event.key === 'ArrowDown' && showPredictions && portalDropdown) {
      event.preventDefault();
      const firstItem = portalDropdown.querySelector('.species-prediction-item') as HTMLElement;
      firstItem?.focus();
    }
  }

  function selectPrediction(prediction: string) {
    value = prediction;
    onPredictionSelect?.(prediction);
    showPredictions = false;
    destroyPortalDropdown();
    inputElement?.focus();
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
      destroyPortalDropdown();
      inputElement?.focus();
    }
  }

  // Close dropdown when clicking outside
  function handleDocumentClick(event: MouseEvent | TouchEvent) {
    const target = event.target as globalThis.Element;
    if (!target.closest(`#${fieldId}-wrapper`) && !target.closest(`#${instanceId}`)) {
      showPredictions = false;
      manuallyDismissed = true;
      destroyPortalDropdown();
    }
  }

  // Register document-level listeners and clean up on destroy
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
      destroyPortalDropdown();
    };
  });
</script>

<div>
  {#if label}
    <label for={fieldId} class="block text-xs font-medium text-[var(--color-base-content)]/60 mb-1">
      {label}
    </label>
  {/if}

  <div id="{fieldId}-wrapper">
    <input
      bind:this={inputElement}
      type="text"
      id={fieldId}
      bind:value
      {placeholder}
      {disabled}
      oninput={handleInput}
      onkeydown={handleKeydown}
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
      aria-label={label ?? placeholder}
      class="w-full px-3 py-2 rounded-lg text-sm bg-[var(--color-base-200)] border border-[var(--color-base-300)] text-[var(--color-base-content)] placeholder:text-[var(--color-base-content)]/40 outline-none focus:ring-2 focus:ring-[var(--color-primary)]/20 focus:border-[var(--color-primary)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
    />
  </div>

  <!-- Screen reader announcement for suggestion count changes -->
  <div class="sr-only" role="status" aria-atomic="true">
    {#if showPredictions && filteredPredictions.length > 0}
      {t('components.forms.species.suggestionsAvailable', { count: filteredPredictions.length })}
    {/if}
  </div>
</div>

<style>
  :global(.species-prediction-item:focus) {
    background-color: var(--color-base-200);
    outline: 2px solid var(--color-primary);
    outline-offset: -2px;
  }

  :global(.species-prediction-item) {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  :global(.dropdown-above) {
    box-shadow:
      0 4px 6px -1px rgb(0 0 0 / 0.1),
      0 2px 4px -2px rgb(0 0 0 / 0.1);
  }

  :global(.dropdown-below) {
    box-shadow:
      0 10px 15px -3px rgb(0 0 0 / 0.1),
      0 4px 6px -2px rgb(0 0 0 / 0.05);
  }
</style>
