<script lang="ts">
  import { settingsStore, settingsActions, hasUnsavedChanges } from '$lib/stores/settings.js';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';

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
      aria-label="Reset all changes"
    >
      <svg
        xmlns="http://www.w3.org/2000/svg"
        class="h-4 w-4"
        fill="none"
        viewBox="0 0 24 24"
        stroke="currentColor"
      >
        <path
          stroke-linecap="round"
          stroke-linejoin="round"
          stroke-width="2"
          d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"
        />
      </svg>
      Reset
    </button>
  {/if}

  <!-- Primary Save button - matches old Alpine.js style -->
  <button
    type="button"
    class="btn btn-primary"
    onclick={handleSave}
    disabled={!unsavedChanges || store.isSaving}
    aria-busy={store.isSaving}
    aria-label={store.isSaving ? 'Saving changes...' : 'Save Changes'}
  >
    {#if store.isSaving}
      <LoadingSpinner size="sm" />
      Saving...
    {:else}
      Save Changes
    {/if}
  </button>
</div>
