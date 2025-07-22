<script lang="ts">
  import { onMount } from 'svelte';
  import { cn } from '$lib/utils/cn';
  import { navigationIcons } from '$lib/utils/icons'; // Centralized icons - see icons.ts

  interface Props {
    value: string; // YYYY-MM-DD format
    onChange: (_date: string) => void;
    maxDate?: string;
    minDate?: string;
    className?: string;
    disabled?: boolean;
    placeholder?: string;
  }

  let {
    value,
    onChange,
    maxDate = new Date().toISOString().split('T')[0],
    minDate,
    className = '',
    disabled = false,
    placeholder = 'Select date',
  }: Props = $props();

  let showCalendar = $state(false);
  let displayMonth = $state(new Date(value || new Date().toISOString()));
  let calendarRef = $state<HTMLDivElement>();
  let buttonRef = $state<HTMLButtonElement>();

  // Get the selected date as a Date object (use noon to avoid timezone shifts)
  const selectedDate = $derived(value ? new Date(value + 'T12:00:00') : null);

  // Format the display text
  const displayText = $derived(() => {
    if (!selectedDate) return placeholder;
    return selectedDate.toLocaleDateString('en-US', {
      weekday: 'short',
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    });
  });

  // Get month name for calendar header
  const monthYearText = $derived(
    displayMonth.toLocaleDateString('en-US', {
      month: 'long',
      year: 'numeric',
    })
  );

  // Get calendar days
  const calendarDays = $derived(() => {
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

    const dateStr = date.toISOString().split('T')[0];

    if (minDate && dateStr < minDate) return false;
    if (maxDate && dateStr > maxDate) return false;

    return true;
  }

  // Check if a date is selected
  function isDateSelected(date: Date): boolean {
    if (!date || !selectedDate) return false;
    return date.toDateString() === selectedDate.toDateString();
  }

  // Check if a date is today
  function isToday(date: Date): boolean {
    if (!date) return false;
    return date.toDateString() === new Date().toDateString();
  }

  // Navigate months
  function goToPreviousMonth() {
    displayMonth = new Date(displayMonth.getFullYear(), displayMonth.getMonth() - 1, 1);
  }

  function goToNextMonth() {
    displayMonth = new Date(displayMonth.getFullYear(), displayMonth.getMonth() + 1, 1);
  }

  // Select a date
  function selectDate(date: Date) {
    if (!date || !isDateSelectable(date)) return;

    const dateStr = date.toISOString().split('T')[0];
    onChange(dateStr);
    showCalendar = false;
  }

  // Toggle calendar
  function toggleCalendar() {
    if (disabled) return;
    showCalendar = !showCalendar;
  }

  // Handle keyboard navigation
  function handleKeyDown(event: KeyboardEvent) {
    if (!showCalendar) {
      if (event.key === 'Enter' || event.key === ' ') {
        event.preventDefault();
        toggleCalendar();
      }
      return;
    }

    if (event.key === 'Escape') {
      event.preventDefault();
      showCalendar = false;
      buttonRef?.focus();
    }
  }

  // Click outside handler
  function handleClickOutside(event: MouseEvent) {
    if (!showCalendar) return;

    const target = event.target as Node;
    if (!calendarRef?.contains(target) && !buttonRef?.contains(target)) {
      showCalendar = false;
    }
  }

  onMount(() => {
    document.addEventListener('click', handleClickOutside);

    return () => {
      document.removeEventListener('click', handleClickOutside);
    };
  });

  // Week day headers
  const weekDays = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
</script>

<div class="relative datepicker-wrapper">
  <!-- Date Input Button -->
  <button
    bind:this={buttonRef}
    type="button"
    class={cn(
      'btn btn-sm',
      'flex items-center gap-2',
      'font-normal',
      disabled ? 'btn-disabled' : '',
      className
    )}
    onclick={toggleCalendar}
    onkeydown={handleKeyDown}
    {disabled}
    aria-label="Select date"
    aria-expanded={showCalendar}
    aria-haspopup="true"
  >
    <svg
      xmlns="http://www.w3.org/2000/svg"
      class="h-4 w-4"
      fill="none"
      viewBox="0 0 24 24"
      stroke="currentColor"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M8 7V3m8 4V3m-9 8h10M5 21h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z"
      />
    </svg>
    <span>{displayText()}</span>
  </button>

  <!-- Calendar Dropdown -->
  {#if showCalendar}
    <div
      bind:this={calendarRef}
      class="absolute z-50 mt-1 bg-base-100 border border-base-300 rounded-lg shadow-lg p-4 min-w-[280px]"
      role="dialog"
      aria-label="Date picker calendar"
    >
      <!-- Month Navigation -->
      <div class="flex items-center justify-between mb-4">
        <button
          type="button"
          class="btn btn-ghost btn-sm btn-circle"
          onclick={goToPreviousMonth}
          aria-label="Previous month"
        >
          {@html navigationIcons.arrowLeft}
        </button>

        <h3 class="text-sm font-semibold">
          {monthYearText}
        </h3>

        <button
          type="button"
          class="btn btn-ghost btn-sm btn-circle"
          onclick={goToNextMonth}
          aria-label="Next month"
        >
          {@html navigationIcons.arrowRight}
        </button>
      </div>

      <!-- Week Days -->
      <div class="grid grid-cols-7 gap-1 mb-2">
        {#each weekDays as day}
          <div class="text-center text-xs font-medium text-base-content/60 py-1">
            {day}
          </div>
        {/each}
      </div>

      <!-- Calendar Days -->
      <div class="grid grid-cols-7 gap-1">
        {#each calendarDays() as date}
          {#if date}
            <button
              type="button"
              class={cn(
                'relative h-8 w-8 rounded text-sm transition-colors',
                'hover:bg-base-200',
                isDateSelected(date) ? 'bg-primary text-primary-content font-semibold' : '',
                isToday(date) && !isDateSelected(date) ? 'bg-base-200 font-semibold' : '',
                !isDateSelectable(date)
                  ? 'text-base-content/30 cursor-not-allowed hover:bg-transparent'
                  : 'cursor-pointer'
              )}
              onclick={() => selectDate(date)}
              disabled={!isDateSelectable(date)}
              aria-label={date.toLocaleDateString()}
            >
              {date.getDate()}
              {#if isToday(date)}
                <div
                  class="absolute bottom-0.5 left-1/2 -translate-x-1/2 w-1 h-1 bg-primary rounded-full"
                ></div>
              {/if}
            </button>
          {:else}
            <div class="h-8 w-8"></div>
          {/if}
        {/each}
      </div>

      <!-- Today Button -->
      <div class="mt-4 pt-4 border-t border-base-200">
        <button
          type="button"
          class="btn btn-primary btn-sm btn-block"
          onclick={() => selectDate(new Date())}
          disabled={!isDateSelectable(new Date())}
        >
          Today
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
</style>
