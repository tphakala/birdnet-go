<!--
  ModelDocumentText - the verified text of one hosted licence or policy document.

  Loads the text through the models API by its sha256 and shows it as plain,
  preformatted text in a keyboard-scrollable region. The text is never parsed as
  HTML or Markdown. While it loads a skeleton holds its place; when the server
  cannot fetch and verify it, the reason and a Retry replace it.

  @component
-->
<script lang="ts" module>
  /** Where a document's text is: loading, shown, or unavailable. */
  export type DocumentTextStatus = 'loading' | 'loaded' | 'error';
</script>

<script lang="ts">
  import { tick, untrack } from 'svelte';
  import { t } from '$lib/i18n';
  import { ApiError } from '$lib/utils/api';
  import { fetchModelDocument } from '$lib/utils/modelsApi';
  import { cn } from '$lib/utils/cn';
  import ErrorAlert from '$lib/desktop/components/ui/ErrorAlert.svelte';
  import Skeleton from '$lib/desktop/components/ui/Skeleton.svelte';
  import SkeletonStatus from '$lib/desktop/components/ui/SkeletonStatus.svelte';

  interface Props {
    /** sha256 of the hosted document. */
    sha256: string;
    /** Display name of the document, used to name the text region. */
    label: string;
    /** Called whenever the status changes, including the first load. */
    onStatus?: (_status: DocumentTextStatus) => void;
    className?: string;
  }

  let { sha256, label, onStatus, className = '' }: Props = $props();

  // Widths of the skeleton bars, varied so the placeholder reads as running text.
  const SKELETON_LINE_WIDTHS = ['w-1/3', 'w-full', 'w-11/12', 'w-full', 'w-4/5', 'w-full', 'w-2/3'];

  // The root element: Retry replaces the alert holding focus with the skeleton, so
  // focus falls back here instead of dropping to the page body. It is focusable only
  // while a Retry runs, so a dialog that opens on this component never gives it
  // initial focus and a click inside the alert never lands on it.
  let rootEl: HTMLDivElement | undefined;
  let regionEl = $state<HTMLDivElement | undefined>();
  let retryButton = $state<HTMLButtonElement | undefined>();
  let retrying = $state(false);

  let status = $state<DocumentTextStatus>('loading');
  let text = $state('');
  let errorMessage = $state('');
  // Bumped per request, so a response for an earlier sha256 or an earlier attempt
  // never overwrites a newer one.
  let requestSeq = 0;

  function setStatus(next: DocumentTextStatus) {
    status = next;
    onStatus?.(next);
  }

  async function load(sha: string): Promise<void> {
    const seq = ++requestSeq;
    setStatus('loading');
    try {
      const doc = await fetchModelDocument(sha);
      if (seq !== requestSeq) return;
      text = doc.text;
      setStatus('loaded');
      await tick();
      // A Retry that left focus on the root hands it to the text it loaded, before
      // the root stops being focusable.
      if (retrying && document.activeElement === rootEl) regionEl?.focus();
    } catch (e) {
      if (seq !== requestSeq) return;
      errorMessage =
        e instanceof ApiError ? e.userMessage : t('analysis.gallery.errors.documentUnavailable');
      setStatus('error');
      await tick();
      // A Retry that failed again hands focus back to the new Retry button.
      if (retrying && document.activeElement === rootEl) retryButton?.focus();
    } finally {
      if (seq === requestSeq) retrying = false;
    }
  }

  function retry(): Promise<void> {
    retrying = true;
    return load(sha256);
  }

  $effect(() => {
    const sha = sha256;
    untrack(() => void load(sha));
    return () => {
      // Drop any response still in flight when the document changes or the view closes.
      requestSeq++;
    };
  });
</script>

<!-- tabindex is -1 or absent: a focus target for Retry, never in the tab order. -->
<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<div bind:this={rootEl} tabindex={retrying ? -1 : undefined} class={cn('outline-none', className)}>
  {#if status === 'error'}
    <ErrorAlert
      message={errorMessage}
      onRetry={retry}
      focusFallback={() => [rootEl]}
      bind:retryButton
    />
  {:else if status === 'loading'}
    <div
      class="flex h-64 flex-col gap-2 rounded-lg border border-[var(--color-base-300)] bg-[var(--color-base-200)] p-3 text-sm"
    >
      <SkeletonStatus label={t('analysis.gallery.documents.loading')} />
      {#each SKELETON_LINE_WIDTHS as width, i (i)}
        <Skeleton className={cn('h-3', width)} />
      {/each}
    </div>
  {:else}
    <!-- Focusable so keyboard users can scroll the text; named after the document. The
         height matches the skeleton, so nothing below moves when the text arrives. -->
    <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
    <div
      bind:this={regionEl}
      role="region"
      aria-label={t('analysis.gallery.documents.textRegion', { document: label })}
      tabindex="0"
      class="h-64 overflow-y-auto rounded-lg border border-[var(--color-base-300)] bg-[var(--color-base-200)] p-3 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-primary)]"
    >
      <pre
        class="font-sans text-sm leading-relaxed break-words whitespace-pre-wrap text-[var(--color-base-content)]">{text}</pre>
    </div>
  {/if}
</div>
