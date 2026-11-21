<!--
  SourceCard

  Purpose: The shell of one audio source on a tab that saves with the page (a sound
  card, a stream). The header is always visible: a disclosure button with the
  icon, name and subtitle that opens and closes the body, the badges, and a
  delete button that asks for confirmation in an overlay first. The body holds
  the source's settings groups.

  Props:
  - name: Source name, the start of the disclosure button's name
  - subtitle: Second line (device, URL)
  - icon: Icon component shown in the header
  - expanded: Whether the body shows (the parent owns the open state)
  - onToggleExpanded: Called by the disclosure button
  - deleteConfirm: Question shown in the delete overlay; it names the source itself
  - onDelete: Called after the delete was confirmed
  - iconClass: Colours of the icon tile (background, text, border); primary by default
  - disabled: Blocks delete
  - disabledReason: Shown beside the delete button while disabled, and its description
  - nameAddon: Shown after the name inside the disclosure button (a status pill)
  - badges: Header badges after the disclosure button
  - actions: Header controls before delete (an enable switch)
  - details: Always visible lines under the header (stats, warnings)
  - children: The body

  @component
-->
<script lang="ts">
  import { tick, type Component, type Snippet } from 'svelte';
  import { slide } from 'svelte/transition';
  import { AlertCircle, ChevronRight, Trash2, type LucideProps } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import { generateId } from '$lib/utils/uuid';

  interface Props {
    name: string;
    subtitle?: string;
    icon: Component<LucideProps>;
    expanded: boolean;
    onToggleExpanded: () => void;
    deleteConfirm: string;
    onDelete: () => void;
    iconClass?: string;
    disabled?: boolean;
    disabledReason?: string;
    nameAddon?: Snippet;
    badges?: Snippet;
    actions?: Snippet;
    details?: Snippet;
    children?: Snippet;
  }

  let {
    name,
    subtitle,
    icon: Icon,
    expanded,
    onToggleExpanded,
    deleteConfirm,
    onDelete,
    iconClass,
    disabled = false,
    disabledReason,
    nameAddon,
    badges,
    actions,
    details,
    children,
  }: Props = $props();

  const DEFAULT_ICON_CLASS =
    'border-[color-mix(in_srgb,var(--color-primary)_25%,transparent)] bg-[color-mix(in_srgb,var(--color-primary)_15%,transparent)] text-[var(--text-primary)]';

  const cardId = generateId('source-card');
  const bodyId = `${cardId}-body`;
  const deleteTitleId = `${cardId}-delete-title`;
  const deleteDescId = `${cardId}-delete-desc`;
  const disabledReasonId = `${cardId}-disabled-reason`;

  let showDeleteConfirm = $state(false);
  let deleteButton = $state<HTMLButtonElement>();
  let cancelDeleteButton = $state<HTMLButtonElement>();

  async function confirmDelete() {
    if (disabled) return;
    showDeleteConfirm = true;
    await tick();
    cancelDeleteButton?.focus();
  }

  async function cancelDelete() {
    showDeleteConfirm = false;
    await tick();
    deleteButton?.focus();
  }

  function executeDelete() {
    if (disabled) {
      void cancelDelete();
      return;
    }
    showDeleteConfirm = false;
    onDelete();
  }

  function handleDeleteKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault();
      void cancelDelete();
    }
  }
</script>

<div
  class={cn(
    'relative rounded-lg border border-[var(--border-200)] transition-colors duration-200',
    expanded ? 'bg-[var(--color-base-100)] shadow-sm' : 'bg-[var(--color-base-200)]'
  )}
