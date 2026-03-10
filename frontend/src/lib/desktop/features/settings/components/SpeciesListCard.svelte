<!--
  SpeciesListCard.svelte

  Purpose: Card for managing Include/Exclude species lists with system page
  design language (surface tokens, icon pills, text-muted). Integrates
  SpeciesInput for adding new species.

  Props:
  - title: string - Card title
  - species: string[] - Current species list
  - icon: Component - Lucide icon for the header pill
  - iconColorClass: string - Tailwind color class for icon pill (e.g. 'emerald', 'red')
  - predictions: string[] - Autocomplete predictions
  - inputValue: string - Current input value (bindable)
  - inputLabel: string - Label for the input
  - inputPlaceholder: string - Placeholder for the input
  - emptyMessage: string - Message when list is empty
  - disabled: boolean - Disable all interactions
  - onAdd: (species: string) => void - Add species callback
  - onRemove: (species: string) => void - Remove species callback
  - onInput: (input: string) => void - Input change callback for predictions

  @component
-->
<script lang="ts">
  import type { Component } from 'svelte';
  import type { IconProps } from '@lucide/svelte';
  import { Trash2 } from '@lucide/svelte';
  import SpeciesInput from '$lib/desktop/components/forms/SpeciesInput.svelte';

  interface Props {
    title: string;
    species: string[];
    icon: Component<IconProps>;
    iconColorClass?: string;
    predictions: string[];
    inputValue: string;
    inputLabel: string;
    inputPlaceholder: string;
    emptyMessage: string;
    disabled?: boolean;
    onAdd: (_species: string) => void;
    onRemove: (_species: string) => void;
    onInput: (_input: string) => void;
  }

  let {
    title,
    species,
    icon: Icon,
    iconColorClass = 'emerald',
    predictions,
    inputValue = $bindable(),
    inputLabel,
    inputPlaceholder,
    emptyMessage,
    disabled = false,
    onAdd,
    onRemove,
    onInput,
  }: Props = $props();

  const colorMap: Record<string, { bg: string; text: string }> = {
    emerald: { bg: 'bg-emerald-500/10', text: 'text-emerald-500' },
    red: { bg: 'bg-red-500/10', text: 'text-red-500' },
    orange: { bg: 'bg-orange-500/10', text: 'text-orange-500' },
    blue: { bg: 'bg-blue-500/10', text: 'text-blue-500' },
    teal: { bg: 'bg-teal-500/10', text: 'text-teal-500' },
  };

  let colors = $derived(colorMap[iconColorClass] ?? colorMap.emerald);
</script>

<div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl shadow-sm">
  <!-- Header -->
  <div class="flex items-center justify-between px-4 py-3 border-b border-[var(--border-100)]">
    <div class="flex items-center gap-2">
      <div class="p-1.5 rounded-lg {colors.bg}">
        <Icon class="w-4 h-4 {colors.text}" />
      </div>
      <h3 class="text-xs font-semibold uppercase tracking-wider text-muted">{title}</h3>
      <span
        class="inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-medium bg-slate-500/10 text-muted"
      >
        {species.length}
      </span>
    </div>
  </div>

  <!-- Species List -->
  <div class="p-4 space-y-2">
    {#if species.length > 0}
      {#each species as item (item)}
        <div
          class="flex items-center justify-between px-3 py-2 rounded-lg border border-[var(--border-100)]/50 hover:bg-black/[0.02] dark:hover:bg-white/[0.02] transition-colors"
        >
          <span class="text-sm font-medium">{item}</span>
          <button
            type="button"
            class="p-1.5 rounded-md transition-colors cursor-pointer hover:bg-red-500/10 text-red-500/70 hover:text-red-500 disabled:opacity-50 disabled:cursor-not-allowed"
            onclick={() => onRemove(item)}
            {disabled}
            aria-label="Remove {item}"
            title="Remove species"
          >
            <Trash2 class="w-3.5 h-3.5" />
          </button>
        </div>
      {/each}
    {:else}
      <div class="text-sm text-muted italic py-4 text-center">
        {emptyMessage}
      </div>
    {/if}

    <!-- Add species input -->
    <div class="pt-2">
      <SpeciesInput
        bind:value={inputValue}
        label={inputLabel}
        placeholder={inputPlaceholder}
        {predictions}
        size="sm"
        {onInput}
        {onAdd}
        {disabled}
      />
    </div>
  </div>
</div>
