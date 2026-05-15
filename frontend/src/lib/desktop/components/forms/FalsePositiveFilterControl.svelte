<!--
  False Positive Filter Control

  Stepped slider for false positive filter level selection.
  Accepts a configurable levels array so bird and bat filters
  can expose different level sets (bat has fewer distinct levels).

  @component
-->
<script lang="ts">
  import { cn } from '$lib/utils/cn.js';
  import { t } from '$lib/i18n';
  import { safeArrayAccess } from '$lib/utils/security';

  export interface FilterLevel {
    value: number;
    nameKey: string;
    badgeClass: string;
  }

  interface Props {
    id: string;
    level: number;
    levels: FilterLevel[];
    onUpdate: (_level: number) => void;
    getDescription: (_level: number) => string;
    disabled?: boolean;
  }

  let { id, level, levels, onUpdate, getDescription, disabled = false }: Props = $props();

  const sliderPosition = $derived(
    Math.max(
      0,
      levels.findIndex(l => l.value === level)
    )
  );

  const currentLevel = $derived(safeArrayAccess(levels, sliderPosition));

  function handleInput(e: Event) {
    const pos = parseInt((e.currentTarget as HTMLInputElement).value);
    const target = safeArrayAccess(levels, pos);
    if (target) onUpdate(target.value);
  }
</script>

<div class="min-w-0">
  <label for={id} class="flex items-center justify-between mb-2">
    <span class="text-sm font-medium text-[var(--color-base-content)]">
      {t('settings.main.sections.falsePositiveFilter.level.label')}
    </span>
    {#if currentLevel}
      <span
        class={cn(
          'inline-flex items-center justify-center px-2 py-0.5 text-xs font-medium rounded-full',
          currentLevel.badgeClass
        )}
      >
        {t(currentLevel.nameKey)}
      </span>
    {/if}
  </label>

  <input
    {id}
    type="range"
    aria-describedby="{id}-help"
    class="w-full h-2 bg-[var(--color-base-300)] rounded-lg appearance-none cursor-pointer accent-[var(--color-primary)]"
    min={0}
    max={levels.length - 1}
    step={1}
    value={sliderPosition}
    oninput={handleInput}
    {disabled}
  />

  <!-- Stepped dots showing discrete levels -->
  <div class="flex justify-between px-[2px] mt-1.5" aria-hidden="true">
    {#each levels as _, i (i)}
      <div
        class={cn(
          'size-1.5 rounded-full transition-colors',
          i <= sliderPosition ? 'bg-[var(--color-primary)]' : 'bg-[var(--color-base-300)]'
        )}
      ></div>
    {/each}
  </div>

  <div class="mt-1">
    <span id="{id}-help" class="text-xs text-[var(--color-base-content)] opacity-60">
      {getDescription(level)}
    </span>
  </div>
</div>