>
  {#if showDeleteConfirm}
    <div
      role="alertdialog"
      aria-labelledby={deleteTitleId}
      aria-describedby={deleteDescId}
      tabindex="-1"
      onkeydown={handleDeleteKeydown}
      class="absolute inset-x-0 top-0 z-10 flex min-h-16 items-center rounded-lg bg-[var(--color-base-300)] px-4 py-3"
    >
      <div class="flex w-full items-center gap-3">
        <AlertCircle class="size-6 shrink-0 text-[var(--text-error)]" aria-hidden="true" />
        <span id={deleteTitleId} class="sr-only">{t('common.delete')}</span>
        <p id={deleteDescId} class="flex-1 text-sm font-medium text-[var(--color-base-content)]">
          {deleteConfirm}
        </p>
        <div class="flex shrink-0 gap-2">
          <button
            bind:this={cancelDeleteButton}
            type="button"
            class="inline-flex h-8 items-center justify-center rounded-lg px-3 text-sm font-medium transition-colors hover:bg-[var(--color-base-content)]/10"
            onclick={cancelDelete}
          >
            {t('common.cancel')}
          </button>
          <button
            type="button"
            class="inline-flex h-8 items-center justify-center rounded-lg bg-[var(--color-error)] px-3 text-sm font-medium text-[var(--color-error-content)] transition-colors hover:bg-[var(--color-error-hover)]"
            onclick={executeDelete}
          >
            {t('common.delete')}
          </button>
        </div>
      </div>
    </div>
  {/if}

  <!-- Header: the button toggles the body; delete sits beside it -->
  <div class="flex items-center gap-2 p-3">
    <button
      type="button"
      class="flex min-w-56 flex-1 items-center gap-3 rounded-lg text-left focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-primary)]"
      aria-expanded={expanded}
      aria-controls={bodyId}
      onclick={onToggleExpanded}
    >
      <ChevronRight
        class={cn(
          'size-4 shrink-0 text-muted transition-transform duration-200 motion-reduce:transition-none',
          expanded && 'rotate-90'
        )}
        aria-hidden="true"
      />
      <span
        class={cn(
          'flex size-10 shrink-0 items-center justify-center rounded-lg border',
          iconClass ?? DEFAULT_ICON_CLASS
        )}
      >
        <Icon class="size-5" aria-hidden="true" />
      </span>
      <span class="min-w-0 flex-1">
        {#if nameAddon}
          <span class="flex min-w-0 items-center gap-2">
            <span
              class="truncate text-sm font-semibold text-[var(--color-base-content)]"
              title={name}
            >
              {name}
            </span>
            {@render nameAddon()}
          </span>
        {:else}
          <span
            class="block truncate text-sm font-semibold text-[var(--color-base-content)]"
            title={name}
          >
            {name}
          </span>
        {/if}
        {#if subtitle}
          <span class="mt-0.5 block truncate text-xs text-muted">{subtitle}</span>
        {/if}
      </span>
    </button>

    {#if badges}
      <div class="flex min-w-0 max-w-1/2 flex-wrap items-center justify-end gap-1.5">
        {@render badges()}
      </div>
    {/if}

    {#if actions}
      <div class="flex shrink-0 items-center">
        {@render actions()}
      </div>
    {/if}

    {#if disabled && disabledReason}
      <span id={disabledReasonId} class="shrink-0 text-xs text-muted">{disabledReason}</span>
    {/if}
    <button
      bind:this={deleteButton}
      type="button"
      class="inline-flex size-8 shrink-0 items-center justify-center rounded-lg transition-colors hover:bg-[var(--color-base-content)]/10 aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
      onclick={confirmDelete}
      aria-disabled={disabled ? 'true' : undefined}
      aria-describedby={disabled && disabledReason ? disabledReasonId : undefined}
      aria-label={t('common.delete')}
    >
      <Trash2 class="size-4" aria-hidden="true" />
    </button>
  </div>

  {#if details}
    <div class="-mt-1 space-y-1.5 px-3 pb-3 pl-23">
      {@render details()}
    </div>
  {/if}

  {#if expanded}
    <div
      id={bodyId}
      inert={showDeleteConfirm}
      class="divide-y divide-[var(--border-100)] border-t border-[var(--border-100)] px-4"
      transition:slide={{ duration: 150 }}
    >
      {@render children?.()}
    </div>
  {/if}
</div>
