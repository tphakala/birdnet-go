<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Snippet } from 'svelte';
  import type { SelectOption } from './SelectDropdown.types';
  import { X, ChevronDown } from '@lucide/svelte';
  import {
    safeGet,
    safeArrayAccess,
    safeArraySpread,
    safeElementAccess,
  } from '$lib/utils/security';

  interface Props {
    options: SelectOption[];
    value?: string | string[];
    placeholder?: string;
    multiple?: boolean;
    searchable?: boolean;
    clearable?: boolean;
    disabled?: boolean;
    required?: boolean;
    label?: string;
    helpText?: string;
    className?: string;
    dropdownClassName?: string;
    maxHeight?: number;
    maxSelections?: number;
    groupBy?: boolean;
    virtualScroll?: boolean;
    onChange?: (_value: string | string[]) => void;
    onSearch?: (_query: string) => void;
    onClear?: () => void;
    renderOption?: Snippet<[SelectOption]>;
    renderSelected?: Snippet<[SelectOption[]]>;
  }

  let {
    options = [],
    value = $bindable(),
    placeholder = 'Select...',
    multiple = false,
    searchable = false,
    clearable = false,
    disabled = false,
    required = false,
    label,
    helpText,
    className = '',
    dropdownClassName = '',
    maxHeight = 300,
    maxSelections,
    groupBy = true,
    // virtualScroll = false, // Reserved for future implementation
    onChange,
    onSearch,
    onClear,
    renderOption,
    renderSelected,
  }: Props = $props();

  // Generate unique field ID
  let fieldId = `select-dropdown-${Math.random().toString(36).substring(2, 11)}`;

  // Initialize value based on multiple prop and handle type changes
  $effect(() => {
    if (value === undefined) {
      value = multiple ? [] : '';
    } else {
      // Reset value type when multiple prop changes
      if (multiple && !Array.isArray(value)) {
        value = [];
      } else if (!multiple && Array.isArray(value)) {
        value = '';
      }
    }
  });

  // State
  let isOpen = $state(false);
  let searchQuery = $state('');
  let highlightedIndex = $state(-1);
  let dropdownElement = $state<HTMLDivElement>();
  let inputElement = $state<HTMLInputElement>();
  let buttonElement = $state<HTMLButtonElement>();

  // Computed values
  let selectedOptions = $derived(
    multiple
      ? options.filter(
          opt => value && Array.isArray(value) && (value as string[]).includes(opt.value)
        )
      : value
        ? options.filter(opt => opt.value === value)
        : []
  );

  let filteredOptions = $derived(() => {
    if (!searchable || !searchQuery) return options;

    const query = searchQuery.toLowerCase();
    return options.filter(
      opt =>
        opt.label.toLowerCase().includes(query) ||
        opt.value.toLowerCase().includes(query) ||
        (opt.description && opt.description.toLowerCase().includes(query))
    );
  });

  let groupedOptions = $derived(() => {
    if (!groupBy) return { '': filteredOptions() };

    return filteredOptions().reduce(
      (groups, option) => {
        const group = option.group || '';
        const existingGroup = safeGet(groups, group, []);
        Object.assign(groups, { [group]: safeArraySpread(existingGroup, [option]) });
        return groups;
      },
      {} as Record<string, SelectOption[]>
    );
  });

  let canAddMore = $derived(
    !maxSelections ||
      !multiple ||
      (value && Array.isArray(value) ? (value as string[]).length : 0) < maxSelections
  );

  let displayText = $derived(() => {
    if (selectedOptions.length === 0) return placeholder;
    if (multiple) {
      return `${selectedOptions.length} selected`;
    }
    return selectedOptions[0].label;
  });

  // Event handlers
  function toggleDropdown() {
    if (disabled) return;
    isOpen = !isOpen;

    if (isOpen && searchable) {
      setTimeout(() => inputElement?.focus(), 0);
    }
  }

  function closeDropdown() {
    isOpen = false;
    searchQuery = '';
    highlightedIndex = -1;
  }

  function selectOption(option: SelectOption) {
    if (option.disabled || disabled) return;

    if (multiple) {
      const currentValues = (value && Array.isArray(value) ? value : []) as string[];
      let newValues: string[];

      if (currentValues.includes(option.value)) {
        // Remove if already selected
        newValues = currentValues.filter(v => v !== option.value);
      } else if (canAddMore) {
        // Add if not selected and can add more
        newValues = [...currentValues, option.value];
      } else {
        return; // Can't add more
      }

      value = newValues;
      onChange?.(newValues);
    } else {
      value = option.value;
      onChange?.(option.value);
      closeDropdown();
    }
  }

  function clearSelection() {
    if (disabled) return;

    value = multiple ? [] : '';
    onChange?.(multiple ? [] : '');
    onClear?.();
    closeDropdown();
  }

  function handleSearch(event: Event) {
    const target = event.target as HTMLInputElement;
    searchQuery = target.value;
    onSearch?.(searchQuery);
    highlightedIndex = -1;
  }

  function handleKeyDown(event: KeyboardEvent) {
    const allOptions = filteredOptions();

    switch (event.key) {
      case 'Escape':
        if (isOpen) {
          event.preventDefault();
          event.stopPropagation();
          closeDropdown();
        }
        break;

      case 'Enter':
      case ' ':
        if (!isOpen) {
          event.preventDefault();
          toggleDropdown();
        } else if (highlightedIndex >= 0 && highlightedIndex < allOptions.length) {
          event.preventDefault();
          const selectedOption = safeArrayAccess(allOptions, highlightedIndex);
          if (selectedOption) {
            selectOption(selectedOption);
          }
        }
        break;

      case 'ArrowDown':
        event.preventDefault();
        if (!isOpen) {
          toggleDropdown();
        } else {
          highlightedIndex =
            highlightedIndex === -1 ? 0 : Math.min(highlightedIndex + 1, allOptions.length - 1);
          scrollToHighlighted();
        }
        break;

      case 'ArrowUp':
        event.preventDefault();
        if (isOpen) {
          highlightedIndex = Math.max(highlightedIndex - 1, -1);
          scrollToHighlighted();
        }
        break;

      case 'Tab':
        if (isOpen) {
          closeDropdown();
        }
        break;
    }
  }

  function scrollToHighlighted() {
    if (highlightedIndex < 0 || !dropdownElement) return;

    const options = dropdownElement.querySelectorAll('[role="option"]');
    const highlighted = safeElementAccess<HTMLElement>(options, highlightedIndex, HTMLElement);

    if (highlighted) {
      highlighted.scrollIntoView({ block: 'nearest' });
    }
  }

  function isSelected(option: SelectOption): boolean {
    if (multiple) {
      return Boolean(value && Array.isArray(value) && (value as string[]).includes(option.value));
    }
    return value === option.value;
  }

  // Click outside handler
  function handleClickOutside(event: MouseEvent) {
    const target = event.target as Node;
    if (!buttonElement?.contains(target) && !dropdownElement?.contains(target)) {
      closeDropdown();
    }
  }

  $effect(() => {
    if (isOpen) {
      document.addEventListener('click', handleClickOutside);
      return () => {
        document.removeEventListener('click', handleClickOutside);
      };
    }
  });
