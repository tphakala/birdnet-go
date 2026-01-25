<!--
  MigrationConfirmDialog Component

  Modal dialog asking user to confirm they have backed up data before starting migration.
  Requires checkbox confirmation before the start button is enabled.

  Props:
  - open: Whether the dialog is open
  - onConfirm: Callback when user confirms (after checking the checkbox)
  - onCancel: Callback when user cancels or closes dialog
  - isLoading: Whether the confirm action is in progress

  @component
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import { AlertTriangle, Loader2 } from '@lucide/svelte';

  interface Props {
    open: boolean;
    onConfirm: () => void;
    onCancel: () => void;
    isLoading?: boolean;
  }

  let { open, onConfirm, onCancel, isLoading = false }: Props = $props();

  let confirmed = $state(false);

  function handleConfirm() {
    if (confirmed) {
      onConfirm();
    }
  }

  // Reset checkbox when dialog opens
  $effect(() => {
    if (open) {
      confirmed = false;
    }
  });
</script>

{#if open}
  <div class="fixed inset-0 z-50 flex items-center justify-center">
    <!-- Backdrop -->
    <button type="button" class="fixed inset-0 bg-black/50" onclick={onCancel} aria-label="Close"
    ></button>

    <!-- Dialog -->
    <div
      class="relative bg-[var(--color-base-100)] rounded-xl shadow-xl p-8 max-w-xl w-full mx-4 z-10"
    >
      <div class="flex items-center gap-4 mb-6">
        <div class="p-3 rounded-full bg-[var(--color-warning)]/10">
          <AlertTriangle class="size-7 text-[var(--color-warning)]" />
        </div>
        <h3 class="text-xl font-semibold text-[var(--color-base-content)]">
          {t('system.database.migration.confirmDialog.title')}
        </h3>
      </div>

      <p class="text-base text-[var(--color-base-content)]/80 mb-6 leading-relaxed">
        {t('system.database.migration.confirmDialog.message')}
      </p>

      <!-- Checkbox -->
      <label
        class="flex items-start gap-4 p-4 rounded-lg bg-[var(--color-base-200)] cursor-pointer mb-6"
      >
        <input
          type="checkbox"
          bind:checked={confirmed}
          class="mt-1 size-5 rounded border-[var(--color-base-300)]
                 text-[var(--color-primary)] focus:ring-[var(--color-primary)]"
        />
        <span class="text-base text-[var(--color-base-content)]">
          {t('system.database.migration.confirmDialog.checkbox')}
        </span>
      </label>

      <p class="text-sm text-[var(--color-base-content)]/60 mb-8">
        {t('system.database.migration.confirmDialog.note')}
      </p>

      <!-- Actions -->
      <div class="flex justify-end gap-3">
        <button
          class="inline-flex items-center justify-center gap-2 px-5 py-2.5
                 text-sm font-medium rounded-lg transition-colors
                 border border-[var(--color-base-300)]
                 text-[var(--color-base-content)]
                 hover:bg-[var(--color-base-200)]"
          onclick={onCancel}
        >
          {t('common.cancel')}
        </button>
        <button
          class="inline-flex items-center justify-center gap-2 px-5 py-2.5
                 text-sm font-medium rounded-lg transition-colors
                 bg-[var(--color-primary)] text-[var(--color-primary-content)]
                 hover:bg-[var(--color-primary)]/90
                 disabled:opacity-50 disabled:cursor-not-allowed"
          onclick={handleConfirm}
          disabled={!confirmed || isLoading}
        >
          {#if isLoading}
            <Loader2 class="size-4 animate-spin" />
          {/if}
          {t('system.database.migration.actions.start')}
        </button>
      </div>
    </div>
  </div>
{/if}
