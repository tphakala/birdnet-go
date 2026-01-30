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
  <div class="modal modal-open">
    <div class="modal-box">
      <h3 class="font-bold text-lg">
        {t('system.database.legacy.cleanup.confirmTitle')}
      </h3>

      <p class="py-4">
        {t('system.database.legacy.cleanup.confirmMessage', { size: formatBytes(sizeBytes) })}
      </p>

      <div class="form-control">
        <label class="label cursor-pointer justify-start gap-3">
          <input
            type="checkbox"
            class="checkbox checkbox-error"
            bind:checked={confirmed}
            disabled={isLoading}
          />
          <span class="label-text">
            {t('system.database.legacy.cleanup.confirmCheckbox')}
          </span>
        </label>
      </div>

      <div class="modal-action">
        <button class="btn" onclick={onCancel} disabled={isLoading}>
          {t('system.database.legacy.cleanup.cancelButton')}
        </button>
        <button class="btn btn-error" onclick={onConfirm} disabled={!confirmed || isLoading}>
          {#if isLoading}
            <span class="loading loading-spinner loading-sm"></span>
          {/if}
          {t('system.database.legacy.cleanup.deleteConfirmButton')}
        </button>
      </div>
    </div>
    <div class="modal-backdrop" onclick={onCancel} role="presentation"></div>
  </div>
{/if}
