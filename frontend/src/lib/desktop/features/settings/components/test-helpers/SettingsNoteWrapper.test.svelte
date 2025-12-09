<!--
  Test wrapper component for SettingsNote
  Used to test snippet props in Svelte 5
-->
<script lang="ts">
  import SettingsNote from '../SettingsNote.svelte';
  import { Info } from '@lucide/svelte';

  export let className: string = '';
  export let showIcon: boolean = false;
  export let childContent: string = '';
</script>

<SettingsNote {className}>
  {#snippet children()}
    {#if childContent}
      <div data-testid="note-content">{@html childContent}</div>
    {/if}
  {/snippet}

  {#snippet icon()}
    {#if showIcon}
      <Info data-testid="note-icon" class="size-5" />
    {/if}
  {/snippet}
</SettingsNote>
