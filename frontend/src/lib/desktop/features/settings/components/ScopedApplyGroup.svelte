<!--
  ScopedApplyGroup

  Purpose: Controls whose change restarts something (capture, a stream
  connection), saved together with a scoped Apply. Renders the controls, and
  below them, while the draft differs or an apply runs, a row with the hint,
  Cancel and the Apply button named for its effect. A failed apply keeps the
  draft and says so in the row.

  Props:
  - draft: The ApplyDraft holding the controls' values
  - onApply: Commits the draft (called through draft.apply)
  - hint: What applying does ("Capture restarts when you apply.")
  - failedHint: Shown in the row after a failed apply
  - applyLabel: Apply button text, named for its effect
  - applyingLabel: Apply button text while the apply is saved
  - disabled: Blocks the apply
  - children: The controls

  @component
-->
<script lang="ts" generics="T extends object">
  import type { Snippet } from 'svelte';
  import { RotateCcw } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import type { ApplyDraft } from './applyDraft.svelte';

  interface Props {
    draft: ApplyDraft<T>;
    onApply: (_value: T) => void;
    hint: string;
    failedHint: string;
    applyLabel: string;
    applyingLabel: string;
    disabled?: boolean;
    statusId?: string;
    children?: Snippet;
  }

  let {
    draft,
    onApply,
    hint,
    failedHint,
    applyLabel,
    applyingLabel,
    disabled = false,
    statusId,
    children,
  }: Props = $props();
</script>

{@render children?.()}
{#if draft.visible}
  <div
    class="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-[var(--border-200)] bg-[var(--color-base-200)] px-3 py-2"
  >
    <p class="flex items-center gap-2 text-sm text-[var(--color-base-content)]">
      <RotateCcw class="size-4 shrink-0 text-muted" aria-hidden="true" />
      {draft.failed ? failedHint : hint}
    </p>
    <div class="flex gap-2">
      <button
        type="button"
        class="btn btn-ghost btn-sm aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
        aria-disabled={draft.applying ? 'true' : undefined}
        onclick={() => draft.cancel()}
      >
        {t('common.cancel')}
      </button>
      <button
        type="button"
        class="btn btn-primary btn-sm aria-disabled:cursor-not-allowed aria-disabled:opacity-60"
        aria-disabled={draft.applying || disabled ? 'true' : undefined}
        aria-describedby={draft.failed ? statusId : undefined}
        onclick={() => {
          if (!disabled) draft.apply(onApply);
        }}
      >
        {draft.applying ? applyingLabel : applyLabel}
      </button>
    </div>
  </div>
{/if}
