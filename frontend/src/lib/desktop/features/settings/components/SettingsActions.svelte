<script lang="ts">
  import { settingsStore, settingsActions, hasUnsavedChanges } from '$lib/stores/settings.js';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';
  import { actionIcons } from '$lib/utils/icons';
  import { t } from '$lib/i18n';

  let store = $derived($settingsStore);
  let unsavedChanges = $derived($hasUnsavedChanges);

  async function handleSave() {
    try {
      await settingsActions.saveSettings();
      // Success notification will be handled by the store/SSE
    } catch (error) {
      // Error is already handled in the store
      console.error('Failed to save settings:', error);
    }
  }

  function handleReset() {
    settingsActions.resetAllSettings();
  }
</script>

<!-- Save Actions Bar - Matches old Alpine.js styling but right-aligned -->
<div class="flex justify-end items-center gap-3 mt-6 pt-6">
  <!-- Reset button - subtle and less prominent -->
  {#if unsavedChanges}
    <button
      type="button"
      class="btn btn-ghost btn-sm"
      onclick={handleReset}
      disabled={store.isSaving}
      aria-label={t('settings.actions.resetAriaLabel')}
    >
      {@html actionIcons.refresh}
      {t('settings.actions.reset')}
    </button>
  {/if}

  <!-- Primary Save button - matches old Alpine.js style -->
  <button
    type="button"
    class="btn btn-primary"
    onclick={handleSave}
    disabled={!unsavedChanges || store.isSaving}
    aria-busy={store.isSaving}
    aria-label={store.isSaving ? t('settings.actions.savingAriaLabel') : t('settings.actions.saveAriaLabel')}
  >
    {#if store.isSaving}
      <LoadingSpinner size="sm" />
      {t('settings.actions.saving')}
    {:else}
      {t('settings.actions.save')}
    {/if}
  </button>
</div>
