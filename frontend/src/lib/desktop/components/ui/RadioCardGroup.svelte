<!--
  RadioCardGroup.svelte

  A card-style single choice that follows the WAI-ARIA radio group pattern: one
  Tab stop for the whole group, arrow keys that move focus and selection, and
  Space (or Enter, as a click) to check the focused card.

  Usage:
  - Wizard steps that pick one of a few options with a title and a description
  - Any short single choice where each option needs more room than a plain radio

  Features:
  - Roving tabindex: the checked card, else the first enabled card, is the Tab stop
  - Arrow keys wrap and skip disabled cards; focus alone never selects
  - The parent owns the selection; onChange fires on every activation, including
    the checked card
  - Disabled cards stay focusable (aria-disabled) and show why

  Props:
  - options: RadioCardOption<T>[] - Cards, in order
  - value: T | null - The checked option's value, or null for none
  - onChange: (value: T) => void - Called when a card is activated
  - columns?: 1 | 2 - Grid columns (default 1)
  - className?: string - Extra classes for the radiogroup element
  - Other HTML attributes go to the radiogroup element; pass aria-label or aria-labelledby
-->
<script lang="ts" generics="T extends string">
  import { tick } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';
  import { cn } from '$lib/utils/cn';
  import type { RadioCardOption } from './RadioCardGroup.types';

  interface Props<T extends string> extends Omit<HTMLAttributes<HTMLDivElement>, 'role' | 'class'> {
    options: RadioCardOption<T>[];
    value: T | null;
    onChange: (_value: T) => void;
    columns?: 1 | 2;
    className?: string;
  }

  let { options, value, onChange, columns = 1, className = '', ...rest }: Props<T> = $props();

  const CARD_BASE_CLASS =
    'flex w-full gap-3 rounded-lg border-2 text-left transition-colors focus-visible:outline-2 focus-visible:outline-[var(--color-primary)] focus-visible:outline-offset-2';
  const BADGE_CLASS =
    'rounded-full bg-[var(--color-primary)]/10 px-2 py-0.5 text-xs font-medium text-[var(--color-base-content)]';

  let optionRefs = $state<HTMLButtonElement[]>([]);

  // A value that matches no option counts as nothing checked
  let checkedIndex = $derived(options.findIndex(o => value !== null && o.value === value));
  let firstEnabledIndex = $derived(options.findIndex(o => !o.disabled));
  // The one option in the Tab order: the checked one (even when disabled), else the
  // first enabled one, else the first, so the group and its reasons stay reachable
  let tabStopIndex = $derived(
    checkedIndex >= 0 ? checkedIndex : firstEnabledIndex >= 0 ? firstEnabledIndex : 0
  );
  // Cards of a group share their padding and icon alignment
  let hasDetails = $derived(options.some(o => o.description || o.detail));

  function cardClass(option: RadioCardOption<T>, checked: boolean): string {
    const layout = hasDetails ? 'items-start p-4' : 'items-center p-3';
    let look = 'border-[var(--border-200)] hover:border-[var(--border-300)]';
    if (checked) look = 'border-[var(--color-primary)] bg-[var(--color-primary)]/5';
    else if (option.disabled) look = 'border-[var(--border-200)]';
    return `${CARD_BASE_CLASS} ${layout} ${look}${option.disabled ? ' cursor-not-allowed' : ''}`;
  }

  function iconClass(option: RadioCardOption<T>, checked: boolean): string {
    const colour = checked
      ? 'text-[var(--color-primary)]'
      : `text-[var(--color-base-content)] ${option.disabled ? 'opacity-40' : 'opacity-70'}`;
    return `size-5 shrink-0 ${hasDetails ? 'mt-0.5 ' : ''}${colour}`;
  }

  // Every activation (click, Space, Enter, arrow) is reported, the checked option included
  function select(option: RadioCardOption<T>) {
    if (option.disabled) return;
    onChange(option.value);
  }

  function arrowStep(key: string): 1 | -1 | 0 {
    switch (key) {
      case 'ArrowDown':
      case 'ArrowRight':
        return 1;
      case 'ArrowUp':
      case 'ArrowLeft':
        return -1;
      default:
        return 0;
    }
  }

  // The next enabled option in the direction of step, wrapping, or -1 when no other is enabled
  function nextEnabledIndex(from: number, step: 1 | -1): number {
    const count = options.length;
    for (let offset = 1; offset < count; offset++) {
      const candidate = (from + step * offset + count) % count;
      if (!options.at(candidate)?.disabled) return candidate;
    }
    return -1;
  }

  async function onKey(event: KeyboardEvent, index: number) {
    // Leave browser and OS shortcuts such as Alt+Left alone
    if (event.altKey || event.ctrlKey || event.metaKey) return;
    const step = arrowStep(event.key);
    if (step === 0) return;
    // Before any await, or the page still scrolls
    event.preventDefault();
    const target = nextEnabledIndex(index, step);
    const option = options.at(target);
    if (target < 0 || option === undefined) return;
    select(option);
    // Focus after the new card is checked and is the Tab stop, so it is announced as checked
    await tick();
    optionRefs.at(target)?.focus();
  }
</script>

<div
  role="radiogroup"
  class={cn('grid gap-3', columns === 2 && 'grid-cols-2', className)}
  {...rest}
>
  {#each options as option, i (option.value)}
    {@const checked = i === checkedIndex}
    {@const Icon = option.icon}
    <button
      type="button"
      role="radio"
      bind:this={optionRefs[i]}
      aria-checked={checked}
      aria-disabled={option.disabled ? 'true' : undefined}
      tabindex={i === tabStopIndex ? 0 : -1}
      class={cardClass(option, checked)}
      onclick={() => select(option)}
      onkeydown={event => onKey(event, i)}
    >
      {#if Icon}
        <Icon class={iconClass(option, checked)} />
      {/if}
      <span class="flex-1">
        <span class="flex items-center gap-2">
          <span class="text-sm font-medium text-[var(--color-base-content)]">{option.label}</span>
          {#if option.badge}
            <span class={BADGE_CLASS}>{option.badge}</span>
          {/if}
        </span>
        {#if option.description}
          <span class="mt-0.5 block text-sm text-[var(--color-base-content)] opacity-80">
            {option.description}
          </span>
        {/if}
        {#if option.detail}
          <span class="mt-1 block font-mono text-sm text-[var(--color-base-content)] opacity-70">
            {option.detail}
          </span>
        {/if}
        {#if option.disabled}
          <span class="mt-1 block text-sm text-[var(--color-base-content)]">
            {option.disabledReason}
          </span>
        {/if}
      </span>
    </button>
  {/each}
</div>
