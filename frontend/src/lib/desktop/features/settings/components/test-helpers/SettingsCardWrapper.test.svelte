<!--
  Test wrapper component for SettingsCard
  Used to test snippet props in Svelte 5
-->
<script lang="ts">
  import SettingsCard from '../SettingsCard.svelte';

  export let title = '';
  export let description = '';
  export let hasChanges = false;
  export let className = '';
  export let padding = true;
  export let showCustomHeader = false;
  export let showCustomFooter = false;
  export let childContent = '';
  export let headerContent = '';
  export let footerContent = '';
</script>

<SettingsCard {title} {description} {hasChanges} {className} {padding}>
  {#snippet children()}
    {#if childContent}
      <div data-testid="child-content">{childContent}</div>
    {/if}
  {/snippet}

  {#snippet header()}
    {#if showCustomHeader}
      <div data-testid="custom-header">{headerContent}</div>
    {/if}
  {/snippet}

  {#snippet footer()}
    {#if showCustomFooter}
      <div data-testid="custom-footer">{footerContent}</div>
    {/if}
  {/snippet}
</SettingsCard>
