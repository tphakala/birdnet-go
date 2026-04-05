<!--
  SpeciesConfigList - Styled row list for species configuration rules.

  Matches the notification rules list design from NotificationsSettingsPage.
  Each row shows species name, scientific name, threshold bar, interval,
  and action badges with edit/delete buttons.

  @component
-->
<script lang="ts">
  import { Pencil, Trash2, Clock, Settings2 } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import type { SpeciesConfig } from '$lib/stores/settings';

  interface Props {
    configs: Record<string, SpeciesConfig>;
    scientificNameMap: Map<string, string>;
    editingSpecies: string | null;
    disabled?: boolean;
    onEdit: (_species: string) => void;
    onDelete: (_species: string) => void;
  }

  let {
    configs,
    scientificNameMap,
    editingSpecies = null,
    disabled = false,
    onEdit,
    onDelete,
  }: Props = $props();

  let entries = $derived(Object.entries(configs));
  let editorOpen = $derived(editingSpecies !== null);
</script>

{#if entries.length === 0}
  <!-- Empty state -->
  <div class="text-center py-8">
    <Settings2 class="size-12 mx-auto mb-3 opacity-20 text-[var(--color-base-content)]" />
    <p class="text-sm font-medium text-[var(--color-base-content)]/60">
      {t('settings.species.customConfiguration.emptyState.title')}
    </p>
    <p class="text-xs mt-1 text-[var(--color-base-content)]/40">
      {t('settings.species.customConfiguration.emptyState.description')}
    </p>
  </div>
{:else}
  <div class="rounded-xl bg-[var(--color-base-100)] shadow-xs overflow-hidden">
    {#each entries as [species, config] (species)}
      {@const isEditing = editingSpecies === species}
      {@const scientificName = scientificNameMap.get(species.toLowerCase()) ?? ''}
      <div
        class="px-4 py-3 flex items-start gap-3 border-b border-[var(--color-base-200)] last:border-b-0 transition-colors {isEditing
          ? 'bg-[var(--color-primary)]/5 ring-1 ring-inset ring-[var(--color-primary)]/30'
          : 'hover:bg-[var(--color-base-200)]/30'}"
      >
        <!-- Content -->
        <div class="flex-1 min-w-0">
          <!-- Primary line: species name + action badge -->
          <div class="flex items-center gap-2 flex-wrap">
            <span class="text-sm font-medium text-[var(--color-base-content)]">{species}</span>
            {#if config.actions?.length > 0}
              <span
                class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-teal-500/10 text-teal-600 dark:text-teal-400"
              >
                {t('settings.species.customConfiguration.list.actionBadge')}
              </span>
            {/if}
          </div>

          <!-- Secondary: scientific name -->
          {#if scientificName}
            <p class="text-xs text-[var(--color-base-content)]/50 italic mt-0.5">
              {scientificName}
            </p>
          {/if}

          <!-- Metadata row: threshold + interval -->
          <div class="flex items-center gap-3 mt-1.5 text-xs text-[var(--color-base-content)]/50">
            <!-- Threshold bar + value -->
            <div class="flex items-center gap-2">
              <div class="w-20 h-1.5 rounded-full bg-[var(--color-base-300)] overflow-hidden">
                <div
                  class="h-full rounded-full bg-teal-500 transition-all"
                  style:width="{((config.threshold ?? 0) * 100).toFixed(0)}%"
                ></div>
              </div>
              <span class="font-mono tabular-nums font-medium">
                {(config.threshold ?? 0).toFixed(2)}
              </span>
            </div>

            <!-- Interval -->
            {#if config.interval > 0}
              <span class="w-px h-3 bg-[var(--color-base-300)]"></span>
              <span class="flex items-center gap-1">
                <Clock class="size-3" />
                {config.interval}s
              </span>
            {/if}
          </div>
        </div>

        <!-- Action buttons -->
        <div class="flex items-center gap-1 flex-shrink-0">
          <button
            type="button"
            class="inline-flex items-center justify-center size-7 rounded-md text-[var(--color-base-content)]/70 hover:bg-[var(--color-base-200)] hover:text-[var(--color-base-content)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
            title={t('settings.species.customConfiguration.list.editTitle')}
            aria-label={t('settings.species.customConfiguration.list.editTitle')}
            disabled={disabled || (editorOpen && !isEditing)}
            onclick={() => onEdit(species)}
          >
            <Pencil class="size-3.5" />
          </button>
          <button
            type="button"
            class="inline-flex items-center justify-center size-7 rounded-md text-[var(--color-base-content)]/70 hover:bg-[var(--color-error)]/10 hover:text-[var(--color-error)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
            title={t('settings.species.customConfiguration.list.removeTitle')}
            aria-label={t('settings.species.customConfiguration.list.removeTitle')}
            {disabled}
            onclick={() => onDelete(species)}
          >
            <Trash2 class="size-3.5" />
          </button>
        </div>
      </div>
    {/each}
  </div>
{/if}
