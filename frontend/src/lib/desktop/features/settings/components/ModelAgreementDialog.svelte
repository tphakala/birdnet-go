<!--
  ModelAgreementDialog - the agreement step of a model install.

  Shows each document the user must agree to before a model install, reinstall or
  variant swap starts: its verified text as plain text, and one checkbox per
  document. The primary action stays unavailable, with the reason shown, until
  every text has loaded and every box is ticked. A document whose text cannot be
  loaded offers Retry and no way to agree to it. Closing the dialog in any way
  other than agreeing calls onCancel.

  @component
-->
<script lang="ts">
  import { untrack } from 'svelte';
  import { t } from '$lib/i18n';
  import type { AgreementDocument } from '$lib/types/models';
  import { documentKindLabel } from '$lib/utils/modelDocuments';
  import { generateId } from '$lib/utils/uuid';
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import ModelDocumentText, { type DocumentTextStatus } from './ModelDocumentText.svelte';

  interface Props {
    /** Whether the dialog is open. The parent controls it; a native close calls onCancel. */
    open: boolean;
    /** Catalog name of the model being installed. */
    modelName: string;
    /** The documents that need an agreement, each agreed to separately. */
    documents: AgreementDocument[];
    /** Called with the sha256 of every document once the user agreed to all of them. */
    onAgree: (_sha256s: string[]) => void;
    onCancel: () => void;
  }

  let { open, modelName, documents, onAgree, onCancel }: Props = $props();

  // A plain binding: the native dialog is driven through showModal() and close().
  let dialogEl: HTMLDialogElement | null = null;
  // Initial focus goes to Cancel, the safe action, rather than whatever the browser
  // finds first while the document texts are still loading.
  let cancelEl = $state<HTMLButtonElement | null>(null);

  const TITLE_ID = generateId('agreement-dialog-title');
  const REASON_ID = generateId('agreement-dialog-reason');
  const idPrefix = generateId('agreement-doc');

  let statuses = $state<Record<string, DocumentTextStatus>>({});
  let ticked = $state<Record<string, boolean>>({});

  // The content mounts only while open, so clearing the state on close starts the
  // next request with nothing loaded and nothing ticked.
  $effect(() => {
    if (open) {
      dialogEl?.showModal();
      untrack(() => cancelEl)?.focus();
    } else {
      dialogEl?.close();
      statuses = {};
      ticked = {};
    }
  });

  const anyUnavailable = $derived(documents.some(d => statuses[d.sha256] === 'error'));
  const allLoaded = $derived(documents.every(d => statuses[d.sha256] === 'loaded'));
  const allTicked = $derived(documents.every(d => ticked[d.sha256] === true));
  // A box shows only once its text has loaded, so every box ticked means every text was shown.
  const canAgree = $derived(documents.length > 0 && allTicked);
  const blockedReason = $derived(
    anyUnavailable
      ? t('analysis.gallery.agreement.unavailableBlocked')
      : !allLoaded
        ? t('analysis.gallery.agreement.waitForText')
        : !allTicked
          ? t('analysis.gallery.agreement.tickEach')
          : ''
  );

  function handleAgree(e: MouseEvent) {
    if (!canAgree) {
      e.preventDefault();
      return;
    }
    onAgree(documents.map(d => d.sha256));
  }
</script>

<dialog
  bind:this={dialogEl}
  onclose={() => {
    // The close event is delivered as a task, so one left over from an earlier
    // closing can arrive after the dialog was shown again for a new request; only a
    // close of the dialog that is showing now (Escape) cancels.
    if (open && !dialogEl?.open) onCancel();
  }}
  class="m-auto w-full max-w-3xl rounded-xl border border-[var(--color-base-300)] bg-[var(--color-base-100)] p-0 shadow-xl backdrop:bg-black/50"
  aria-labelledby={TITLE_ID}
>
  {#if open}
    <div class="p-6">
      <h3 id={TITLE_ID} class="text-lg font-semibold text-[var(--color-base-content)]">
        {t('analysis.gallery.agreement.title')}
      </h3>
      <p class="mt-2 text-sm text-[var(--color-base-content)]/80">
        {t('analysis.gallery.agreement.intro', { name: modelName })}
      </p>

      <div class="mt-4 space-y-6">
        {#each documents as doc, i (doc.sha256)}
          {@const kindLabel = documentKindLabel(doc.kind)}
          <section aria-labelledby="{idPrefix}-{i}-title">
            <h4
              id="{idPrefix}-{i}-title"
              class="text-base font-semibold text-[var(--color-base-content)]"
            >
              {kindLabel}
            </h4>
            <p class="mb-2 text-xs break-all text-[var(--color-base-content)]/70">
              {t('analysis.gallery.documents.source', { repo: doc.repo })}
            </p>
            <ModelDocumentText
              sha256={doc.sha256}
              label={kindLabel}
              onStatus={status => (statuses[doc.sha256] = status)}
            />
            <!-- The row keeps the checkbox's height before the text loads, so the
                 checkbox appearing does not move the buttons below. -->
            <div data-agree-row class="mt-3 min-h-7">
              {#if statuses[doc.sha256] === 'loaded'}
                <Checkbox
                  id="{idPrefix}-{i}-agree"
                  size="sm"
                  checked={ticked[doc.sha256] === true}
                  label={t('analysis.gallery.agreement.agree', { document: kindLabel })}
                  onchange={checked => (ticked[doc.sha256] = checked)}
                />
              {/if}
            </div>
          </section>
        {/each}
      </div>

      <p id={REASON_ID} class="mt-6 min-h-5 text-sm text-[var(--color-base-content)]/80">
        {blockedReason}
      </p>
      <div class="mt-3 flex justify-end gap-3">
        <button
          bind:this={cancelEl}
          type="button"
          onclick={onCancel}
          class="rounded-lg border border-[var(--color-base-300)] px-4 py-2 text-sm font-medium text-[var(--color-base-content)] hover:bg-[var(--color-base-200)] transition-colors"
        >
          {t('common.cancel')}
        </button>
        <button
          type="button"
          onclick={handleAgree}
          aria-disabled={canAgree ? undefined : 'true'}
          aria-describedby={canAgree ? undefined : REASON_ID}
          class="inline-flex items-center gap-2 rounded-lg bg-[var(--color-primary)] px-4 py-2 text-sm font-medium text-[var(--color-primary-content)] hover:not-aria-disabled:bg-[var(--color-primary-hover)] transition-colors aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
        >
          {t('analysis.gallery.agreement.agreeAndContinue')}
        </button>
      </div>
    </div>
  {/if}
</dialog>
