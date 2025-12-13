<!--
DatePicker.svelte - Calendar date selection component

Purpose:
- Provides an accessible date selection interface with calendar dropdown
- Supports date validation, keyboard navigation, and range constraints
- Includes special "Today" button with optional custom behavior

Features:
- Full keyboard navigation (arrow keys, PageUp/Down, Home/End)
- Screen reader support with ARIA attributes and live regions
- Date range validation (min/max date constraints)
- Visual indicators for today's date and selected date
- Custom handler support for "Today" button click
- Responsive button sizing (xs, sm, md, lg)
- Automatic focus management
- Click-outside-to-close behavior

Props:
- value: string - Selected date in YYYY-MM-DD format
- onChange: (date: string) => void - Callback when date is selected
- onTodayClick?: () => void - Optional custom handler for Today button (e.g., to reset date persistence)
- maxDate?: string - Maximum selectable date (defaults to today)
- minDate?: string - Minimum selectable date
- className?: string - Additional CSS classes
- disabled?: boolean - Disable the date picker
- placeholder?: string - Placeholder text when no date selected
- size?: ButtonSize - Button size variant ('xs' | 'sm' | 'md' | 'lg')

Keyboard Navigation:
- Enter/Space: Open calendar (when button focused)
- Escape: Close calendar
- Arrow keys: Navigate days (left/right = ±1 day, up/down = ±7 days)
- PageUp/PageDown: Previous/next month
- Shift+PageUp/PageDown: Previous/next year
- Home/End: First/last day of month
- Enter/Space: Select focused date