</script>

<div class={cn('select-dropdown', className)}>
  {#if label}
    <label class="label" for={fieldId} id="{fieldId}-label">
      <span class="label-text">
        {label}
        {#if required}
          <span class="text-error">*</span>
        {/if}
      </span>
    </label>
  {/if}

  <div class="relative">
    <button
      bind:this={buttonElement}
      id={fieldId}
      type="button"
      class={cn(
        'btn btn-block justify-between',
        isOpen && 'btn-active',
        disabled && 'btn-disabled'
      )}
      {disabled}
      onclick={toggleDropdown}
      onkeydown={handleKeyDown}
      aria-haspopup="listbox"
      aria-expanded={isOpen}
      aria-labelledby={label ? `${fieldId}-label` : undefined}
      aria-describedby={helpText ? `${fieldId}-help` : undefined}
    >
      <span class="truncate">
        {#if renderSelected && selectedOptions.length > 0}
          {@render renderSelected(selectedOptions)}
        {:else}
          {displayText()}
        {/if}
      </span>

      <div class="flex items-center gap-1">
        {#if clearable && selectedOptions.length > 0}
          <div
            role="button"
            tabindex="0"
            class="btn btn-ghost btn-xs btn-circle"
            onclick={e => {
              e.stopPropagation();
              clearSelection();
            }}
            onkeydown={e => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault();
                e.stopPropagation();
                clearSelection();
              }
            }}
            aria-label="Clear selection"
          >
            <X class="size-4" />
          </div>
        {/if}

        <div class={cn('transition-transform', isOpen && 'rotate-180')}>
          <ChevronDown class="size-4" />
        </div>
      </div>
    </button>

    {#if isOpen}
      <div
        bind:this={dropdownElement}
        class={cn(
          'absolute z-50 w-full mt-1 bg-base-100 rounded-lg shadow-lg border border-base-300 overflow-hidden',
          dropdownClassName
        )}
        style:max-height="{maxHeight}px"
      >
        {#if searchable}
          <div class="p-2 border-b border-base-300">
            <input
              bind:this={inputElement}
              type="text"
              bind:value={searchQuery}
              oninput={handleSearch}
              placeholder="Search..."
              class="input input-sm input-bordered w-full"
              aria-label="Search options"
              role="searchbox"
              aria-controls="{fieldId}-listbox"
            />
          </div>
        {/if}

        <div
          class="overflow-auto"
          style:max-height="{searchable ? maxHeight - 60 : maxHeight}px"
          role="listbox"
          aria-multiselectable={multiple}
          id="{fieldId}-listbox"
          aria-labelledby={label ? `${fieldId}-label` : undefined}
        >
          {#if filteredOptions().length === 0}
            <div class="p-4 text-center text-base-content/60">No options found</div>
          {:else}
            {@const flatOptions = filteredOptions()}
            {@const optionIndexMap = new Map(flatOptions.map((option, index) => [option, index]))}
            {#each Object.entries(groupedOptions()) as [group, groupOptions]}
              {#if group && groupBy}
                <div class="px-3 py-2 text-xs font-semibold text-base-content/60 uppercase">
                  {group}
                </div>
              {/if}

              {#each groupOptions as option}
                {@const flatIndex = optionIndexMap.get(option) ?? -1}
                <button
                  type="button"
                  class={cn(
                    'w-full px-3 py-2 text-left hover:bg-base-200 focus:bg-base-200 focus:outline-none flex items-center gap-2',
                    isSelected(option) && 'bg-primary/10 text-primary',
                    option.disabled && 'opacity-50 cursor-not-allowed',
                    highlightedIndex === flatIndex && 'bg-base-200'
                  )}
                  disabled={option.disabled}
                  onclick={() => selectOption(option)}
                  role="option"
                  aria-selected={isSelected(option)}
                >
                  {#if multiple}
                    <input
                      type="checkbox"
                      checked={isSelected(option)}
                      disabled={option.disabled}
                      class="checkbox checkbox-sm"
                      tabindex="-1"
                    />
                  {/if}

                  {#if option.icon}
                    <span class="text-lg">{option.icon}</span>
                  {/if}

                  <div class="flex-1">
                    {#if renderOption}
                      {@render renderOption(option)}
                    {:else}
                      <div class="font-medium">{option.label}</div>
                      {#if option.description}
                        <div class="text-xs text-base-content/60">{option.description}</div>
                      {/if}
                    {/if}
                  </div>
                </button>
              {/each}
            {/each}
          {/if}
        </div>

        {#if multiple && maxSelections}
          <div class="p-2 border-t border-base-300 text-xs text-base-content/60">
            {selectedOptions.length} / {maxSelections} selected
          </div>
        {/if}
      </div>
    {/if}
  </div>

  {#if helpText}
    <div class="label" id="{fieldId}-help">
      <span class="label-text-alt">{helpText}</span>
    </div>
  {/if}
</div>
