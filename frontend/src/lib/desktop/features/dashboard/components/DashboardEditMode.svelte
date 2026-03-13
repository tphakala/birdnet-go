<!--
  DashboardEditMode - Orchestrates dashboard editing.
  Provides drag-and-drop reordering, element toggling, and save/cancel flow.

  Usage: Wraps the dashboard content area. When editMode is true,
  elements become draggable and configurable.

  @component
-->
<script lang="ts">
  import { dndzone } from 'svelte-dnd-action';
  import { Pencil, Save, X } from '@lucide/svelte';
  import type { DashboardElement, DashboardLayout } from '$lib/stores/settings';
  import DashboardElementWrapper from './DashboardElementWrapper.svelte';
  import ElementConfigModal from './ElementConfigModal.svelte';
  import { api } from '$lib/utils/api';
  import { getLogger } from '$lib/utils/logger';
  import { t } from '$lib/i18n';
  import type { Snippet } from 'svelte';

  const logger = getLogger('dashboard');

  interface Props {
    layout: DashboardLayout;
    isAdmin: boolean;
    onLayoutChange: (_layout: DashboardLayout) => void;
    onEditModeChange: (_editing: boolean) => void;
    renderElement: Snippet<[element: DashboardElement, editMode: boolean]>;
  }

  let { layout, isAdmin, onLayoutChange, onEditModeChange, renderElement }: Props = $props();

  let editMode = $state(false);
  let editElements = $state<(DashboardElement & { id: string })[]>([]);
  let configElement = $state<DashboardElement | null>(null);
  let configModalOpen = $state(false);
  let isSaving = $state(false);

  // Enter edit mode: clone layout for editing
  function enterEditMode() {
    editElements = layout.elements.map((el, i) => ({
      ...(JSON.parse(JSON.stringify(el)) as DashboardElement),
      id: el.id ?? `${el.type}-${i}`,
    }));
    editMode = true;
    onEditModeChange(true);
  }

  // Cancel: discard changes
  function cancelEdit() {
    editMode = false;
    editElements = [];
    configElement = null;
    configModalOpen = false;
    onEditModeChange(false);
  }

  // Save: persist layout via dashboard API
  async function saveLayout() {
    isSaving = true;
    try {
      // Preserve id field for stable element identification
      const cleanElements: DashboardElement[] = editElements.map(el => ({
        id: el.id,
        type: el.type,
        enabled: el.enabled,
        ...(el.banner ? { banner: el.banner } : {}),
        ...(el.video ? { video: el.video } : {}),
        ...(el.summary ? { summary: el.summary } : {}),
        ...(el.grid ? { grid: el.grid } : {}),
      }));
      const newLayout: DashboardLayout = { elements: cleanElements };

      await api.patch('/api/v2/settings/dashboard', { layout: newLayout });
      onLayoutChange(newLayout);
      editMode = false;
      onEditModeChange(false);
    } catch (error) {
      logger.error('Failed to save dashboard layout:', error);
    } finally {
      isSaving = false;
    }
  }

  // Handle drag-and-drop reorder
  function handleDndConsider(e: CustomEvent<{ items: (DashboardElement & { id: string })[] }>) {
    editElements = e.detail.items;
  }

  function handleDndFinalize(e: CustomEvent<{ items: (DashboardElement & { id: string })[] }>) {
    editElements = e.detail.items;
  }

  // Toggle element enabled/disabled
  function toggleElement(index: number, enabled: boolean) {
    editElements = editElements.map((el, i) => (i === index ? { ...el, enabled } : el));
  }

  // Open config modal for an element
  function configureElement(index: number) {
    const el = editElements.find((_, i) => i === index);
    if (el) {
      configElement = el;
      configModalOpen = true;
    }
  }

  // Save element config from modal
  function saveElementConfig(updated: DashboardElement) {
    editElements = editElements.map(el =>
      el.id === (updated.id ?? updated.type) ? { ...el, ...updated } : el
    );
  }
</script>

{#if isAdmin && !editMode}
  <!-- Edit button (floating, bottom-right) -->
  <div class="fixed bottom-6 right-6 z-40">
    <button
      onclick={enterEditMode}
      class="flex items-center gap-2 rounded-full bg-[var(--color-primary)] px-4 py-2.5 text-[var(--color-primary-content)] shadow-lg transition-all hover:opacity-90"
    >
      <Pencil class="size-4" />
      <span class="text-sm font-medium">{t('dashboard.editMode.editButton')}</span>
    </button>
  </div>
{/if}

{#if editMode}
  <!-- Floating toolbar (top, centered) -->
  <div
    class="fixed left-1/2 top-4 z-50 flex -translate-x-1/2 items-center gap-3 rounded-full border border-[var(--color-base-200)] bg-[var(--color-base-100)] px-4 py-2 shadow-xl"
  >
    <span class="text-sm font-medium text-[var(--color-base-content)]/70"
      >{t('dashboard.editMode.editing')}</span
    >
    <div class="h-5 w-px bg-[var(--color-base-200)]"></div>
    <button
      onclick={saveLayout}
      disabled={isSaving}
      class="flex items-center gap-1.5 rounded-lg bg-[var(--color-primary)] px-3 py-1.5 text-sm font-medium text-[var(--color-primary-content)] transition-colors hover:opacity-90 disabled:opacity-50"
    >
      <Save class="size-3.5" />
      {isSaving ? t('dashboard.editMode.saving') : t('dashboard.editMode.save')}
    </button>
    <button
      onclick={cancelEdit}
      class="flex items-center gap-1.5 rounded-lg border border-[var(--color-base-content)]/30 px-3 py-1.5 text-sm font-medium transition-colors hover:bg-black/5 dark:hover:bg-white/10"
    >
      <X class="size-3.5" />
      {t('dashboard.editMode.cancel')}
    </button>
  </div>

  <!-- Drag-and-drop zone -->
  <div
    use:dndzone={{ items: editElements, flipDurationMs: 200 }}
    onconsider={handleDndConsider}
    onfinalize={handleDndFinalize}
    class="space-y-4 pt-16"
  >
    {#each editElements as element, index (element.id)}
      <DashboardElementWrapper
        elementType={element.type}
        enabled={element.enabled}
        {editMode}
        onToggle={enabled => toggleElement(index, enabled)}
        onConfigure={() => configureElement(index)}
      >
        {@render renderElement(element, true)}
      </DashboardElementWrapper>
    {/each}
  </div>

  <!-- Config modal -->
  {#if configElement}
    <ElementConfigModal
      element={configElement}
      open={configModalOpen}
      onSave={saveElementConfig}
      onClose={() => {
        configModalOpen = false;
        configElement = null;
      }}
    />
  {/if}
{:else}
  <!-- Normal mode: render elements from layout -->
  {#each layout.elements.filter(e => e.enabled) as element (element.id ?? element.type)}
    {@render renderElement(element, false)}
  {/each}
{/if}
