<!--
  AutoSaveStatus

  Purpose: The inline feedback of one immediately saved setting. Shows nothing
  while idle, "Saving..." while its edit is waiting for or in a request, "Saved"
  for a moment after, and the reason with a dismiss button when the save failed
  or the setting was changed elsewhere. Unmounting clears an error of its
  field (registerAutoSaveField). A persistent visually hidden status
  region announces each state, because a region mounted together with its text
  is often not announced; the visible parts are not live regions themselves.

  Props:
  - field: The field key the control passed to commitSetting
  - id: Optional id of the visible error message; the control points
    aria-describedby at it while an error shows
  - class: Additional CSS classes

  @component
-->
<script lang="ts">
  import { Check, CircleAlert, X } from '@lucide/svelte';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';
  import { t } from '$lib/i18n';
  import {
    autoSaveStatus,
    dismissAutoSaveError,
    registerAutoSaveField,
  } from '$lib/stores/settingsAutoSave.svelte';
  import { cn } from '$lib/utils/cn';

  interface Props {
    field: string;
    id?: string;
    class?: string;
  }

  let { field, id, class: className }: Props = $props();

  let status = $derived(autoSaveStatus(field));

  // While mounted the field's status is shown here; on unmount its error is
  // cleared, so the tab's failure count never holds one nothing can dismiss.
  $effect(() => registerAutoSaveField(field));
  let announcement = $derived.by(() => {
    switch (status.state) {
      case 'saving':
        return t('settings.actions.saving');
      case 'saved':
        return t('settings.autoSave.saved');
      case 'error':
        return status.message;
      case 'idle':
        return '';
    }
  });

  let root = $state<HTMLDivElement>();

  // Dismissing removes the focused button, so focus moves to this wrapper first and
  // keeps the user's place beside the control instead of dropping to the page start.
  function dismiss() {
    root?.focus();
    dismissAutoSaveError(field);
  }
</script>

<div bind:this={root} tabindex="-1" class={cn('outline-none', className)}>
  <span role="status" class="sr-only">{announcement}</span>
  {#if status.state === 'saving'}
    <div class="flex items-center gap-1.5 text-sm" aria-hidden="true">
      <LoadingSpinner size="xs" aria-hidden="true" />
      <span class="text-[var(--text-subtle)]">{announcement}</span>
    </div>
  {:else if status.state === 'saved'}
    <div class="flex items-center gap-1.5 text-sm" aria-hidden="true">
      <Check class="size-4 text-[var(--text-success)]" aria-hidden="true" />
      <span class="text-[var(--text-success)]">{announcement}</span>
    </div>
  {:else if status.state === 'error'}
    <div {id} class="flex items-start gap-1.5 text-sm">
      <CircleAlert class="mt-0.5 size-4 shrink-0 text-[var(--text-error)]" aria-hidden="true" />
      <span class="min-w-0 text-[var(--text-error)]">{announcement}</span>
      <button
        type="button"
        class="btn btn-ghost btn-xs shrink-0"
        aria-label={t('settings.autoSave.dismiss')}
        onclick={dismiss}
      >
        <X class="size-3.5" aria-hidden="true" />
      </button>
    </div>
  {/if}
</div>
