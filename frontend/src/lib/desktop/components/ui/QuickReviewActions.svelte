<!--
  QuickReviewActions

  Visible triage controls for a detection. The complete action menu remains
  available for secondary actions; these controls put the normal review path in
  reach without hiding it behind a menu.
-->
<script lang="ts">
  import type { Detection } from '$lib/types/detection.types';
  import { auth } from '$lib/stores/auth';
  import { t } from '$lib/i18n';
  import { CircleCheck, CircleX, Trash2 } from '@lucide/svelte';

  interface Props {
    detection: Detection;
    onAccept?: () => void;
    onReject?: () => void;
    onDelete?: () => void;
    compact?: boolean;
  }

  let { detection, onAccept, onReject, onDelete, compact = false }: Props = $props();

  const canEdit = $derived(!$auth.security.enabled || $auth.security.accessAllowed);
  const disabled = $derived(!canEdit || detection.locked);

  function run(action: (() => void) | undefined, event: MouseEvent) {
    event.stopPropagation();
    if (!disabled) action?.();
  }
</script>

{#if canEdit && (onAccept || onReject || onDelete)}
  <div
    class:compact
    class="quick-review-actions flex items-center gap-1"
    aria-label="Detection review actions"
    onclick={event => event.stopPropagation()}
  >
    {#if onAccept}
      <button
        type="button"
        class="btn btn-xs flex-1 border-[var(--color-success)]/40 bg-[var(--color-success)]/15 text-[var(--color-success)] hover:bg-[var(--color-success)]/25"
        class:ring-2={detection.verified === 'correct'}
        class:ring-[var(--color-success)]={detection.verified === 'correct'}
        onclick={event => run(onAccept, event)}
        disabled={disabled}
      >
        <CircleCheck class="size-4" />
        <span class:sr-only={compact}>{t('dashboard.recentDetections.actions.markCorrect')}</span>
      </button>
    {/if}

    {#if onReject}
      <button
        type="button"
        class="btn btn-xs flex-1 border-[var(--color-error)]/40 bg-[var(--color-error)]/15 text-[var(--color-error)] hover:bg-[var(--color-error)]/25"
        class:ring-2={detection.verified === 'false_positive'}
        class:ring-[var(--color-error)]={detection.verified === 'false_positive'}
        onclick={event => run(onReject, event)}
        disabled={disabled}
      >
        <CircleX class="size-4" />
        <span class:sr-only={compact}>{t('dashboard.recentDetections.actions.markFalsePositive')}</span>
      </button>
    {/if}

    {#if onDelete}
      <button
        type="button"
        class="btn btn-xs btn-ghost text-[var(--color-error)] hover:bg-[var(--color-error)]/15"
        aria-label={t('dashboard.recentDetections.actions.deleteDetection')}
        title={t('dashboard.recentDetections.actions.deleteDetection')}
        onclick={event => run(onDelete, event)}
        disabled={disabled}
      >
        <Trash2 class="size-4" />
        <span class:sr-only={compact}>{t('dashboard.recentDetections.actions.deleteDetection')}</span>
      </button>
    {/if}
  </div>
{/if}

<style>
  .quick-review-actions:not(.compact) {
    padding: 0.5rem 0.25rem 0 0.25rem;
  }

  .quick-review-actions.compact {
    min-width: 7rem;
  }
</style>
