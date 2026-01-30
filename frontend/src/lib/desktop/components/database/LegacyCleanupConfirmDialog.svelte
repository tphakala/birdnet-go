<!--
  LegacyCleanupConfirmDialog Component

  Confirmation dialog for legacy database deletion.
  Requires checkbox confirmation before allowing deletion.

  @component
-->
<script lang="ts">
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

{#if open}
  <div class="fixed inset-0 z-50 flex items-center justify-center">
    <div
      class="bg-[var(--color-base-100)] rounded-xl shadow-xl max-w-md w-full mx-4 p-6 border border-[var(--color-base-200)]"
      role="dialog"
      aria-modal="true"
      aria-labelledby="legacy-cleanup-confirm-title"
    >
      <h3
        id="legacy-cleanup-confirm-title"
        class="font-bold text-lg text-[var(--color-base-content)]"
      >
        {t('system.database.legacy.cleanup.confirmTitle')}
      </h3>

      <p class="py-4 text-[var(--color-base-content)]/80">
        {t('system.database.legacy.cleanup.confirmMessage', { size: formatBytes(sizeBytes) })}
      </p>

      <div class="mb-4">
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

      <div class="flex justify-end gap-2 mt-6">
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
            <span
              class="animate-spin rounded-full border-2 h-4 w-4 border-current border-t-transparent"
            ></span>
          {/if}
          {t('system.database.legacy.cleanup.deleteConfirmButton')}
        </button>
      </div>
    </div>
    <div class="fixed inset-0 bg-black/50 -z-10" onclick={onCancel} role="presentation"></div>
  </div>
{/if}
