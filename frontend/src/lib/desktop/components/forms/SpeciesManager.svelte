<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { tick } from 'svelte';
  import {
    filterSpeciesForAutocomplete,
    validateSpecies,
    formatSpeciesName,
    sortSpecies,
  } from '$lib/utils/speciesUtils';
  import { actionIcons, navigationIcons } from '$lib/utils/icons'; // Centralized icons - see icons.ts

  interface Props {
    species?: string[];
    allowedSpecies?: string[];
    editable?: boolean;
    maxItems?: number;
    placeholder?: string;
    label?: string;
    helpText?: string;
    sortable?: boolean;
    className?: string;
    onChange?: (_species: string[]) => void;
    onValidate?: (_species: string) => boolean;
  }

  let {
    species = [],
    allowedSpecies = [],
    editable = true,
    maxItems,
    placeholder = 'Enter species name...',
    label,
    helpText,
    sortable = false,
    className = '',
    onChange,
    onValidate,
  }: Props = $props();

  // State
  let input = $state('');
  let predictions = $state<string[]>([]);
  let editingIndex = $state<number | null>(null);
  let editingValue = $state('');
  let draggedIndex = $state<number | null>(null);
  let dragOverIndex = $state<number | null>(null);
  // Generate unique IDs for accessibility - use crypto.randomUUID() with fallback
  let inputId = `species-input-${crypto?.randomUUID?.() ?? Math.random().toString(36).substr(2, 9)}`;
  let listId = `species-list-${crypto?.randomUUID?.() ?? Math.random().toString(36).substr(2, 9)}`;

  // Derived
  let canAddMore = $derived(!maxItems || species.length < maxItems);
  let displaySpecies = $derived(sortable ? sortSpecies(species) : species);

  // Update predictions when input changes
  $effect(() => {
    if (input.trim()) {
      predictions = filterSpeciesForAutocomplete(
        input,
        allowedSpecies.length > 0 ? allowedSpecies : [],
        species,
        5
      );
    } else {
      predictions = [];
    }
  });

  function addSpecies() {
    const trimmed = input.trim();

    if (!trimmed) return;

    // Check if already exists (case-insensitive)
    if (species.some(s => s.toLowerCase() === trimmed.toLowerCase())) {
      input = '';
      predictions = [];
      return;
    }

    // Validate if custom validator provided
    if (onValidate && !onValidate(trimmed)) {
      return;
    }

    // Validate against allowed list if provided
    if (allowedSpecies.length > 0 && !validateSpecies(trimmed, allowedSpecies)) {
      return;
    }

    // Add to list
    const newSpecies = [...species, trimmed];
    species = newSpecies;

    // Clear input
    input = '';
    predictions = [];

    // Notify parent
    onChange?.(newSpecies);
  }

  function removeSpecies(index: number) {
    const newSpecies = species.filter((_, i) => i !== index);
    species = newSpecies;
    onChange?.(newSpecies);
  }

  function startEdit(index: number) {
    editingIndex = index;
    editingValue = species[index];
    // Focus will be set in next tick via $effect
  }

  // Focus edit input when editing starts
  $effect(() => {
    if (editingIndex !== null) {
      // Use tick() to ensure DOM is updated before focusing
      tick().then(() => {
        const editInput = document.querySelector(
          `[data-edit-index="${editingIndex}"]`
        ) as HTMLInputElement;
        editInput?.focus();
      });
    }
  });

  function saveEdit() {
    if (editingIndex === null) return;

    const trimmed = editingValue.trim();
    if (!trimmed) {
      cancelEdit();
      return;
    }

    // Check for duplicates (excluding current item)
    const duplicate = species.some(
      (s, i) => i !== editingIndex && s.toLowerCase() === trimmed.toLowerCase()
    );

    if (duplicate) {
      cancelEdit();
      return;
    }

    // Update species
    const newSpecies = [...species];
    newSpecies[editingIndex] = trimmed;
    species = newSpecies;

    // Reset edit state
    editingIndex = null;
    editingValue = '';

    onChange?.(newSpecies);
  }

  function cancelEdit() {
    editingIndex = null;
    editingValue = '';
  }

  function handleKeyDown(event: KeyboardEvent) {
    if (event.key === 'Enter') {
      event.preventDefault();
      addSpecies();
    }
  }

  function handleEditKeyDown(event: KeyboardEvent) {
    if (event.key === 'Enter') {
      event.preventDefault();
      saveEdit();
    } else if (event.key === 'Escape') {
      event.preventDefault();
      cancelEdit();
    }
  }

  // Drag and drop handlers
  function handleDragStart(event: DragEvent, index: number) {
    if (!editable || !sortable) return;

    draggedIndex = index;
    if (event.dataTransfer) {
      event.dataTransfer.effectAllowed = 'move';
      event.dataTransfer.setData('text/html', ''); // Firefox requires this
    }
  }

  function handleDragOver(event: DragEvent, index: number) {
    if (!editable || !sortable || draggedIndex === null) return;

    event.preventDefault();
    dragOverIndex = index;
  }

  function handleDragEnd() {
    if (!editable || !sortable || draggedIndex === null || dragOverIndex === null) return;

    if (draggedIndex !== dragOverIndex) {
      const newSpecies = [...species];
      const [removed] = newSpecies.splice(draggedIndex, 1);
      newSpecies.splice(dragOverIndex, 0, removed);
      species = newSpecies;
      onChange?.(newSpecies);
    }

    draggedIndex = null;
    dragOverIndex = null;
  }

  function selectPrediction(prediction: string) {
    input = prediction;
    predictions = [];
    addSpecies();
  }

  // Keyboard handler for accessible drag and drop
  function handleItemKeyDown(event: KeyboardEvent, index: number) {
    if (!editable || !sortable) return;

    // Use arrow keys to reorder items
    if (event.key === 'ArrowUp' && index > 0) {
      event.preventDefault();
      const newSpecies = [...species];
      const [item] = newSpecies.splice(index, 1);
      newSpecies.splice(index - 1, 0, item);
      species = newSpecies;
      onChange?.(newSpecies);
    } else if (event.key === 'ArrowDown' && index < species.length - 1) {
      event.preventDefault();
      const newSpecies = [...species];
      const [item] = newSpecies.splice(index, 1);
      newSpecies.splice(index + 1, 0, item);
      species = newSpecies;
      onChange?.(newSpecies);
    }
  }
