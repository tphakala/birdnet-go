<!--
  Settings Page Actions Component

  Purpose: An inline save/reset actions bar that integrates directly into settings pages
  instead of floating at the bottom of the screen.

  Features:
  - Reset button (only visible when there are unsaved changes)
  - Save button with loading state
  - Integrates with global settings store
  - Accessible with proper ARIA attributes
  - Consistent DaisyUI 5 styling

  Props: None - Uses global settings stores

  @component
-->
<script lang="ts">
  import { settingsStore, settingsActions, hasUnsavedChanges } from '$lib/stores/settings.js';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';
  import { RefreshCw, Save } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';

  const logger = loggers.settings;

  let store = $derived($settingsStore);
  let unsavedChanges = $derived($hasUnsavedChanges);

  async function handleSave() {
    // Guard against calls when button should be disabled
    if (!unsavedChanges || store.isSaving) {
      return;
    }

    try {
      await settingsActions.saveSettings();
      // Success notification will be handled by the store/SSE
    } catch (error) {
      // Error is already handled in the store
      logger.error('Failed to save settings:', error);
    }
  }

  function handleReset() {
    // Guard against calls when button should be disabled
    if (store.isSaving) {
      return;
    }

    settingsActions.resetAllSettings();
  }
</script>

<!-- Inline Settings Actions Bar -->
<div
  class="mt-6 pt-4 border-t border-base-300"
  role="toolbar"
  aria-label={t('settings.actions.toolbar')}
>
  <div class="flex justify-end items-center gap-3">
    <!-- Reset button - only shows when there are unsaved changes -->
    {#if unsavedChanges}
      <button
        type="button"
        class="btn btn-ghost btn-sm gap-2"
        onclick={handleReset}
        disabled={store.isSaving}
        aria-label={t('settings.actions.resetAriaLabel')}
      >
        <RefreshCw class="size-4" aria-hidden="true" />
        {t('settings.actions.reset')}
      </button>
    {/if}

    <!-- Primary Save button -->
    <button
      type="button"
      class="btn btn-primary btn-sm gap-2"
      onclick={handleSave}
      disabled={!unsavedChanges || store.isSaving}
      aria-busy={store.isSaving}
      aria-label={store.isSaving
        ? t('settings.actions.savingAriaLabel')
        : t('settings.actions.saveAriaLabel')}
    >
      {#if store.isSaving}
        <LoadingSpinner size="xs" />
        {t('settings.actions.saving')}
      {:else}
        <Save class="size-4" aria-hidden="true" />
        {t('settings.actions.save')}
      {/if}
    </button>
  </div>
</div>
