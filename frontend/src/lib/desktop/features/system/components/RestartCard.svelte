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

<div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm">
  <h3 class="text-xs font-semibold uppercase tracking-wider mb-3 text-muted">
    {t('restart.applicationRestart')}
  </h3>

  <div class="flex flex-wrap gap-3">
    <button
      class="inline-flex items-center gap-2 px-4 py-2 rounded-lg text-sm font-medium
             bg-amber-500/15 text-[var(--text-warning)]
             border border-amber-500/30
             hover:bg-amber-500/25 active:bg-amber-500/35
             disabled:opacity-50 disabled:cursor-not-allowed
             focus-visible:ring-2 focus-visible:ring-amber-500/50
             transition-colors cursor-pointer"
      disabled={restartInProgress.value}
      onclick={() => (confirmType = 'binary')}
    >
      <RotateCw class="h-3.5 w-3.5" />
      {t('restart.applicationRestart')}
    </button>

    {#if restartState.container_restart_available}
      <button
        class="inline-flex items-center gap-2 px-4 py-2 rounded-lg text-sm font-medium
               bg-red-500/15 text-red-700 dark:text-red-300
               border border-red-500/30
               hover:bg-red-500/25 active:bg-red-500/35
               disabled:opacity-50 disabled:cursor-not-allowed
               focus-visible:ring-2 focus-visible:ring-red-500/50
               transition-colors cursor-pointer"
        disabled={restartInProgress.value}
        onclick={() => (confirmType = 'container')}
      >
        <Container class="h-3.5 w-3.5" />
        {t('restart.containerRestart')}
      </button>
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