</script>

<div class={cn('species-manager', className)}>
  {#if label}
    <label for={inputId} class="label">
      <span class="label-text">{label}</span>
    </label>
  {/if}

  {#if editable && canAddMore}
    <div class="form-control">
      <div class="relative">
        <input
          id={inputId}
          type="text"
          bind:value={input}
          onkeydown={handleKeyDown}
          {placeholder}
          class="input input-bordered w-full"
          list={listId}
        />

        {#if predictions.length > 0}
          <div
            class="absolute z-10 w-full mt-1 bg-base-100 rounded-lg shadow-lg border border-base-300 max-h-48 overflow-auto"
          >
            {#each predictions as prediction}
              <button
                type="button"
                onclick={() => selectPrediction(prediction)}
                class="w-full px-4 py-2 text-left hover:bg-base-200 focus:bg-base-200 focus:outline-none"
              >
                {formatSpeciesName(prediction)}
              </button>
            {/each}
          </div>
        {/if}
      </div>

      {#if helpText}
        <div class="label">
          <span class="label-text-alt">{helpText}</span>
        </div>
      {/if}
    </div>
  {/if}

  {#if species.length > 0}
    <div class="mt-4 space-y-2">
      {#each displaySpecies as item, index}
        <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
        <div
          class={cn(
            'flex items-center gap-2 p-2 bg-base-200 rounded-lg',
            dragOverIndex === index && 'ring-2 ring-primary'
          )}
          draggable={editable && sortable}
          ondragstart={e => handleDragStart(e, index)}
          ondragover={e => handleDragOver(e, index)}
          ondragend={handleDragEnd}
          role={editable && sortable ? 'button' : 'listitem'}
          tabindex={editable && sortable ? 0 : undefined}
          aria-label={editable && sortable
            ? `Drag to reorder ${formatSpeciesName(item)}`
            : undefined}
          onkeydown={editable && sortable ? e => handleItemKeyDown(e, index) : undefined}
        >
          {#if editable && sortable}
            {@html navigationIcons.dragHandle}
          {/if}

          <div class="flex-1">
            {#if editingIndex === index}
              <input
                type="text"
                bind:value={editingValue}
                onkeydown={handleEditKeyDown}
                onblur={saveEdit}
                class="input input-sm input-bordered w-full"
                data-edit-index={index}
              />
            {:else}
              <span class="text-sm">{formatSpeciesName(item)}</span>
            {/if}
          </div>

          {#if editable}
            <div class="flex gap-1">
              {#if editingIndex !== index}
                <button
                  type="button"
                  onclick={() => startEdit(index)}
                  class="btn btn-ghost btn-xs"
                  aria-label="Edit species"
                >
                  {@html actionIcons.edit}
                </button>
              {/if}

              <button
                type="button"
                onclick={() => removeSpecies(index)}
                class="btn btn-ghost btn-xs text-error"
                aria-label="Remove species"
              >
                {@html navigationIcons.close}
              </button>
            </div>
          {/if}
        </div>
      {/each}
    </div>
  {:else if !editable}
    <p class="text-base-content/60 italic">No species added</p>
  {/if}

  {#if maxItems && species.length >= maxItems}
    <p class="text-sm text-warning mt-2">
      Maximum of {maxItems} species reached
    </p>
  {/if}
</div>
