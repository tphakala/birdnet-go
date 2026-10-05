<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { Snippet, Component } from 'svelte';
  import type { SelectOption, SelectDropdownVariant } from './SelectDropdown.types';
  import { X, ChevronDown, Check } from '@lucide/svelte';
  import { dropdown } from '$lib/utils/transitions';
  import { portal } from '$lib/utils/portal';
  import { computeAnchorPosition, type AnchorPosition } from '$lib/utils/anchorPosition';
  import { safeGet, safeArrayAccess, safeArraySpread } from '$lib/utils/security';
  import { t } from '$lib/i18n';

  interface Props {
    options: SelectOption[];
    value?: string | string[];
    placeholder?: string;
    multiple?: boolean;
    searchable?: boolean;
    clearable?: boolean;
    disabled?: boolean;
    required?: boolean;
    /** Optional id for the control element (for label association) */
    id?: string;
    label?: string;
    helpText?: string;
    /** Space-separated ids of extra elements that describe the trigger (in addition to helpText) */
    'aria-describedby'?: string;
    className?: string;
    dropdownClassName?: string;
    maxHeight?: number;
    maxSelections?: number;
    groupBy?: boolean;
    virtualScroll?: boolean;
    /** Visual style variant: 'select' (default, looks like native select) or 'button' */
    variant?: SelectDropdownVariant;
    /** Size of the dropdown trigger */
    size?: 'xs' | 'sm' | 'md' | 'lg';
    /** Font size of dropdown menu items */
    menuSize?: 'xs' | 'sm' | 'md';
    onChange?: (_value: string | string[]) => void;
    onSearch?: (_query: string) => void;
    onClear?: () => void;
    renderOption?: Snippet<[SelectOption]>;
    renderSelected?: Snippet<[SelectOption[]]>;
  }

  /** Check if icon is a Svelte component (function) or string */
  function isComponentIcon(icon: string | Component | undefined): icon is Component {
    return typeof icon === 'function';
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
    id,
    label,
    helpText,
    'aria-describedby': ariaDescribedBy,
    className = '',
    dropdownClassName = '',
    maxHeight = 300,
    maxSelections,
    groupBy = true,
    // virtualScroll = false, // Reserved for future implementation
    variant = 'select',
    size = 'sm',
    menuSize = 'sm',
    onChange,
    onSearch,
    onClear,
    renderOption,
    renderSelected,
  }: Props = $props();

  // Use provided id or generate unique field ID (only generate once)
  let generatedId = `select-dropdown-${Math.random().toString(36).substring(2, 11)}`;
  let fieldId = $derived(id || generatedId);

  // State - must be declared before derived values that use them
  let isOpen = $state(false);
  let searchQuery = $state('');
  let highlightedIndex = $state(-1);
  let dropdownElement = $state<HTMLDivElement>();
  let inputElement = $state<HTMLInputElement>();
  let buttonElement = $state<HTMLButtonElement>();

  // Gap in px between the trigger button and the dropdown menu.
  const DROPDOWN_OFFSET = 4;

  // Position state for the fixed, portaled dropdown. `width` tracks the trigger
  // width; the rest comes from the shared anchor/flip helper.
  let dropdownPosition = $state<AnchorPosition & { width: number }>({
    placement: 'below',
    top: 0,
    bottom: null,
    left: 0,
    width: 0,
  });

  // Portal target: nearest dialog ancestor (for focus containment) or body
  let portalTarget = $derived.by(
    () => (buttonElement?.closest('[role="dialog"]') as HTMLElement | null) ?? document.body
  );

  // Trigger description: the help text (when shown) followed by any caller-provided ids
  let triggerDescribedBy = $derived(
    [helpText ? `${fieldId}-help` : undefined, ariaDescribedBy].filter(Boolean).join(' ') ||
      undefined
  );

  // Accessible name for the open listbox when the `label` prop is not used: the text of
  // the labels associated with the trigger, read when the list opens.
  let externalLabelText = $state('');

  // Size classes for trigger (padding + font size)
  const sizeClasses = {
    xs: 'py-1 pl-2 pr-3 text-xs',
    sm: 'py-1.5 pl-2.5 pr-3 text-[0.8125rem]',
    md: 'py-2 pl-3 pr-3 text-sm',
    lg: 'py-3 pl-4 pr-3 text-base',
  };

  // Menu item size classes (font size matches trigger sizeClasses)
  const menuSizeClasses = {
    xs: 'text-xs py-1.5 px-2',
    sm: 'text-[0.8125rem] py-1.5 px-2.5',
    md: 'text-sm py-2 px-3',
  };

  // Shared focus ring style (theme-aware via color-mix)
  const focusRingClasses =
    'focus:outline-none focus:border-[var(--color-primary)] focus:shadow-[0_0_0_3px_color-mix(in_srgb,var(--color-primary)_10%,transparent)]';

  // Trigger button classes based on variant
  let triggerClasses = $derived(
    variant === 'select'
      ? cn(
          'w-full flex items-center justify-between text-left cursor-pointer leading-5',
          'bg-[var(--color-base-100)] text-[var(--color-base-content)]',
          'border border-[var(--border-100)] rounded-[var(--radius-field)]',
          'transition-all',
          'hover:border-[var(--border-200)]',
          focusRingClasses,
          safeGet(sizeClasses, size, ''),
          disabled && 'opacity-50 cursor-not-allowed bg-[var(--color-base-200)] pointer-events-none'
        )
      : cn(
          'inline-flex items-center justify-center gap-2 w-full justify-between',
          'py-2 px-4 text-sm font-medium leading-5',
          'bg-[var(--color-base-300)] text-[var(--color-base-content)]',
          'border border-transparent rounded-[var(--radius-field)]',
          'cursor-pointer transition-all select-none',
          'hover:bg-[var(--surface-400)]',
          'active:scale-[0.98]',
          'focus-visible:outline-2 focus-visible:outline-[var(--color-primary)] focus-visible:outline-offset-2',
          isOpen && 'bg-[var(--surface-400)]',
          disabled && 'opacity-50 cursor-not-allowed pointer-events-none'
        )
  );

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

  let filteredOptions = $derived.by(() => {
    if (!searchable || !searchQuery) return options;

    const query = searchQuery.toLowerCase();
    return options.filter(
      opt =>
        opt.label.toLowerCase().includes(query) ||
        opt.value.toLowerCase().includes(query) ||
        (opt.description && opt.description.toLowerCase().includes(query))
    );
  });

  let groupedOptions = $derived.by(() => {
    if (!groupBy) return { '': filteredOptions };

    return filteredOptions.reduce<Record<string, SelectOption[]>>((groups, option) => {
      const group = option.group || '';
      const existingGroup = safeGet(groups, group, []);
      Object.assign(groups, { [group]: safeArraySpread(existingGroup, [option]) });
      return groups;
    }, {});
  });

  // Options in the order they are rendered: grouped when groupBy is on, so keyboard
  // navigation, ids and aria-activedescendant agree with what is on screen
  let renderedOptions = $derived(groupBy ? Object.values(groupedOptions).flat() : filteredOptions);

  let canAddMore = $derived(
    !maxSelections ||
      !multiple ||
      (value && Array.isArray(value) ? (value as string[]).length : 0) < maxSelections
  );

  let displayText = $derived.by(() => {
    if (selectedOptions.length === 0) return placeholder;
    if (multiple) {
      return `${selectedOptions.length} selected`;
    }
    return selectedOptions[0].label;
  });

  // Calculate dropdown position based on the button element, using viewport
  // coordinates for fixed positioning. The flip decision uses the dropdown's real
  // rendered height once it is in the DOM, falling back to the maxHeight cap before
  // the first layout (so a short list no longer flips above prematurely).
  function updateDropdownPosition() {
    if (!buttonElement) return;

    const rect = buttonElement.getBoundingClientRect();
    const position = computeAnchorPosition({
      triggerRect: rect,
      floatingHeight: dropdownElement?.offsetHeight ?? 0,
      floatingWidth: rect.width,
      estimatedHeight: maxHeight,
      offset: DROPDOWN_OFFSET,
      align: 'start',
    });

    dropdownPosition = { ...position, width: rect.width };
  }

  // Event handlers
  function toggleDropdown() {
    if (disabled) return;
    isOpen = !isOpen;

    if (isOpen) {
      externalLabelText = Array.from(buttonElement?.labels ?? [])
        .map(associated => associated.textContent?.trim() ?? '')
        .filter(Boolean)
        .join(' ');
      updateDropdownPosition();
      if (searchable) {
        setTimeout(() => inputElement?.focus(), 0);
      }
    }
  }

  function closeDropdown() {
    // The popover is removed on close; keep focus from falling to <body> when it was inside
    if (dropdownElement?.contains(document.activeElement)) {
      buttonElement?.focus();
    }
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
      // The popover stays open, so a click that took no focus (Safari) must not leave focus on
      // <body>, outside the dialog's focus trap. The search box is where typing continues.
      if (document.activeElement === document.body) {
        (searchable ? inputElement : buttonElement)?.focus();
      }
    } else {
      value = option.value;
      onChange?.(option.value);
      // A click that took no focus (Safari) leaves it on <body>; keep it inside the dialog
      if (document.activeElement === document.body) buttonElement?.focus();
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

  // Move the highlighted option down (1) or up (-1), clamped to the list
  function moveHighlight(delta: 1 | -1) {
    // Nothing rendered to highlight, and aria-activedescendant must not name a missing option
    const lastIndex = renderedOptions.length - 1;
    if (lastIndex < 0) return;
    if (delta === 1) {
      highlightedIndex = highlightedIndex === -1 ? 0 : Math.min(highlightedIndex + 1, lastIndex);
    } else {
      // ArrowUp from nothing wraps to the last option; at the first option it stays there
      // A highlight left beyond a shrunken list counts as being below the last option
      highlightedIndex =
        highlightedIndex === -1 || highlightedIndex > lastIndex
          ? lastIndex
          : Math.max(highlightedIndex - 1, 0);
    }
    scrollToHighlighted();
  }

  // Keys from anywhere inside the popover (search box or a focused option). Escape must not
  // reach the document, where a surrounding Modal would treat it as its own close request.
  function handlePopoverKeyDown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      closeDropdown();
    } else if (event.key === 'Tab') {
      // No preventDefault: focus moves to the trigger and the browser's Tab continues from there
      closeDropdown();
    }
  }

  // Navigation keys for the search box only; Space and other keys keep typing text
  function handleSearchKeyDown(event: KeyboardEvent) {
    switch (event.key) {
      case 'ArrowDown':
        event.preventDefault();
        moveHighlight(1);
        break;

      case 'ArrowUp':
        event.preventDefault();
        moveHighlight(-1);
        break;

      case 'Enter': {
        // Always consumed, so Enter in the search box never submits a surrounding form
        event.preventDefault();
        const highlighted = safeArrayAccess(renderedOptions, highlightedIndex);
        if (highlightedIndex >= 0 && highlighted) selectOption(highlighted);
        break;
      }
    }
  }

  function handleKeyDown(event: KeyboardEvent) {
    const allOptions = renderedOptions;

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
          moveHighlight(1);
        }
        break;

      case 'ArrowUp':
        event.preventDefault();
        if (isOpen) {
          moveHighlight(-1);
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

    const highlighted = document.getElementById(`${fieldId}-option-${highlightedIndex}`);

    if (highlighted instanceof HTMLElement) {
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

  // Capture-phase scroll handler: reposition when an outer/ancestor container
  // scrolls (which moves the trigger), but ignore scrolls inside the dropdown's
  // own option list. Those don't move the trigger and would otherwise recompute
  // the same position on every scroll tick.
  function handleScroll(event: Event) {
    if (event.target instanceof Node && dropdownElement?.contains(event.target)) return;
    updateDropdownPosition();
  }

  // Native listener on the popover root rather than a Svelte `onkeydown`: Svelte delegates
  // keydown to the document, where stopPropagation could no longer keep Escape from other
  // document-level listeners (a surrounding Modal).
  $effect(() => {
    const popover = dropdownElement;
    if (!popover) return;
    popover.addEventListener('keydown', handlePopoverKeyDown);
    return () => popover.removeEventListener('keydown', handlePopoverKeyDown);
  });

  $effect(() => {
    if (isOpen) {
      // Re-measure now that the dropdown is in the DOM, so the flip decision uses
      // its real height rather than the pre-layout maxHeight estimate.
      updateDropdownPosition();
      document.addEventListener('click', handleClickOutside);
      window.addEventListener('scroll', handleScroll, true);
      window.addEventListener('resize', updateDropdownPosition);
      return () => {
        document.removeEventListener('click', handleClickOutside);
        window.removeEventListener('scroll', handleScroll, true);
        window.removeEventListener('resize', updateDropdownPosition);
      };
    }
  });
</script>

<div class={cn('flex flex-col', className)}>
  {#if label}
    <label class="flex items-center py-2" for={fieldId} id="{fieldId}-label">
      <span class="text-sm font-medium text-[var(--color-base-content)]">
        {label}
        {#if required}
          <span class="text-[var(--color-error)]">*</span>
        {/if}
      </span>
    </label>
  {/if}

  <div class="relative">
    <button
      bind:this={buttonElement}
      id={fieldId}
      type="button"
      class={triggerClasses}
      {disabled}
      onclick={toggleDropdown}
      onkeydown={handleKeyDown}
      aria-haspopup="listbox"
      aria-expanded={isOpen}
      aria-labelledby={label ? `${fieldId}-label` : undefined}
      aria-describedby={triggerDescribedBy}
    >
      <span class="flex items-center gap-2 truncate min-w-0">
        {#if renderSelected && selectedOptions.length > 0}
          {@render renderSelected(selectedOptions)}
        {:else if selectedOptions.length > 0 && !multiple}
          {#if selectedOptions[0].icon}
            {#if isComponentIcon(selectedOptions[0].icon)}
              {@const IconComponent = selectedOptions[0].icon}
              <IconComponent class="size-4 shrink-0" />
            {:else}
              <span class="text-base shrink-0">{selectedOptions[0].icon}</span>
            {/if}
          {/if}
          <span class="truncate">{selectedOptions[0].label}</span>
        {:else}
          <span class="truncate">{displayText}</span>
        {/if}
      </span>

      <div class="flex items-center gap-1 shrink-0">
        {#if clearable && selectedOptions.length > 0}
          <div
            role="button"
            tabindex="0"
            class="inline-flex items-center justify-center p-1 rounded-full aspect-square bg-transparent text-[var(--color-base-content)] cursor-pointer transition-all hover:bg-[color-mix(in_srgb,var(--color-base-content)_10%,transparent)] focus-visible:outline-2 focus-visible:outline-[var(--color-primary)] focus-visible:outline-offset-2"
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

        <div class={cn('transition-transform opacity-70', isOpen && 'rotate-180')}>
          <ChevronDown class="size-4" />
        </div>
      </div>
    </button>

    {#if isOpen}
      <div
        bind:this={dropdownElement}
        use:portal={portalTarget}
        in:dropdown={{ y: -4, duration: 120 }}
        out:dropdown={{ y: -4, duration: 80 }}
        class={cn(
          'fixed z-[2100] font-sans bg-[var(--color-base-100)] rounded-md shadow-xl border border-[var(--color-base-content)]/20 overflow-hidden',
          dropdownClassName
        )}
        style:top={dropdownPosition.top !== null ? `${dropdownPosition.top}px` : 'auto'}
        style:bottom={dropdownPosition.bottom !== null ? `${dropdownPosition.bottom}px` : 'auto'}
        style:left="{dropdownPosition.left}px"
        style:width="{dropdownPosition.width}px"
        style:max-height="{maxHeight}px"
      >
        {#if searchable}
          <div class="p-2 border-b border-[var(--color-base-300)]">
            <input
              bind:this={inputElement}
              type="text"
              bind:value={searchQuery}
              oninput={handleSearch}
              onkeydown={handleSearchKeyDown}
              placeholder={t('common.ui.search')}
              class={cn(
                'block w-full py-2 px-3 text-sm leading-5 bg-[var(--color-base-100)] text-[var(--color-base-content)] border border-[var(--border-100)] rounded-[var(--radius-field)] transition-all placeholder:text-[var(--color-base-content)] placeholder:opacity-50 hover:border-[var(--border-200)]',
                focusRingClasses
              )}
              aria-label={t('components.forms.select.searchOptions')}
              role="searchbox"
              aria-controls="{fieldId}-listbox"
              aria-activedescendant={highlightedIndex >= 0 &&
              highlightedIndex < renderedOptions.length
                ? `${fieldId}-option-${highlightedIndex}`
                : undefined}
            />
          </div>
        {/if}

        <div
          class="overflow-auto p-1"
          style:max-height="{searchable ? maxHeight - 60 : maxHeight}px"
          role="listbox"
          aria-multiselectable={multiple}
          id="{fieldId}-listbox"
          aria-labelledby={label ? `${fieldId}-label` : undefined}
          aria-label={label ? undefined : externalLabelText || undefined}
        >
          {#if filteredOptions.length === 0}
            <div class="p-4 text-center text-[var(--color-base-content)] opacity-60">
              {t('components.forms.select.noOptions')}
            </div>
          {:else}
            {@const optionIndexMap = new Map(
              renderedOptions.map((option, index) => [option, index])
            )}
            {#each Object.entries(groupedOptions) as [group, options] (group)}
              {#if group && groupBy}
                <div
                  class="px-3 py-2 text-xs font-semibold text-[var(--color-base-content)] opacity-60 uppercase"
                >
                  {group}
                </div>
              {/if}

              {#each options as option, optionIndex (`${group}-${option.value}-${optionIndex}`)}
                {@const flatIndex = optionIndexMap.get(option) ?? -1}
                <button
                  type="button"
                  id="{fieldId}-option-{flatIndex}"
                  class={cn(
                    'w-full text-left hover:bg-[var(--color-base-200)] focus:bg-[var(--color-base-200)] focus:outline-hidden flex items-center gap-2 rounded',
                    safeGet(menuSizeClasses, menuSize, ''),
                    isSelected(option) &&
                      'bg-[color-mix(in_srgb,var(--color-primary)_10%,transparent)] text-[var(--color-primary)]',
                    option.disabled && 'opacity-50 cursor-not-allowed',
                    highlightedIndex === flatIndex && 'bg-[var(--color-base-200)]'
                  )}
                  disabled={option.disabled}
                  onclick={() => selectOption(option)}
                  role="option"
                  aria-selected={isSelected(option)}
                >
                  <span class="size-4 shrink-0 flex items-center justify-center">
                    {#if isSelected(option)}
                      <Check class="size-4 text-[var(--color-primary)]" />
                    {/if}
                  </span>

                  {#if option.icon}
                    {#if isComponentIcon(option.icon)}
                      {@const IconComponent = option.icon}
                      <IconComponent class="size-4 shrink-0" />
                    {:else}
                      <span class="text-base shrink-0">{option.icon}</span>
                    {/if}
                  {/if}

                  <div class="flex-1">
                    {#if renderOption}
                      {@render renderOption(option)}
                    {:else}
                      <div>{option.label}</div>
                      {#if option.description}
                        <div class="text-xs text-[var(--color-base-content)] opacity-60">
                          {option.description}
                        </div>
                      {/if}
                    {/if}
                  </div>
                </button>
              {/each}
            {/each}
          {/if}
        </div>

        {#if multiple && maxSelections}
          <div
            class="p-2 border-t border-[var(--color-base-300)] text-xs text-[var(--color-base-content)] opacity-60"
          >
            {selectedOptions.length} / {maxSelections} selected
          </div>
        {/if}
      </div>
    {/if}
  </div>

  {#if helpText}
    <div class="flex items-center py-2" id="{fieldId}-help">
      <span class="help-text">{helpText}</span>
    </div>
  {/if}
</div>
