<!--
  Managed Species Lists Tab Component

  Purpose: CRUD interface for database-backed managed species lists used by alerting & extended capture.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '$lib/utils/api';
  import { t } from '$lib/i18n';
  import { toastActions } from '$lib/stores/toast';
  import { Plus, Trash2, Edit2, X, List, ArrowLeft, Search, Loader2 } from '@lucide/svelte';
  import SpeciesInput from '$lib/desktop/components/forms/SpeciesInput.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import { fetchAlertRules } from '$lib/api/alerts';
  import type { AlertRule } from '$lib/api/alerts';
  import { settingsStore } from '$lib/stores/settings';
  import { translateField } from '$lib/utils/notifications';

  interface SpeciesListMember {
    id?: number;
    list_id?: number;
    scientific_name: string;
  }

  interface SpeciesList {
    id?: number;
    name: string;
    description: string;
    is_system?: boolean;
    members?: SpeciesListMember[];
  }

  // Lists state
  let lists = $state<SpeciesList[]>([]);
  let loading = $state(false);
  let searchQuery = $state('');

  // Autocomplete state
  let allSpeciesList = $state<
    Array<{ commonName?: string; scientificName?: string; label: string }>
  >([]);
  let scientificNamePredictions = $state<string[]>([]);
  let predictions = $state<string[]>([]);
  let speciesInputValue = $state('');

  // Editor state
  let isEditing = $state(false);
  let editorListId = $state<number | null>(null);
  let editorName = $state('');
  let editorDescription = $state('');
  let editorSpecies = $state<string[]>([]);
  let saving = $state(false);
  let manualSpeciesInput = $state('');

  // Delete state
  let deleteConfirmId = $state<number | null>(null);

  // Alert rules for usage tracking
  let alertRules = $state<AlertRule[]>([]);

  // Compute usage for a given list id: returns labels describing where the list is used
  function getListUsages(listId: number): string[] {
    const usages: string[] = [];
    const listRef = `list:${listId}`;

    // Check alert rules
    for (const rule of alertRules) {
      const refersToList = rule.conditions?.some(
        c =>
          (c.property === 'species_name' || c.property === 'scientific_name') && c.value === listRef
      );
      if (refersToList) {
        const displayName = translateField(rule.name_key, undefined, rule.name);
        usages.push(
          t('settings.species.managedLists.usage.usedInAlertRule', { name: displayName })
        );
      }
    }

    // Check settings store capture configs
    const realtime = $settingsStore.formData.realtime;
    if (realtime?.extendedCapture?.species?.includes(listRef)) {
      usages.push(t('settings.species.managedLists.usage.usedInExtendedCapture'));
    }
    if (realtime?.dogBarkFilter?.species?.includes(listRef)) {
      usages.push(t('settings.species.managedLists.usage.usedInDogBarkFilter'));
    }
    if (realtime?.daylightFilter?.species?.includes(listRef)) {
      usages.push(t('settings.species.managedLists.usage.usedInDaylightFilter'));
    }

    return usages;
  }

  onMount(async () => {
    await loadLists();
    await loadAutocompleteData();
    // Load alert rules for usage overview (best-effort, non-blocking)
    try {
      alertRules = await fetchAlertRules();
    } catch (err) {
      console.warn('Could not load alert rules for usage overview', err);
    }
  });

  async function loadLists() {
    loading = true;
    try {
      const response = await api.get<{ lists: SpeciesList[] }>('/api/v2/species-lists');
      lists = response.lists ?? [];
    } catch (err) {
      toastActions.error(t('settings.species.managedLists.toasts.loadFailed'));
      console.error(err);
    } finally {
      loading = false;
    }
  }

  async function loadAutocompleteData() {
    try {
      interface SpeciesListResponse {
        species?: Array<{ commonName?: string; scientificName?: string; label: string }>;
      }
      const data = await api.get<SpeciesListResponse>('/api/v2/species/all');
      allSpeciesList = data.species ?? [];
      scientificNamePredictions = allSpeciesList
        .filter(s => s.scientificName)
        .map(s => `${s.scientificName} (${s.commonName || s.label})`);
    } catch (err) {
      console.error('Failed to load autocomplete species list', err);
    }
  }

  /** Resolve a canonical scientific name to a user-friendly display label.
   *  Uses the already-loaded allSpeciesList from /api/v2/species/all. */
  function displayName(scientific: string): string {
    const entry = allSpeciesList.find(
      s => s.scientificName?.toLowerCase() === scientific.toLowerCase()
    );
    if (entry) {
      const common = entry.commonName || entry.label;
      return common ? `${common} (${scientific})` : scientific;
    }
    return scientific;
  }

  function isUnrecognized(scientific: string): boolean {
    if (!allSpeciesList || allSpeciesList.length === 0) return false;
    return !allSpeciesList.some(s => s.scientificName?.toLowerCase() === scientific.toLowerCase());
  }

  function translateListName(list: SpeciesList): string {
    if (list.is_system) {
      if (list.name === 'YAML: Extended Capture') {
        return t('settings.species.managedLists.system.extendedCapture.name');
      }
      if (list.name === 'YAML: Dog Bark Filter') {
        return t('settings.species.managedLists.system.dogBark.name');
      }
      if (list.name === 'YAML: Daylight Filter') {
        return t('settings.species.managedLists.system.daylight.name');
      }
    }
    return list.name;
  }

  function translateListDescription(list: SpeciesList): string {
    if (list.is_system) {
      if (
        list.description ===
        'Configured via realtime.extendedcapture.species settings in config.yaml'
      ) {
        return t('settings.species.managedLists.system.extendedCapture.description');
      }
      if (
        list.description === 'Configured via realtime.dogbarkfilter.species settings in config.yaml'
      ) {
        return t('settings.species.managedLists.system.dogBark.description');
      }
      if (
        list.description ===
        'Configured via realtime.daylightfilter.species settings in config.yaml'
      ) {
        return t('settings.species.managedLists.system.daylight.description');
      }
    }
    return list.description || t('settings.species.managedLists.noDescription');
  }

  function handleSpeciesInput(input: string) {
    if (!input || input.length < 2) {
      predictions = [];
      return;
    }
    const lower = input.toLowerCase();
    predictions = scientificNamePredictions
      .filter(p => p.toLowerCase().includes(lower))
      .slice(0, 10);
  }

  function handlePredictionSelect(prediction: string) {
    const scientificName = extractScientificName(prediction);
    if (scientificName && !editorSpecies.includes(scientificName)) {
      editorSpecies = [...editorSpecies, scientificName];
    }
    speciesInputValue = '';
    predictions = [];
  }

  function addManualSpecies() {
    // Normalize to lowercase canonical form (OpenFauna convention).
    const sp = manualSpeciesInput.trim().toLowerCase();
    if (sp && !editorSpecies.includes(sp)) {
      editorSpecies = [...editorSpecies, sp];
    }
    manualSpeciesInput = '';
  }

  function removeSpecies(sp: string) {
    editorSpecies = editorSpecies.filter(s => s !== sp);
  }

  function extractScientificName(prediction: string): string {
    const parenIndex = prediction.indexOf(' (');
    return parenIndex > 0 ? prediction.substring(0, parenIndex).trim() : prediction.trim();
  }

  function startCreate() {
    editorListId = null;
    editorName = '';
    editorDescription = '';
    editorSpecies = [];
    isEditing = true;
  }

  function startEdit(list: SpeciesList) {
    editorListId = list.id ?? null;
    editorName = list.name;
    editorDescription = list.description;
    editorSpecies = list.members?.map(m => m.scientific_name) ?? [];
    isEditing = true;
  }

  async function saveList() {
    if (!editorName.trim()) {
      toastActions.error(t('settings.species.managedLists.toasts.nameRequired'));
      return;
    }
    saving = true;
    try {
      const payload = {
        name: editorName.trim(),
        description: editorDescription.trim(),
        species: editorSpecies,
      };
      if (editorListId) {
        await api.put(`/api/v2/species-lists/${editorListId}`, payload);
        toastActions.success(t('settings.species.managedLists.toasts.updateSuccess'));
      } else {
        await api.post('/api/v2/species-lists', payload);
        toastActions.success(t('settings.species.managedLists.toasts.createSuccess'));
      }
      isEditing = false;
      await loadLists();
    } catch (err) {
      toastActions.error(t('settings.species.managedLists.toasts.saveFailed'));
      console.error(err);
    } finally {
      saving = false;
    }
  }

  async function deleteList(id: number) {
    try {
      await api.delete(`/api/v2/species-lists/${id}`);
      toastActions.success(t('settings.species.managedLists.toasts.deleteSuccess'));
      deleteConfirmId = null;
      await loadLists();
    } catch (err) {
      toastActions.error(t('settings.species.managedLists.toasts.deleteFailed'));
      console.error(err);
    }
  }

  // Filtered lists for the search input — matches name, description,
  // and member scientific/common names so users can find lists by species.
  let filteredLists = $derived(
    searchQuery
      ? lists.filter(l => {
          const q = searchQuery.toLowerCase();
          if (l.name.toLowerCase().includes(q) || l.description.toLowerCase().includes(q)) {
            return true;
          }
          // Search member scientific names and their display names (common names)
          return (
            l.members?.some(
              m =>
                m.scientific_name.includes(q) ||
                displayName(m.scientific_name).toLowerCase().includes(q)
            ) ?? false
          );
        })
      : lists
  );
