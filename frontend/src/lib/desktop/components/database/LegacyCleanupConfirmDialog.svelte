<!--
  LegacyCleanupConfirmDialog Component

  Confirmation dialog for legacy database deletion.
  Requires checkbox confirmation before allowing deletion.

  @component
-->
<script lang="ts">
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import { t } from '$lib/i18n';
  import { formatBytes } from '$lib/utils/formatters';

  interface Props {
    open: boolean;
    sizeBytes: number;
    isLoading: boolean;
    onConfirm: () => void;
    onCancel: () => void;
  }

  let { open, sizeBytes, isLoading, onConfirm, onCancel }: Props = $props();

  let confirmed = $state(false);

  // Reset confirmation when dialog opens
  $effect(() => {
    if (open) {
      confirmed = false;
    }
  });
</script>

<Modal
  isOpen={open}
  title={t('system.database.legacy.cleanup.confirmTitle')}
  size="sm"
  type="default"
  closeOnBackdrop={!isLoading}
  closeOnEsc={!isLoading}
  showCloseButton={false}
  loading={isLoading}
  onClose={onCancel}
>
  {#snippet children()}
    <p class="text-[var(--color-base-content)]/80">
      {t('system.database.legacy.cleanup.confirmMessage', { size: formatBytes(sizeBytes) })}
    </p>

    <div class="mt-4">
      <label class="flex items-center cursor-pointer gap-3" for="legacy-cleanup-confirm-checkbox">
        <input
          id="legacy-cleanup-confirm-checkbox"
          type="checkbox"
          class="w-4 h-4 rounded border-[var(--color-base-300)] text-[var(--color-error)] focus:ring-[var(--color-error)]"
          bind:checked={confirmed}
          disabled={isLoading}
        />
        <span class="text-sm text-[var(--color-base-content)]/80">
          {t('system.database.legacy.cleanup.confirmCheckbox')}
        </span>
      </label>
    </div>
  {/snippet}

  {#snippet footer()}
    <button
      class="px-4 py-2 rounded-lg font-medium transition-colors
             border border-[var(--color-base-300)]
             text-[var(--color-base-content)]
             hover:bg-[var(--color-base-200)]
             disabled:opacity-50 disabled:cursor-not-allowed"
      onclick={onCancel}
      disabled={isLoading}
    >
      {t('system.database.legacy.cleanup.cancelButton')}
    </button>
    <button
      class="px-4 py-2 rounded-lg font-medium transition-colors
             bg-[var(--color-error)] text-white
             hover:bg-[var(--color-error)]/90
             disabled:opacity-50 disabled:cursor-not-allowed
             inline-flex items-center gap-2"
      onclick={onConfirm}
      disabled={!confirmed || isLoading}
    >
      {#if isLoading}
        <span class="animate-spin rounded-full border-2 h-4 w-4 border-current border-t-transparent"
        ></span>
      {/if}
      {t('system.database.legacy.cleanup.deleteConfirmButton')}
    </button>
  {/snippet}
</Modal>
