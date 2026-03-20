<script lang="ts">
  import { t } from '$lib/i18n';
  import { RotateCw, Container } from '@lucide/svelte';
  import {
    restartState,
    restartInProgress,
    requestBinaryRestart,
    requestContainerRestart,
  } from '$lib/stores/restart.svelte';
  import ConfirmModal from '$lib/desktop/components/modals/ConfirmModal.svelte';

  let confirmType = $state<'binary' | 'container' | null>(null);

  async function handleConfirm(): Promise<void> {
    if (confirmType === 'binary') {
      await requestBinaryRestart();
    } else if (confirmType === 'container') {
      await requestContainerRestart();
    }
    confirmType = null;
  }

  const confirmMessage = $derived(
    confirmType === 'container'
      ? t('restart.confirmContainerMessage')
      : t('restart.confirmApplicationMessage')
  );
</script>

<div class="card bg-base-100 shadow-sm">
  <div class="card-body gap-4">
    <h3 class="card-title text-base">{t('restart.applicationRestart')}</h3>

    <div class="flex flex-wrap gap-3">
      <button
        class="btn btn-warning gap-2"
        disabled={restartInProgress.value}
        onclick={() => (confirmType = 'binary')}
      >
        <RotateCw class="h-4 w-4" />
        {t('restart.applicationRestart')}
      </button>

      {#if restartState.container_restart_available}
        <button
          class="btn btn-error gap-2"
          disabled={restartInProgress.value}
          onclick={() => (confirmType = 'container')}
        >
          <Container class="h-4 w-4" />
          {t('restart.containerRestart')}
        </button>
      {/if}
    </div>

    {#if restartInProgress.value}
      <div class="flex items-center gap-2 text-sm opacity-70" role="status">
        <div class="loading loading-spinner loading-xs"></div>
        <span>{t('restart.inProgress')}</span>
      </div>
    {/if}
  </div>
</div>

<ConfirmModal
  isOpen={confirmType !== null}
  title={t('restart.confirmTitle')}
  message={confirmMessage}
  confirmVariant={confirmType === 'container' ? 'error' : 'warning'}
  onClose={() => (confirmType = null)}
  onConfirm={handleConfirm}
/>
