<!--
  LimitDropdown - small "how many to show" selector.

  Mirrors the limit dropdown used by the Recent Detections module so the two
  controls look and behave identically. Purely presentational: the caller owns
  the value and persistence.

  @component
-->
<script lang="ts">
  import { Check, ChevronDown } from '@lucide/svelte';
  import { onMount } from 'svelte';
  import { cn } from '$lib/utils/cn';
  import { dropdown } from '$lib/utils/transitions';

  interface Props {
    value: number;
    options: number[];
    ariaLabel: string;
    onChange: (_value: number) => void;
  }

  let { value, options, ariaLabel, onChange }: Props = $props();

  let open = $state(false);
  let menuRef = $state<HTMLDivElement | undefined>(undefined);
  let buttonRef = $state<HTMLButtonElement | undefined>(undefined);

  function select(option: number) {
    onChange(option);
    open = false;
  }

  function handleClickOutside(event: MouseEvent) {
    if (!open) return;
    const target = event.target as Node;
    if (!menuRef?.contains(target) && !buttonRef?.contains(target)) {
      open = false;
    }
  }

  function handleKeyDown(event: KeyboardEvent) {
    if (!open) {
      if (event.key === 'Enter' || event.key === ' ') {
        event.preventDefault();
        open = true;
      }
      return;
    }
    switch (event.key) {
      case 'Escape':
        open = false;
        buttonRef?.focus();
        break;
      case 'ArrowDown': {
        event.preventDefault();
        const next = options.at(Math.min(options.indexOf(value) + 1, options.length - 1));
        if (next !== undefined) select(next);
        break;
      }
      case 'ArrowUp': {
        event.preventDefault();
        const prev = options.at(Math.max(options.indexOf(value) - 1, 0));
        if (prev !== undefined) select(prev);
        break;
      }
    }
  }

  onMount(() => {
    document.addEventListener('click', handleClickOutside);
    return () => document.removeEventListener('click', handleClickOutside);
  });
</script>

<div class="limit-dropdown-container">
  <button
    bind:this={buttonRef}
    type="button"
    class="limit-dropdown-trigger"
    onclick={() => (open = !open)}
    onkeydown={handleKeyDown}
    aria-expanded={open}
    aria-haspopup="listbox"
    aria-label={`${ariaLabel} ${value}`}
  >
    <span class="limit-dropdown-value">{value}</span>
    <ChevronDown class={cn('limit-dropdown-icon', open && 'limit-dropdown-icon-open')} />
  </button>

  {#if open}
    <div
      bind:this={menuRef}
      in:dropdown={{ y: -4, duration: 120 }}
      out:dropdown={{ y: -4, duration: 80 }}
      class="limit-dropdown-menu"
      role="listbox"
      aria-label={ariaLabel}
    >
      {#each options as option (option)}
        <button
          type="button"
          class={cn('limit-dropdown-option', value === option && 'limit-dropdown-option-selected')}
          role="option"
          aria-selected={value === option}
          onclick={() => select(option)}
        >
          <span class="limit-dropdown-option-text">{option}</span>
          {#if value === option}
            <Check class="limit-dropdown-check" />
          {/if}
        </button>
      {/each}
    </div>
  {/if}
</div>

<style>
  .limit-dropdown-container {
    position: relative;
  }

  .limit-dropdown-trigger {
    display: inline-flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
    min-width: 4.5rem;
    padding: 0.5rem 0.75rem;
    font-size: 0.875rem;
    font-weight: 600;
    border-radius: 0.5rem;
    border: 1px solid var(--border-100);
    background-color: var(--color-base-100);
    color: var(--color-base-content);
    cursor: pointer;
    transition: all 150ms ease;
  }

  .limit-dropdown-trigger:hover {
    background-color: var(--color-base-200);
    border-color: var(--border-200);
  }

  .limit-dropdown-trigger:focus {
    outline: none;
  }

  .limit-dropdown-value {
    font-variant-numeric: tabular-nums;
  }

  .limit-dropdown-icon {
    width: 1rem;
    height: 1rem;
    color: var(--text-muted);
    transition: transform 200ms ease;
  }

  .limit-dropdown-icon-open {
    transform: rotate(180deg);
  }

  .limit-dropdown-menu {
    position: absolute;
    top: calc(100% + 0.25rem);
    right: 0;
    z-index: 100;
    min-width: 5rem;
    padding: 0.25rem;
    border-radius: 0.5rem;
    border: 1px solid var(--border-100);
    background-color: var(--color-base-100);
    box-shadow: var(--shadow-lg);
  }

  .limit-dropdown-option {
    display: flex;
    align-items: center;
    justify-content: space-between;
    width: 100%;
    padding: 0.5rem 0.75rem;
    font-size: 0.875rem;
    font-weight: 500;
    border-radius: 0.375rem;
    background-color: transparent;
    color: var(--color-base-content);
    cursor: pointer;
    transition: all 100ms ease;
  }

  .limit-dropdown-option:hover {
    background-color: var(--hover-overlay);
  }

  .limit-dropdown-option-selected {
    background-color: color-mix(in srgb, var(--color-primary) 10%, transparent);
    color: var(--color-primary);
  }

  .limit-dropdown-option-selected:hover {
    background-color: color-mix(in srgb, var(--color-primary) 15%, transparent);
  }

  :global([data-theme='dark']) .limit-dropdown-option-selected {
    background-color: color-mix(in srgb, var(--color-primary) 20%, transparent);
  }

  :global([data-theme='dark']) .limit-dropdown-option-selected:hover {
    background-color: color-mix(in srgb, var(--color-primary) 25%, transparent);
  }

  .limit-dropdown-option-text {
    font-variant-numeric: tabular-nums;
  }

  .limit-dropdown-check {
    width: 1rem;
    height: 1rem;
    color: var(--color-primary);
  }
</style>