</script>

<div class="space-y-6">
  {#if !isEditing}
    <!-- List View -->
    <div class="flex flex-col sm:flex-row gap-4 justify-between items-start sm:items-center">
      <div>
        <h2 class="text-lg font-semibold text-[var(--color-text)]">
          {t('settings.species.managedLists.title')}
        </h2>
        <p class="text-xs text-muted mt-1">
          {t('settings.species.managedLists.description')}
        </p>
      </div>
      <button
        type="button"
        onclick={startCreate}
        class="inline-flex items-center gap-2 h-9 px-4 text-sm font-medium rounded-lg bg-violet-500 hover:bg-violet-600 text-white transition-colors"
      >
        <Plus class="w-4 h-4" />
        {t('settings.species.managedLists.createList')}
      </button>
    </div>

    <!-- Search -->
    <div class="relative w-full max-w-md">
      <Search class="absolute left-3 top-2.5 w-4 h-4 text-muted" />
      <input
        type="text"
        placeholder={t('settings.species.managedLists.searchPlaceholder')}
        bind:value={searchQuery}
        class="w-full pl-9 pr-4 py-2 rounded-lg border border-[color-mix(in_srgb,var(--color-border)_50%,transparent)] bg-[var(--color-bg-input)] text-sm text-[var(--color-text)] focus:border-violet-500 focus:outline-none"
      />
    </div>

    <!-- Lists Grid -->
    {#if loading}
      <div class="flex justify-center items-center py-12">
        <Loader2 class="w-8 h-8 animate-spin text-violet-500" />
      </div>
    {:else if filteredLists.length === 0}
      <div
        class="border border-dashed border-[color-mix(in_srgb,var(--color-border)_50%,transparent)] rounded-xl p-8 text-center bg-[color-mix(in_srgb,var(--color-border)_2%,transparent)]"
      >
        <List class="w-12 h-12 mx-auto text-muted mb-3 opacity-40" />
        <h3 class="font-medium text-sm text-[var(--color-text)]">
          {t('settings.species.managedLists.noListsFound')}
        </h3>
        <p class="text-xs text-muted mt-1">
          {t('settings.species.managedLists.noListsHint')}
        </p>
      </div>
    {:else}
      <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
        {#each filteredLists as list (list.id)}
          <div
            class="flex flex-col justify-between bg-[var(--color-bg-card)] border border-[color-mix(in_srgb,var(--color-border)_50%,transparent)] rounded-xl p-5 hover:border-[color-mix(in_srgb,var(--color-border)_80%,transparent)] transition-all"
          >
            <div>
              <div class="flex items-start justify-between gap-4">
                <h3 class="font-semibold text-base text-[var(--color-text)]">
                  {translateListName(list)}
                </h3>
                <div class="flex items-center gap-1.5 shrink-0">
                  {#if list.is_system}
                    <span
                      class="inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-semibold bg-amber-500/15 text-amber-500 border border-amber-500/20"
                    >
                      {t('settings.species.managedLists.yamlBacked')}
                    </span>
                  {/if}
                  <span
                    class="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium bg-violet-500/10 text-violet-500 border border-violet-500/20"
                  >
                    {list.members?.length === 1
                      ? t('settings.species.managedLists.speciesCountOne', { count: 1 })
                      : t('settings.species.managedLists.speciesCount', {
                          count: list.members?.length ?? 0,
                        })}
                  </span>
                </div>
              </div>
              <p class="text-xs text-muted mt-2 line-clamp-2 min-h-[2rem]">
                {translateListDescription(list)}
              </p>

              <!-- Species Preview (up to 6 species) -->
              {#if list.members && list.members.length > 0}
                <div class="flex flex-wrap gap-1.5 mt-4">
                  {#each list.members.slice(0, 6) as member}
                    {@const unrecognized = isUnrecognized(member.scientific_name)}
                    <span
                      class="text-[10px] px-2 py-0.5 rounded select-none {unrecognized
                        ? 'bg-red-500/10 text-red-500 border border-red-500/20'
                        : 'bg-[color-mix(in_srgb,var(--color-border)_30%,transparent)] text-muted'}"
                      title={unrecognized ? 'Unrecognized species (Edit list to correct)' : ''}
                    >
                      {unrecognized ? '⚠️ ' : ''}{displayName(member.scientific_name)}
                    </span>
                  {/each}
                  {#if list.members.length > 6}
                    <span
                      class="text-[10px] px-2 py-0.5 rounded bg-violet-500/5 text-violet-400 select-none"
                    >
                      {t('settings.species.managedLists.moreCount', {
                        count: list.members.length - 6,
                      })}
                    </span>
                  {/if}
                </div>
              {/if}

              <!-- Usage Overview -->
              {#if list.id != null}
                {@const usages = getListUsages(list.id)}
                <div
                  class="mt-4 pt-3 border-t border-[color-mix(in_srgb,var(--color-border)_20%,transparent)]"
                >
                  <p class="text-[10px] font-medium uppercase tracking-wide text-muted mb-1.5">
                    {t('settings.species.managedLists.usage.usageLabel')}
                  </p>
                  {#if usages.length === 0}
                    <span
                      class="inline-flex items-center gap-1 text-[10px] px-2 py-0.5 rounded bg-slate-500/5 text-muted/60 border border-[color-mix(in_srgb,var(--color-border)_20%,transparent)] italic"
                    >
                      {t('settings.species.managedLists.usage.notUsed')}
                    </span>
                  {:else}
                    <div class="flex flex-wrap gap-1.5">
                      {#each usages as usage}
                        <span
                          class="inline-flex items-center gap-1 text-[10px] px-2 py-0.5 rounded-full font-medium bg-violet-500/10 text-violet-500 border border-violet-500/20"
                        >
                          {usage}
                        </span>
                      {/each}
                    </div>
                  {/if}
                </div>
              {/if}
            </div>

            <div
              class="flex items-center justify-end gap-2 border-t border-[color-mix(in_srgb,var(--color-border)_30%,transparent)] mt-5 pt-4"
            >
              {#if list.is_system}
                <span class="text-xs text-muted italic mr-auto"
                  >{t('settings.alerts.editor.readOnlyYaml')}</span
                >
              {:else if deleteConfirmId === list.id}
                <span class="text-xs text-[var(--color-error)] font-medium mr-auto"
                  >{t('settings.species.managedLists.confirmDelete')}</span
                >
                <button
                  type="button"
                  onclick={() => deleteList(list.id!)}
                  class="h-8 px-3 rounded-lg text-xs font-semibold bg-[var(--color-error)] hover:bg-[color-mix(in_srgb,var(--color-error)_90%,black)] text-white transition-colors"
                >
                  {t('settings.species.managedLists.yesDelete')}
                </button>
                <button
                  type="button"
                  onclick={() => (deleteConfirmId = null)}
                  class="h-8 px-3 rounded-lg text-xs font-medium bg-slate-500/10 hover:bg-slate-500/20 text-[var(--color-text)] transition-colors"
                >
                  {t('settings.species.managedLists.cancel')}
                </button>
              {:else}
                <button
                  type="button"
                  onclick={() => startEdit(list)}
                  class="inline-flex items-center gap-1.5 h-8 px-3 rounded-lg text-xs font-semibold bg-slate-500/10 hover:bg-slate-500/20 text-[var(--color-text)] transition-colors"
                >
                  <Edit2 class="w-3.5 h-3.5" />
                  {t('settings.species.managedLists.edit')}
                </button>
                <button
                  type="button"
                  onclick={() => (deleteConfirmId = list.id!)}
                  class="inline-flex items-center gap-1.5 h-8 px-3 rounded-lg text-xs font-semibold bg-red-500/10 hover:bg-red-500/20 text-red-500 transition-colors"
                >
                  <Trash2 class="w-3.5 h-3.5" />
                  {t('settings.species.managedLists.delete')}
                </button>
              {/if}
            </div>
          </div>
        {/each}
      </div>
    {/if}
  {:else}
    <!-- Create / Edit Form -->
    <div
      class="bg-[var(--color-bg-card)] border border-[color-mix(in_srgb,var(--color-border)_50%,transparent)] rounded-xl overflow-hidden"
    >
      <div
        class="flex items-center gap-3 p-5 border-b border-[color-mix(in_srgb,var(--color-border)_30%,transparent)] bg-[color-mix(in_srgb,var(--color-border)_2%,transparent)]"
      >
        <button
          type="button"
          onclick={() => (isEditing = false)}
          class="inline-flex items-center justify-center w-8 h-8 rounded-lg hover:bg-slate-500/10 text-[var(--color-text)] transition-colors"
          title={t('settings.species.managedLists.backTitle')}
        >
          <ArrowLeft class="w-4 h-4" />
        </button>
        <div>
          <h3 class="font-semibold text-base text-[var(--color-text)]">
            {editorListId
              ? t('settings.species.managedLists.editTitle')
              : t('settings.species.managedLists.createTitle')}
          </h3>
          <p class="text-xs text-muted">
            {editorListId
              ? t('settings.species.managedLists.editSub')
              : t('settings.species.managedLists.createSub')}
          </p>
        </div>
      </div>

      <div class="p-6 space-y-6">
        <!-- Details -->
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
          <TextInput
            bind:value={editorName}
            label={t('settings.species.managedLists.listName')}
            placeholder={t('settings.species.managedLists.listNamePlaceholder')}
            disabled={saving}
            required
          />
          <TextInput
            bind:value={editorDescription}
            label={t('settings.species.managedLists.descriptionLabel')}
            placeholder={t('settings.species.managedLists.descriptionPlaceholder')}
            disabled={saving}
          />
        </div>

        <hr class="border-[color-mix(in_srgb,var(--color-border)_30%,transparent)]" />

        <!-- Species Input -->
        <div class="space-y-4">
          <h4 class="font-medium text-sm text-[var(--color-text)]">
            {t('settings.species.managedLists.addHeader')}
          </h4>

          <div class="grid grid-cols-1 md:grid-cols-[1fr_auto] gap-3 items-end">
            <SpeciesInput
              bind:value={speciesInputValue}
              label={t('settings.species.managedLists.searchLabel')}
              {predictions}
              disabled={saving}
              onInput={handleSpeciesInput}
              onPredictionSelect={handlePredictionSelect}
              addOnSelect={false}
              size="sm"
            />
            <div class="flex gap-2">
              <input
                type="text"
                placeholder={t('settings.species.managedLists.manualLabel')}
                bind:value={manualSpeciesInput}
                disabled={saving}
                class="h-9 px-3 rounded-lg border border-[color-mix(in_srgb,var(--color-border)_50%,transparent)] bg-[var(--color-bg-input)] text-sm text-[var(--color-text)] focus:border-violet-500 focus:outline-none"
              />
              <button
                type="button"
                onclick={addManualSpecies}
                disabled={saving || !manualSpeciesInput.trim()}
                class="inline-flex items-center gap-1.5 h-9 px-4 text-sm font-semibold rounded-lg bg-violet-500 hover:bg-violet-600 text-white transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              >
                {t('settings.species.managedLists.addManual')}
              </button>
            </div>
          </div>

          <!-- Added Species Tags -->
          <div class="space-y-2">
            <div class="flex items-center justify-between">
              <span class="text-xs font-semibold text-muted uppercase tracking-wider">
                {t('settings.species.managedLists.tabLabel')} ({editorSpecies.length})
              </span>
              {#if editorSpecies.length > 0}
                <button
                  type="button"
                  onclick={() => (editorSpecies = [])}
                  class="text-xs text-red-500 hover:underline"
                >
                  {t('settings.species.managedLists.clearAll')}
                </button>
              {/if}
            </div>

            {#if editorSpecies.length === 0}
              <div
                class="text-center py-6 border border-dashed border-[color-mix(in_srgb,var(--color-border)_30%,transparent)] rounded-lg text-muted text-xs"
              >
                {t('settings.species.managedLists.noSpeciesInList')}
              </div>
            {:else}
              <div
                class="flex flex-wrap gap-2 p-4 border border-[color-mix(in_srgb,var(--color-border)_30%,transparent)] bg-[color-mix(in_srgb,var(--color-border)_2%,transparent)] rounded-lg max-h-[250px] overflow-y-auto"
              >
                {#each editorSpecies as sp}
                  {@const unrecognized = isUnrecognized(sp)}
                  <span
                    class="inline-flex items-center gap-1.5 px-3 py-1 rounded-full text-xs font-medium border select-none {unrecognized
                      ? 'bg-red-500/10 text-red-500 border-red-500/20'
                      : 'bg-violet-500/10 text-violet-500 border-violet-500/20'}"
                    title={unrecognized ? 'Unrecognized species (edit or remove)' : ''}
                  >
                    {unrecognized ? '⚠️ ' : ''}{displayName(sp)}
                    <button
                      type="button"
                      onclick={() => removeSpecies(sp)}
                      disabled={saving}
                      class="{unrecognized
                        ? 'text-red-500 hover:text-red-700'
                        : 'text-violet-500 hover:text-violet-700'} focus:outline-none"
                    >
                      <X class="w-3.5 h-3.5" />
                    </button>
                  </span>
                {/each}
              </div>
            {/if}
          </div>
        </div>
      </div>

      <div
        class="flex items-center justify-end gap-3 p-5 border-t border-[color-mix(in_srgb,var(--color-border)_30%,transparent)] bg-[color-mix(in_srgb,var(--color-border)_2%,transparent)]"
      >
        <button
          type="button"
          onclick={() => (isEditing = false)}
          disabled={saving}
          class="h-9 px-4 rounded-lg text-sm font-semibold bg-slate-500/10 hover:bg-slate-500/20 text-[var(--color-text)] transition-colors disabled:opacity-50"
        >
          {t('settings.species.managedLists.cancelEditor')}
        </button>
        <button
          type="button"
          onclick={saveList}
          disabled={saving || !editorName.trim()}
          class="inline-flex items-center gap-1.5 h-9 px-4 text-sm font-semibold rounded-lg bg-violet-500 hover:bg-violet-600 text-white transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {#if saving}
            <Loader2 class="w-4 h-4 animate-spin" />
            {t('settings.species.managedLists.saving')}
          {:else}
            {t('settings.species.managedLists.save')}
          {/if}
        </button>
      </div>
    </div>
  {/if}
</div>
