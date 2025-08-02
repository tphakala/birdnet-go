<!--
  Test wrapper component for SettingsNote
  Used to test snippet props in Svelte 5
-->
<script lang="ts">
  import SettingsNote from '../SettingsNote.svelte';

  export let className = '';
  export let showIcon = false;
  export let childContent = '';
</script>

<SettingsNote {className}>
  {#snippet children()}
    {#if childContent}
      <div data-testid="note-content">{@html childContent}</div>
    {/if}
  {/snippet}

  {#snippet icon()}
    {#if showIcon}
      <svg data-testid="note-icon" class="w-5 h-5">
        <circle cx="12" cy="12" r="10" fill="currentColor" />
      </svg>
    {/if}
  {/snippet}
</SettingsNote>
