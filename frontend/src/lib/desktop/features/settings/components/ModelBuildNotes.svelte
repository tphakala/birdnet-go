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
</script>

<div lang="en" class={cn('space-y-2 text-[var(--color-base-content)]', className)}>
  {#if record.reason}
    <p class="font-medium">{record.reason}</p>
  {/if}
  {#if record.notes && record.notes.length > 0}
    <ul class="list-disc space-y-1 pl-5">
      {#each record.notes as note, i (i)}
        <li>{note}</li>
      {/each}
    </ul>
  {/if}
</div>