Accessibility:
- ARIA labels for all interactive elements
- Live region announcements for navigation
- Keyboard instructions for screen readers
- Focus management and visual indicators
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { cn } from '$lib/utils/cn.js';
  import { Calendar, ChevronLeft, ChevronRight } from '@lucide/svelte';
  import { getLocalDateString } from '$lib/utils/date.js';
  import { t } from '$lib/i18n';
  import type { HTMLAttributes } from 'svelte/elements';

  type ButtonSize = 'xs' | 'sm' | 'md' | 'lg';

  interface Props extends HTMLAttributes<HTMLButtonElement> {
    value: string; // YYYY-MM-DD format
    onChange: (_date: string) => void;
    onTodayClick?: () => void; // Optional custom handler for Today button
    maxDate?: string;
    minDate?: string;
    className?: string;
    disabled?: boolean;
    placeholder?: string;
    size?: ButtonSize;
  }

  let {
    value,
    onChange,
    onTodayClick,
    maxDate = getLocalDateString(new Date()),
    minDate,
    className = '',
    disabled = false,
    placeholder = t('forms.placeholders.date'),
    size = 'sm',
    ...restProps
  }: Props = $props();

  // Date validation functions
  function isValidDateFormat(dateString: string): boolean {
    if (!dateString) return true; // Empty string is valid (no selection)
    const dateRegex = /^\d{4}-\d{2}-\d{2}$/;
    return dateRegex.test(dateString);
  }

  function isValidDate(dateString: string): boolean {
    if (!dateString) return true;
    if (!isValidDateFormat(dateString)) return false;

    const date = new Date(dateString + 'T12:00:00');
    return !isNaN(date.getTime()) && dateString === getLocalDateString(date);
  }

  // Validation state
  const isValueValid = $derived(isValidDate(value));
  const validationError = $derived.by(() => {
    if (!value) return null;
    if (!isValidDateFormat(value)) {
      return t('components.datePicker.feedback.invalidDateFormat');
    }
    if (!isValidDate(value)) {
      return t('components.datePicker.feedback.dateOutOfRange');
    }
    return null;
  });

  let showCalendar = $state(false);
  let displayMonth = $state(
    (() => {
      if (value && isValueValid) {
        try {
          return new Date(value + 'T12:00:00');
        } catch {
          return new Date();
        }
      }
      return new Date();
    })()
  );
  let calendarRef = $state<HTMLDivElement>();
  let buttonRef = $state<HTMLButtonElement>();

  // State for keyboard navigation focus
  let focusedDate = $state<Date | null>(null);
  let ariaMessage = $state<string>('');

  // Get the selected date as a Date object (use noon to avoid timezone shifts)
  const selectedDate = $derived.by(() => {
    if (!value || !isValueValid) return null;
    try {
      return new Date(value + 'T12:00:00');
    } catch {
      return null;
    }
  });

  // Format the display text
  const displayText = $derived.by(() => {
    if (validationError) {
      return t('common.validation.invalid');
    }
    if (!selectedDate) return placeholder;

    try {
      return selectedDate.toLocaleDateString(undefined, {
        weekday: 'short',
        year: 'numeric',
        month: 'short',
        day: 'numeric',
      });
    } catch {
      return t('common.validation.invalid');
    }
  });

  // Get month name for calendar header
  const monthYearText = $derived(
    displayMonth.toLocaleDateString(undefined, {
      month: 'long',
      year: 'numeric',
    })
  );

  // Get calendar days
  const calendarDays = $derived.by(() => {
    const year = displayMonth.getFullYear();
    const month = displayMonth.getMonth();
    const firstDay = new Date(year, month, 1);
    const lastDay = new Date(year, month + 1, 0);
    const daysInMonth = lastDay.getDate();
    const startingDayOfWeek = firstDay.getDay();

    const days: (Date | null)[] = [];

    // Add empty slots for days before month starts
    for (let i = 0; i < startingDayOfWeek; i++) {
      days.push(null);
    }

    // Add all days of the month
    for (let i = 1; i <= daysInMonth; i++) {
      days.push(new Date(year, month, i));
    }

    return days;
  });

  // Check if a date is selectable
  function isDateSelectable(date: Date): boolean {
    if (!date) return false;

    const dateStr = getLocalDateString(date);

    if (minDate && dateStr < minDate) return false;
    if (maxDate && dateStr > maxDate) return false;

    return true;
  }

  // Check if a date is selected
  function isDateSelected(date: Date): boolean {
    if (!date) return false;
    const selected = selectedDate;
    if (!selected) return false;
    return date.toDateString() === selected.toDateString();
  }

  // Check if a date is today
  function isToday(date: Date): boolean {
    if (!date) return false;
    return date.toDateString() === new Date().toDateString();
  }

  // Navigate months with aria announcements
  function goToPreviousMonth() {
    displayMonth = new Date(displayMonth.getFullYear(), displayMonth.getMonth() - 1, 1);
    ariaMessage = t('components.datePicker.aria.monthChanged', {
      month: displayMonth.toLocaleDateString(undefined, { month: 'long' }),
      year: displayMonth.getFullYear(),
    });
  }

  function goToNextMonth() {
    displayMonth = new Date(displayMonth.getFullYear(), displayMonth.getMonth() + 1, 1);
    ariaMessage = t('components.datePicker.aria.monthChanged', {
      month: displayMonth.toLocaleDateString(undefined, { month: 'long' }),
      year: displayMonth.getFullYear(),
    });
  }

  // Select a date
  function selectDate(date: Date) {
    if (!date || !isDateSelectable(date)) return;

    const dateStr = getLocalDateString(date);
    onChange(dateStr);
    showCalendar = false;
    buttonRef?.focus();
  }

  // Toggle calendar
  function toggleCalendar() {
    if (disabled) return;
    const opening = !showCalendar;
    showCalendar = opening;
    ariaMessage = opening
      ? t('components.datePicker.aria.calendarOpened')
      : t('components.datePicker.aria.calendarClosed');
    if (opening) {
      focusedDate = selectedDate || new Date();
    }
  }

  // Enhanced keyboard navigation
  function handleKeyDown(event: KeyboardEvent) {
    if (!showCalendar) {
      if (event.key === 'Enter' || event.key === ' ') {
        event.preventDefault();
        toggleCalendar();
        // Set initial focus to selected date or today
        const initialFocus = selectedDate || new Date();
        focusedDate = initialFocus;
        ariaMessage = t('components.datePicker.aria.calendarOpened');
      }
      return;
    }

    // Handle keyboard navigation within calendar
    const currentFocus = focusedDate || selectedDate || new Date();
    let newFocus: Date | null = null;
    let handled = true;

    switch (event.key) {
      case 'Escape':
        showCalendar = false;
        buttonRef?.focus();
        ariaMessage = t('components.datePicker.aria.calendarClosed');
        break;

      case 'ArrowLeft':
        newFocus = new Date(currentFocus);
        newFocus.setDate(newFocus.getDate() - 1);
        break;

      case 'ArrowRight':
        newFocus = new Date(currentFocus);
        newFocus.setDate(newFocus.getDate() + 1);
        break;

      case 'ArrowUp':
        newFocus = new Date(currentFocus);
        newFocus.setDate(newFocus.getDate() - 7);
        break;

      case 'ArrowDown':
        newFocus = new Date(currentFocus);
        newFocus.setDate(newFocus.getDate() + 7);
        break;

      case 'Home':
        newFocus = new Date(currentFocus.getFullYear(), currentFocus.getMonth(), 1);
        break;

      case 'End':
        newFocus = new Date(currentFocus.getFullYear(), currentFocus.getMonth() + 1, 0);
        break;

      case 'PageUp':
        event.preventDefault();
        if (event.shiftKey) {
          // Shift + PageUp = Previous year
          displayMonth = new Date(displayMonth.getFullYear() - 1, displayMonth.getMonth(), 1);
          ariaMessage = t('components.datePicker.aria.monthChanged', {
            month: displayMonth.toLocaleDateString(undefined, { month: 'long' }),
            year: displayMonth.getFullYear(),
          });
        } else {
          // PageUp = Previous month
          goToPreviousMonth();
        }
        break;

      case 'PageDown':
        event.preventDefault();
        if (event.shiftKey) {
          // Shift + PageDown = Next year
          displayMonth = new Date(displayMonth.getFullYear() + 1, displayMonth.getMonth(), 1);
          ariaMessage = t('components.datePicker.aria.monthChanged', {
            month: displayMonth.toLocaleDateString(undefined, { month: 'long' }),
            year: displayMonth.getFullYear(),
          });
        } else {
          // PageDown = Next month
          goToNextMonth();
        }
        break;

      case 'Enter':
      case ' ':
        if (focusedDate && isDateSelectable(focusedDate)) {
          selectDate(focusedDate);
          ariaMessage = t('components.datePicker.aria.dateSelected', {
            date: focusedDate.toLocaleDateString(),
          });
        } else if (focusedDate) {
          ariaMessage = t('components.datePicker.aria.dayUnavailable', {
            day: focusedDate.getDate(),
          });
        }
        break;

      default:
        handled = false;
    }

    if (handled) {
      event.preventDefault();
    }

    // Update focused date and ensure it's visible in current month
    if (newFocus) {
      // If navigation moves to different month, update display month
      if (
        newFocus.getMonth() !== displayMonth.getMonth() ||
        newFocus.getFullYear() !== displayMonth.getFullYear()
      ) {
        displayMonth = new Date(newFocus.getFullYear(), newFocus.getMonth(), 1);
        ariaMessage = t('components.datePicker.aria.monthChanged', {
          month: displayMonth.toLocaleDateString(undefined, { month: 'long' }),
          year: displayMonth.getFullYear(),
        });
      }

      focusedDate = newFocus;
    }
  }

  // Click outside handler
  function handleClickOutside(event: MouseEvent) {
    if (!showCalendar) return;

    const target = event.target as Node;
    if (!calendarRef?.contains(target) && !buttonRef?.contains(target)) {
      showCalendar = false;
      ariaMessage = t('components.datePicker.aria.calendarClosed');
      buttonRef?.focus();
    }
  }

  onMount(() => {
    document.addEventListener('click', handleClickOutside);

    return () => {
      document.removeEventListener('click', handleClickOutside);
    };
  });

  // Week day headers (localized)
  const weekDays = Array.from({ length: 7 }, (_, i) =>
    new Date(1970, 0, 4 + i).toLocaleDateString(undefined, { weekday: 'short' })
  );

  // Map size prop to padding/text size classes
  const sizeClass = $derived.by(() => {
    switch (size) {
      case 'xs':
        return 'px-2 py-1 text-xs';
      case 'sm':
        return 'px-3 py-1.5 text-sm';
      case 'md':
        return 'px-4 py-2 text-base';
      case 'lg':
        return 'px-5 py-2.5 text-lg';
      default:
        return 'px-3 py-1.5 text-sm';
    }
  });
