<script lang="ts">
  import { t } from '$lib/i18n';
  import { AlertTriangle } from '@lucide/svelte';
  import {
    restartState,
    restartInProgress,
    requestBinaryRestart,
  } from '$lib/stores/restart.svelte';
  import ConfirmModal from '$lib/desktop/components/modals/ConfirmModal.svelte';

  let showConfirm = $state(false);

  async function handleRestart(): Promise<void> {
    showConfirm = false;
    await requestBinaryRestart();
  }
</script>

{#if restartState.restart_required && !restartInProgress.value}
  <div
    class="flex items-center gap-3 px-4 py-3 text-sm bg-[var(--color-warning)]/10 border-b border-[var(--color-warning)]/30"
    role="alert"
  >
    <AlertTriangle class="h-5 w-5 shrink-0 text-[var(--color-warning)]" />
    <div class="flex-1">
      <span class="font-medium">{t('restart.bannerTitle')}:</span>
      {restartState.restart_reasons.join(', ')}
    </div>
    <button class="btn btn-sm btn-warning" onclick={() => (showConfirm = true)}>
      {t('restart.bannerAction')}
    </button>
  </div>
{/if}

{#if restartInProgress.value}
  <div
    class="flex items-center gap-3 px-4 py-3 text-sm bg-[var(--color-info)]/10 border-b border-[var(--color-info)]/30"
    role="status"
  >
    <div class="loading loading-spinner loading-sm text-[var(--color-info)]"></div>
    <span>{t('restart.inProgress')}</span>
  </div>
{/if}

<ConfirmModal
  isOpen={showConfirm}
  title={t('restart.confirmTitle')}
  message={t('restart.confirmApplicationMessage')}
  confirmVariant="warning"
  onClose={() => (showConfirm = false)}
  onConfirm={handleRestart}
/>
