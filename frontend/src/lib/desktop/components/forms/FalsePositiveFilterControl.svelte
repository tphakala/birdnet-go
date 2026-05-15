<!--
  False Positive Filter Control

  Stepped slider for false positive filter level selection (0-5).
  Used by both bird and bat detection settings with different
  calculation parameters passed via the getDescription callback.

  @component
-->
<script lang="ts">
  import { cn } from '$lib/utils/cn.js';
  import { t } from '$lib/i18n';

  interface Props {
    id: string;
    level: number;
    onUpdate: (_level: number) => void;
    getDescription: (_level: number) => string;
    disabled?: boolean;
  }

  let { id, level, onUpdate, getDescription, disabled = false }: Props = $props();

  const LEVELS = [
    { value: 0, name: 'Off' },
    { value: 1, name: 'Lenient' },
    { value: 2, name: 'Moderate' },
    { value: 3, name: 'Balanced' },
    { value: 4, name: 'Strict' },
    { value: 5, name: 'Maximum' },
  ];

  function getLevelName(value: number): string {
    return LEVELS.find(l => l.value === value)?.name ?? 'Unknown';
  }

  function getBadgeClass(value: number): string {
    switch (value) {
      case 1:
        return 'bg-[var(--color-success)] text-[var(--color-success-content)]';
      case 2:
        return 'bg-[var(--color-info)] text-[var(--color-info-content)]';
      case 3:
        return 'bg-[var(--color-warning)] text-[var(--color-warning-content)]';
      case 4:
      case 5:
        return 'bg-[var(--color-error)] text-[var(--color-error-content)]';
      case 0:
      default:
        return 'bg-black/5 dark:bg-white/5 text-[var(--color-base-content)]';
    }
  }
</script>

<div class="min-w-0">
  <label for={id} class="flex items-center justify-between mb-2">
    <span class="text-sm font-medium text-[var(--color-base-content)]">
      {t('settings.main.sections.falsePositiveFilter.level.label')}
    </span>
    <span
      class={cn(
        'inline-flex items-center justify-center px-2 py-0.5 text-xs font-medium rounded-full',
        getBadgeClass(level)
      )}
    >
      {getLevelName(level)}
    </span>
  </label>

  <input
    {id}
    type="range"
    aria-describedby="{id}-help"
    class="w-full h-2 bg-[var(--color-base-300)] rounded-lg appearance-none cursor-pointer accent-[var(--color-primary)]"
    min={0}
    max={5}
    step={1}
    value={level}
    oninput={e => onUpdate(parseInt(e.currentTarget.value))}
    {disabled}
  />

  <!-- Stepped dots showing discrete levels -->
  <div class="flex justify-between px-[2px] mt-1.5" aria-hidden="true">
    {#each LEVELS as l (l.value)}
      <div
        class={cn(
          'size-1.5 rounded-full transition-colors',
          l.value <= level ? 'bg-[var(--color-primary)]' : 'bg-[var(--color-base-300)]'
        )}
        title={l.name}
      ></div>
    {/each}
  </div>

  <div class="mt-1">
    <span id="{id}-help" class="text-xs text-[var(--color-base-content)] opacity-60">
      {getDescription(level)}
    </span>
  </div>
</div>
