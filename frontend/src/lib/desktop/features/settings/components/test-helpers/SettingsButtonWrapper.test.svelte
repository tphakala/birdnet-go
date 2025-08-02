<!--
  Test wrapper component for SettingsButton
  Used to test snippet props in Svelte 5
-->
<script lang="ts">
  import SettingsButton from '../SettingsButton.svelte';

  export let loading = false;
  export let loadingText = '';
  export let disabled = false;
  export let className = '';
  export let onclick = undefined;
  export let childContent = '';
</script>

<SettingsButton {loading} {loadingText} {disabled} {className} {onclick}>
  {#snippet children()}
    {#if childContent}
      <span data-testid="button-content">{childContent}</span>
    {/if}
  {/snippet}
</SettingsButton>