</script>

<div class={cn('relative datepicker-wrapper', className)}>
  <!-- Date Input Button -->
  <button
    bind:this={buttonRef}
    type="button"
    {...restProps}
    class={cn('datepicker-trigger', sizeClass, disabled && 'opacity-50 cursor-not-allowed')}
    onclick={toggleCalendar}
    onkeydown={handleKeyDown}
    {disabled}
    aria-label={t('common.aria.selectDate')}
    aria-expanded={showCalendar}
    aria-haspopup="true"
  >
    <Calendar class="size-4" />
    <span class="truncate leading-normal">{displayText}</span>
  </button>

  <!-- Validation Error Display -->
  {#if validationError}
    <div class="datepicker-error" role="alert">
      {validationError}
    </div>
  {/if}

  <!-- Enhanced Aria Live Region for Screen Reader Announcements -->
  <div class="sr-only" aria-live="polite" aria-atomic="true">
    {#if ariaMessage}
      {ariaMessage}
    {/if}
  </div>

  <!-- Keyboard Navigation Instructions -->
  {#if showCalendar}
    <div
      class="sr-only"
      id="calendar-instructions"
      role="region"
      aria-label={t('common.aria.calendarNavigation')}
    >
      {t('common.aria.calendarNavigation')}
    </div>
  {/if}

  <!-- Calendar Dropdown -->
  {#if showCalendar}
    <div
      bind:this={calendarRef}
      class="datepicker-calendar"
      role="dialog"
      aria-label={t('common.aria.datePickerCalendar')}
    >
      <!-- Month Navigation -->
      <div class="flex items-center justify-between mb-4">
        <button
          type="button"
          class="datepicker-nav-btn"
          onclick={goToPreviousMonth}
          aria-label={t('common.aria.previousMonth')}
        >
          <ChevronLeft class="size-4" />
        </button>

        <h3 id="month-year-heading" class="text-sm font-semibold">
          {monthYearText}
        </h3>

        <button
          type="button"
          class="datepicker-nav-btn"
          onclick={goToNextMonth}
          aria-label={t('common.aria.nextMonth')}
        >
          <ChevronRight class="size-4" />
        </button>
      </div>

      <!-- Week Days -->
      <div class="grid grid-cols-7 gap-1 mb-2">
        {#each weekDays as day (day)}
          <div class="datepicker-weekday">
            {day}
          </div>
        {/each}
      </div>

      <!-- Calendar Days with Enhanced Keyboard Navigation -->
      <div
        class="grid grid-cols-7 gap-1"
        role="grid"
        aria-labelledby="month-year-heading"
        aria-describedby="calendar-instructions"
      >
        {#each calendarDays as date, i (date ? date.getTime() : `empty-${i}`)}
          {#if date}
            <button
              type="button"
              role="gridcell"
              class={cn(
                'datepicker-day',
                isDateSelected(date) && 'datepicker-day-selected',
                isToday(date) && !isDateSelected(date) && 'datepicker-day-today',
                !isDateSelectable(date) && 'datepicker-day-disabled',
                focusedDate &&
                  focusedDate.toDateString() === date.toDateString() &&
                  'datepicker-day-focused'
              )}
              tabindex={// Only the focused date (or selected date, or today if no focus) should be tabbable
              (focusedDate && focusedDate.toDateString() === date.toDateString()) ||
              (!focusedDate && isDateSelected(date)) ||
              (!focusedDate && !selectedDate && isToday(date))
                ? 0
                : -1}
              onclick={() => selectDate(date)}
              onkeydown={handleKeyDown}
              disabled={!isDateSelectable(date)}
              aria-label={[
                date.toLocaleDateString(),
                isDateSelected(date)
                  ? t('components.datePicker.aria.dateSelected', {
                      date: date.toLocaleDateString(),
                    })
                  : '',
                isToday(date) ? t('components.datePicker.today') : '',
                !isDateSelectable(date)
                  ? t('components.datePicker.aria.dayUnavailable', { day: date.getDate() })
                  : '',
              ]
                .filter(Boolean)
                .join(' ')}
              aria-selected={isDateSelected(date)}
              aria-current={isToday(date) ? 'date' : undefined}
            >
              {date.getDate()}
              {#if isToday(date)}
                <div class="datepicker-today-dot"></div>
              {/if}
            </button>
          {:else}
            <div class="h-8 w-8" role="gridcell" aria-hidden="true"></div>
          {/if}
        {/each}
      </div>

      <!-- Today Button -->
      <div class="datepicker-today-separator">
        <button
          type="button"
          class="datepicker-today-btn"
          onclick={() => {
            if (onTodayClick) {
              // Use custom handler if provided
              onTodayClick();
              showCalendar = false;
              buttonRef?.focus();
            } else {
              // Default behavior: just select today's date
              selectDate(new Date());
            }
          }}
          disabled={!isDateSelectable(new Date())}
          aria-label={t('components.datePicker.aria.todayButton', {
            today: new Date().toLocaleDateString(),
          })}
        >
          {t('components.datePicker.today')}
        </button>
      </div>
    </div>
  {/if}
</div>

<style>
  /* Ensure dropdown doesn't get cut off - scoped to this component */
  .datepicker-wrapper :global(.overflow-x-auto) {
    overflow: visible;
  }

  /* Trigger button - centered content */
  .datepicker-trigger {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 0.5rem;
    min-width: 11rem;
    font-weight: 400;
    border-radius: 0.5rem;
    border: 1px solid rgb(209 213 219); /* neutral-300 */
    background-color: rgb(255 255 255);
    color: rgb(17 24 39); /* neutral-900 */
    transition: all 150ms ease;
  }

  .datepicker-trigger:hover:not(:disabled) {
    background-color: rgb(249 250 251); /* neutral-50 */
    border-color: rgb(156 163 175); /* neutral-400 */
  }

  .datepicker-trigger:focus {
    outline: none;
  }

  /* Validation error */
  .datepicker-error {
    font-size: 0.75rem;
    margin-top: 0.25rem;
    color: rgb(239 68 68); /* red-500 */
  }

  :global([data-theme='dark']) .datepicker-error {
    color: rgb(248 113 113); /* red-400 */
  }

  :global([data-theme='dark']) .datepicker-trigger {
    background-color: rgb(30 41 59); /* slate-800 */
    border-color: rgb(71 85 105); /* slate-600 */
    color: rgb(241 245 249); /* slate-100 */
  }

  :global([data-theme='dark']) .datepicker-trigger:hover:not(:disabled) {
    background-color: rgb(51 65 85); /* slate-700 */
    border-color: rgb(100 116 139); /* slate-500 */
  }

  /* Calendar dropdown */
  .datepicker-calendar {
    position: absolute;
    right: 0;
    z-index: 50;
    margin-top: 0.25rem;
    min-width: 280px;
    padding: 1rem;
    border-radius: 0.5rem;
    border: 1px solid rgb(229 231 235); /* neutral-200 */
    background-color: rgb(255 255 255);
    box-shadow:
      0 10px 15px -3px rgba(0, 0, 0, 0.1),
      0 4px 6px -2px rgba(0, 0, 0, 0.05);
  }

  :global([data-theme='dark']) .datepicker-calendar {
    background-color: rgb(30 41 59); /* slate-800 */
    border-color: rgb(51 65 85); /* slate-700 */
    box-shadow:
      0 10px 15px -3px rgba(0, 0, 0, 0.3),
      0 4px 6px -2px rgba(0, 0, 0, 0.2);
  }

  /* Navigation buttons */
  .datepicker-nav-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 2rem;
    height: 2rem;
    border-radius: 9999px;
    background-color: transparent;
    color: rgb(75 85 99); /* neutral-600 */
    transition: all 150ms ease;
  }

  .datepicker-nav-btn:hover {
    background-color: rgb(243 244 246); /* neutral-100 */
    color: rgb(17 24 39); /* neutral-900 */
  }

  :global([data-theme='dark']) .datepicker-nav-btn {
    color: rgb(148 163 184); /* slate-400 */
  }

  :global([data-theme='dark']) .datepicker-nav-btn:hover {
    background-color: rgb(51 65 85); /* slate-700 */
    color: rgb(241 245 249); /* slate-100 */
  }

  /* Weekday headers */
  .datepicker-weekday {
    text-align: center;
    font-size: 0.75rem;
    font-weight: 500;
    padding: 0.25rem 0;
    color: rgb(107 114 128); /* neutral-500 */
  }

  :global([data-theme='dark']) .datepicker-weekday {
    color: rgb(148 163 184); /* slate-400 */
  }

  /* Calendar day buttons */
  .datepicker-day {
    position: relative;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 2rem;
    height: 2rem;
    border-radius: 0.25rem;
    font-size: 0.875rem;
    cursor: pointer;
    transition: all 150ms ease;
    color: rgb(17 24 39); /* neutral-900 */
  }

  .datepicker-day:hover:not(.datepicker-day-disabled) {
    background-color: rgb(243 244 246); /* neutral-100 */
  }

  :global([data-theme='dark']) .datepicker-day {
    color: rgb(241 245 249); /* slate-100 */
  }

  :global([data-theme='dark']) .datepicker-day:hover:not(.datepicker-day-disabled) {
    background-color: rgb(51 65 85); /* slate-700 */
  }

  .datepicker-day-selected {
    background-color: rgb(2 132 199) !important; /* sky-600 */
    color: rgb(255 255 255) !important;
    font-weight: 600;
  }

  .datepicker-day-today {
    background-color: rgb(229 231 235); /* neutral-200 */
    font-weight: 600;
  }

  :global([data-theme='dark']) .datepicker-day-today {
    background-color: rgb(51 65 85); /* slate-700 */
  }

  .datepicker-day-disabled {
    color: rgb(209 213 219); /* neutral-300 */
    cursor: not-allowed;
  }

  .datepicker-day-disabled:hover {
    background-color: transparent !important;
  }

  :global([data-theme='dark']) .datepicker-day-disabled {
    color: rgb(71 85 105); /* slate-600 */
  }

  .datepicker-day-focused {
    outline: 2px solid rgb(14 165 233); /* sky-500 */
    outline-offset: 1px;
  }

  /* Today indicator dot */
  .datepicker-today-dot {
    position: absolute;
    bottom: 0.125rem;
    left: 50%;
    transform: translateX(-50%);
    width: 0.25rem;
    height: 0.25rem;
    border-radius: 9999px;
    background-color: rgb(2 132 199); /* sky-600 */
  }

  /* Today button separator */
  .datepicker-today-separator {
    margin-top: 1rem;
    padding-top: 1rem;
    border-top: 1px solid rgb(229 231 235); /* neutral-200 */
  }

  :global([data-theme='dark']) .datepicker-today-separator {
    border-top-color: rgb(51 65 85); /* slate-700 */
  }

  /* Today button */
  .datepicker-today-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 100%;
    padding: 0.5rem 1rem;
    font-size: 0.875rem;
    font-weight: 500;
    border-radius: 0.375rem;
    background-color: rgb(2 132 199); /* sky-600 */
    color: rgb(255 255 255);
    transition: all 150ms ease;
  }

  .datepicker-today-btn:hover:not(:disabled) {
    background-color: rgb(3 105 161); /* sky-700 */
  }

  .datepicker-today-btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
</style>
