<!--
  ModelBuildNotes - the release notes of one model build: why the build exists as a lead
  sentence, then what changed as a list. The text is English manifest text from upstream,
  shown as plain text (never parsed as HTML) and marked lang="en" so a screen reader reads
  it in English whatever the UI locale. The caller adds the heading or disclosure around it.

  @component
-->
<script lang="ts">
  import type { CatalogBuild } from '$lib/types/models';
  import { cn } from '$lib/utils/cn';

  interface Props {
    /** The build's release notes record. */
    record: CatalogBuild;
    className?: string;
  }

  let { record, className = '' }: Props = $props();

  // Blank text shows nothing: no empty lead paragraph, no empty bullet.
  const reason = $derived(record.reason?.trim() ?? '');
  const notes = $derived((record.notes ?? []).filter(note => note.trim() !== ''));
</script>

<div lang="en" class={cn('space-y-2 text-[var(--color-base-content)]', className)}>
  {#if reason}
    <p class="font-medium">{reason}</p>
  {/if}
  {#if notes.length > 0}
    <ul class="list-disc space-y-1 pl-5">
      {#each notes as note, i (i)}
        <li>{note}</li>
      {/each}
    </ul>
  {/if}
</div>
