<!--
  AutoSaveStatus

  Purpose: The inline feedback of one immediately saved setting. Shows nothing
  while idle, "Saving..." while its edit is waiting for or in a request, "Saved"
  for a moment after, and the reason with a dismiss button when the save failed
  or the setting was changed elsewhere.

  Props:
  - field: The field key the control passed to commitSetting
  - id: Optional id of the region; the control points aria-describedby at it
    while an error shows
  - class: Additional CSS classes

  @component
-->
<script lang="ts">
  import { Check, CircleAlert, X } from '@lucide/svelte';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';
  import { t } from '$lib/i18n';
  import { autoSaveStatus, dismissAutoSaveError } from '$lib/stores/settingsAutoSave.svelte';
  import { cn } from '$lib/utils/cn';

  interface Props {
    field: string;
    id?: string;
    class?: string;
  }

  let { field, id, class: className }: Props = $props();

  let status = $derived(autoSaveStatus(field));
</script>

{#if status.state === 'saving'}
  <div {id} role="status" class={cn('flex items-center gap-1.5 text-sm', className)}>
    <LoadingSpinner size="xs" aria-hidden="true" />
    <span class="text-[var(--color-base-content)]/70">{t('settings.actions.saving')}</span>
  </div>
{:else if status.state === 'saved'}
  <div {id} role="status" class={cn('flex items-center gap-1.5 text-sm', className)}>
    <Check class="size-4 text-[var(--text-success)]" aria-hidden="true" />
    <span class="text-[var(--text-success)]">{t('settings.autoSave.saved')}</span>
  </div>
{:else if status.state === 'error'}
  <div {id} role="alert" class={cn('flex items-start gap-1.5 text-sm', className)}>
    <CircleAlert class="mt-0.5 size-4 shrink-0 text-[var(--text-error)]" aria-hidden="true" />
    <span class="min-w-0 text-[var(--text-error)]">{status.message}</span>
    <button
      type="button"
      class="btn btn-ghost btn-xs shrink-0"
      aria-label={t('settings.autoSave.dismiss')}
      onclick={() => dismissAutoSaveError(field)}
    >
      <X class="size-3.5" aria-hidden="true" />
    </button>
  </div>
{/if}
