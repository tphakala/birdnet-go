<!--
  EditorFooter - Footer action bar for inline editor cards.

  Features:
  - Delete button on the left (optional, danger style)
  - Cancel and Save buttons on the right
  - Loading spinner on save
  - Disabled state for save button

  @component
-->
<script lang="ts">
  import { Trash2 } from '@lucide/svelte';
  import { t } from '$lib/i18n';

  interface Props {
    onSave: () => void;
    onCancel: () => void;
    onDelete?: () => void;
    saveLabel?: string;
    cancelLabel?: string;
    deleteLabel?: string;
    saveDisabled?: boolean;
    saving?: boolean;
  }

  let {
    onSave,
    onCancel,
    onDelete,
    saveLabel,
    cancelLabel,
    deleteLabel,
    saveDisabled = false,
    saving = false,
  }: Props = $props();

  let effectiveSaveLabel = $derived(saveLabel || t('common.buttons.save'));
  let effectiveCancelLabel = $derived(cancelLabel || t('common.buttons.cancel'));
  let effectiveDeleteLabel = $derived(deleteLabel || t('common.buttons.delete'));
  let isDisabled = $derived(saveDisabled || saving);
</script>

<div class="flex items-center justify-between pt-2">
  <div>
    {#if onDelete}
      <button
        type="button"
        class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium text-[var(--color-error)] hover:bg-[var(--color-error)]/10 transition-colors cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
        onclick={onDelete}
        disabled={saving}
      >
        <Trash2 class="w-3.5 h-3.5" />
        {effectiveDeleteLabel}
      </button>
    {/if}
  </div>
  <div class="flex items-center gap-2">
    <button
      type="button"
      class="px-4 py-1.5 rounded-lg text-xs font-medium text-[var(--color-base-content)]/60 hover:bg-[var(--color-base-200)] transition-colors cursor-pointer"
      onclick={onCancel}
      disabled={saving}
    >
      {effectiveCancelLabel}
    </button>
    <button
      type="button"
      onclick={onSave}
      disabled={isDisabled}
      class="px-4 py-1.5 rounded-lg text-xs font-medium bg-[var(--color-primary)] text-[var(--color-primary-content)] hover:opacity-90 transition-colors cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
    >
      {#if saving}
        <span
          class="inline-block w-3 h-3 border-2 border-[var(--color-primary-content)] border-t-transparent rounded-full animate-spin mr-1"
        ></span>
      {/if}
      {effectiveSaveLabel}
    </button>
  </div>
</div>
