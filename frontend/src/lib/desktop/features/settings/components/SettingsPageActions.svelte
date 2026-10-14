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

  Props:
  - onReset: Optional callback after all settings are reset

  @component
-->
<script lang="ts">
  import {
    settingsStore,
    settingsActions,
    hasUnsavedChanges,
    settingsValidationErrors,
    settingsValidationReasons,
  } from '$lib/stores/settings.js';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';
  import { RefreshCw, Save } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { generateId } from '$lib/utils/uuid';

  const logger = loggers.settings;

  interface Props {
    onReset?: () => void;
  }

  let { onReset }: Props = $props();

  let store = $derived($settingsStore);
  let unsavedChanges = $derived($hasUnsavedChanges);
  let validationErrors = $derived($settingsValidationErrors);
  let canSave = $derived(unsavedChanges && validationErrors.length === 0);
  // Saving is blocked by a page's validation, not just by having nothing to save:
  // say so next to the button, with each page-provided reason.
  let blockedByValidation = $derived(unsavedChanges && validationErrors.length > 0);
  let blockedReasons = $derived(
    validationErrors
      .map(entry => $settingsValidationReasons.get(entry))
      .filter((reason): reason is string => reason !== undefined && reason !== '')
  );
  const SAVE_BLOCKED_ID = generateId('settings-save-blocked');

  async function handleSave() {
    // Guard against calls when button should be disabled
    if (!canSave || store.isSaving) {
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
    onReset?.();
  }
</script>

<!-- Inline Settings Actions Bar -->
<div
  class="mt-6 pt-4 border-t border-[var(--color-base-300)]"
  role="toolbar"
  aria-label={t('settings.actions.toolbar')}
>
  <div class="flex justify-end items-center gap-3">
    {#if blockedByValidation}
      <div id={SAVE_BLOCKED_ID} class="mr-auto min-w-0 text-sm text-[var(--text-error)]">
        <p>{t('settings.actions.validationBlocked')}</p>
        {#if blockedReasons.length > 0}
          <ul class="mt-1 list-disc pl-5 text-xs">
            {#each blockedReasons as reason, index (index)}
              <li>{reason}</li>
            {/each}
          </ul>
        {/if}
      </div>
    {/if}

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
      class="btn btn-primary btn-sm gap-2 shrink-0 aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
      onclick={handleSave}
      aria-disabled={!canSave || store.isSaving ? 'true' : undefined}
      aria-describedby={blockedByValidation ? SAVE_BLOCKED_ID : undefined}
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
